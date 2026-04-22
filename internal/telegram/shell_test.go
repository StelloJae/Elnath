package telegram

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/routing"
	"github.com/stello/elnath/internal/skill"
	"github.com/stello/elnath/internal/wiki"
)

type fakeBotClient struct {
	sent      []sentMessage
	reactions []sentReaction
}

type sentMessage struct {
	chatID string
	text   string
}

type sentReaction struct {
	chatID    string
	messageID int64
	emoji     string
}

type trackedUserMessage struct {
	taskID    int64
	messageID int64
}

type trackedBinding struct {
	taskID int64
	userID string
}

type fakeBindingTracker struct {
	userMessages []trackedUserMessage
	bindings     []trackedBinding
}

func (f *fakeBindingTracker) TrackUserMessage(taskID, userMsgID int64) {
	f.userMessages = append(f.userMessages, trackedUserMessage{taskID: taskID, messageID: userMsgID})
}

func (f *fakeBindingTracker) TrackChatBinding(taskID int64, userID string) {
	f.bindings = append(f.bindings, trackedBinding{taskID: taskID, userID: userID})
}

func (f *fakeBotClient) SendMessage(_ context.Context, chatID, text string) error {
	f.sent = append(f.sent, sentMessage{chatID: chatID, text: text})
	return nil
}

func (f *fakeBotClient) SendMessageReturningID(_ context.Context, chatID, text string) (int64, error) {
	f.sent = append(f.sent, sentMessage{chatID: chatID, text: text})
	return int64(len(f.sent)), nil
}

func (f *fakeBotClient) EditMessage(_ context.Context, _ string, _ int64, _ string) error {
	return nil
}

func (f *fakeBotClient) SetReaction(_ context.Context, chatID string, messageID int64, emoji string) error {
	f.reactions = append(f.reactions, sentReaction{chatID: chatID, messageID: messageID, emoji: emoji})
	return nil
}

func (f *fakeBotClient) GetUpdates(context.Context, int64, int) ([]Update, error) {
	return nil, nil
}

func openTelegramTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "elnath.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestShell(t *testing.T) (*Shell, *daemon.Queue, *daemon.ApprovalStore, *fakeBotClient) {
	t.Helper()
	return newTestShellWithOptions(t, nil)
}

func newTestShellWithOptions(t *testing.T, skillReg *skill.Registry, opts ...ShellOption) (*Shell, *daemon.Queue, *daemon.ApprovalStore, *fakeBotClient) {
	t.Helper()
	db := openTelegramTestDB(t)
	queue, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	approvals, err := daemon.NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	bot := &fakeBotClient{}
	shell, err := NewShell(queue, approvals, bot, "chat-1", filepath.Join(t.TempDir(), "telegram-state.json"), skillReg, opts...)
	if err != nil {
		t.Fatalf("NewShell: %v", err)
	}
	return shell, queue, approvals, bot
}

func newShellBinder(t *testing.T) (*ChatSessionBinder, *stubSessionValidator) {
	t.Helper()
	validator := &stubSessionValidator{}
	binder, err := NewChatSessionBinder(filepath.Join(t.TempDir(), "telegram-chat-bindings.json"), validator)
	if err != nil {
		t.Fatalf("NewChatSessionBinder: %v", err)
	}
	return binder, validator
}

func TestShellHandleUpdateStatusAndFollowUp(t *testing.T) {
	shell, queue, _, bot := newTestShell(t)

	taskID, _, err := queue.Enqueue(context.Background(), "existing task", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil || task.ID != taskID {
		t.Fatalf("Next task = %+v, want task id %d", task, taskID)
	}
	if err := queue.BindSession(context.Background(), task.ID, "sess-status"); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	if err := queue.UpdateProgress(context.Background(), task.ID, daemon.EncodeProgressEvent(daemon.TextProgressEvent("still working"))); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	if err := shell.HandleUpdate(context.Background(), Update{
		ID: 1,
		Message: Message{
			ChatID: "chat-1",
			Text:   "/status",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate status: %v", err)
	}

	if len(bot.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].text, "still working") || !strings.Contains(bot.sent[0].text, "Status") {
		t.Fatalf("status reply = %q, want progress and status header", bot.sent[0].text)
	}

	if err := shell.HandleUpdate(context.Background(), Update{
		ID: 2,
		Message: Message{
			ChatID: "chat-1",
			UserID: "77",
			Text:   "/followup sess-status continue with the same session",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate followup: %v", err)
	}

	tasks, err := queue.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) < 2 {
		t.Fatalf("tasks = %d, want at least 2", len(tasks))
	}
	got := daemon.ParseTaskPayload(tasks[0].Payload)
	if got.SessionID != "sess-status" {
		t.Fatalf("follow-up SessionID = %q, want sess-status", got.SessionID)
	}
	if got.Principal.UserID != "77" || got.Principal.Surface != "telegram" {
		t.Fatalf("follow-up principal = %+v, want telegram principal for user 77", got.Principal)
	}
	if !strings.Contains(got.Prompt, "continue with the same session") {
		t.Fatalf("follow-up Prompt = %q", got.Prompt)
	}
}

func TestShellHandleUpdateApprovalsAndNotifyCompletions(t *testing.T) {
	shell, queue, approvals, bot := newTestShell(t)

	req, err := approvals.Create(context.Background(), "bash", []byte(`{"cmd":"git status"}`))
	if err != nil {
		t.Fatalf("Create approval: %v", err)
	}

	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", Text: "/approvals"},
	}); err != nil {
		t.Fatalf("HandleUpdate approvals: %v", err)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].text, "git status") {
		t.Fatalf("approvals reply = %#v, want tool details", bot.sent)
	}

	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      2,
		Message: Message{ChatID: "chat-1", Text: "/approve 1"},
	}); err != nil {
		t.Fatalf("HandleUpdate approve: %v", err)
	}
	row, err := approvals.Get(context.Background(), req.ID)
	if err != nil {
		t.Fatalf("Get approval: %v", err)
	}
	if row.Decision != daemon.ApprovalDecisionApproved {
		t.Fatalf("approval decision = %q, want approved", row.Decision)
	}

	taskID, _, err := queue.Enqueue(context.Background(), "terminal task", "")
	if err != nil {
		t.Fatalf("Enqueue completion: %v", err)
	}
	task, err := queue.Next(context.Background())
	if err != nil {
		t.Fatalf("Next completion: %v", err)
	}
	if task == nil || task.ID != taskID {
		t.Fatalf("Next completion task = %+v, want %d", task, taskID)
	}
	if err := queue.BindSession(context.Background(), task.ID, "sess-complete"); err != nil {
		t.Fatalf("BindSession completion: %v", err)
	}
	if err := queue.MarkDone(context.Background(), task.ID, "all good", "finished cleanly"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	if err := shell.NotifyCompletions(context.Background()); err != nil {
		t.Fatalf("NotifyCompletions first run: %v", err)
	}
	if len(bot.sent) != 3 {
		t.Fatalf("sent messages after completion = %d, want 3", len(bot.sent))
	}
	if !strings.Contains(bot.sent[2].text, "finished cleanly") || !strings.Contains(bot.sent[2].text, "sess-complete") {
		t.Fatalf("completion notification = %q", bot.sent[2].text)
	}

	if err := shell.NotifyCompletions(context.Background()); err != nil {
		t.Fatalf("NotifyCompletions second run: %v", err)
	}
	if len(bot.sent) != 3 {
		t.Fatalf("completion notifications duplicated: %#v", bot.sent)
	}
}

func TestShellSubmitCommand(t *testing.T) {
	shell, queue, _, bot := newTestShell(t)

	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", UserID: "88", Text: "/submit Write a haiku about Go"},
	}); err != nil {
		t.Fatalf("HandleUpdate submit: %v", err)
	}

	if len(bot.sent) == 0 {
		t.Fatal("expected ack message")
	}

	tasks, _ := queue.List(context.Background())
	if len(tasks) == 0 {
		t.Fatal("expected at least 1 task after /submit")
	}
	payload := daemon.ParseTaskPayload(tasks[0].Payload)
	if payload.Prompt != "Write a haiku about Go" {
		t.Fatalf("prompt = %q, want 'Write a haiku about Go'", payload.Prompt)
	}
	if payload.Surface != "telegram" {
		t.Fatalf("surface = %q, want telegram", payload.Surface)
	}
	if payload.Principal.UserID != "88" || payload.Principal.Surface != "telegram" {
		t.Fatalf("principal = %+v, want telegram principal for user 88", payload.Principal)
	}
}

func TestShellPlainTextAutoSubmit(t *testing.T) {
	shell, queue, _, bot := newTestShell(t)

	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", UserID: "99", Text: "Refactor the auth module"},
	}); err != nil {
		t.Fatalf("HandleUpdate plain text: %v", err)
	}

	if len(bot.sent) == 0 {
		t.Fatal("expected ack message")
	}

	tasks, _ := queue.List(context.Background())
	if len(tasks) == 0 {
		t.Fatal("expected at least 1 task after plain text")
	}
	payload := daemon.ParseTaskPayload(tasks[0].Payload)
	if payload.Prompt != "Refactor the auth module" {
		t.Fatalf("prompt = %q, want 'Refactor the auth module'", payload.Prompt)
	}
	if payload.Principal.UserID != "99" || payload.Principal.Surface != "telegram" {
		t.Fatalf("principal = %+v, want telegram principal for user 99", payload.Principal)
	}
}

func TestShellDeduplicatesSameUserPrompt(t *testing.T) {
	shell, queue, _, bot := newTestShell(t)

	first := Update{ID: 1, Message: Message{ChatID: "chat-1", UserID: "99", MessageID: 1, Text: "Refactor the auth module"}}
	second := Update{ID: 2, Message: Message{ChatID: "chat-1", UserID: "99", MessageID: 2, Text: "Refactor the auth module"}}

	if err := shell.HandleUpdate(context.Background(), first); err != nil {
		t.Fatalf("HandleUpdate(first): %v", err)
	}
	if err := shell.HandleUpdate(context.Background(), second); err != nil {
		t.Fatalf("HandleUpdate(second): %v", err)
	}

	tasks, err := queue.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("queue tasks = %d, want 1", len(tasks))
	}
	if tasks[0].IdempotencyKey == "" {
		t.Fatal("queue task should persist an idempotency key")
	}
	if len(bot.sent) != 2 {
		t.Fatalf("sent messages = %d, want 2", len(bot.sent))
	}
	if !strings.Contains(bot.sent[1].text, "이미 처리 중입니다 (#") {
		t.Fatalf("second reply = %q, want dedup message", bot.sent[1].text)
	}
}

func TestShellEnqueueWithBindingInjectsSessionID(t *testing.T) {
	binder, validator := newShellBinder(t)
	validator.set("sess-bound", true)
	if err := binder.Remember("chat-1", "77", "sess-bound"); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	shell, queue, _, _ := newTestShellWithOptions(t, nil, WithChatSessionBinder(binder))
	principal := shell.principalForMessage(Message{UserID: "77"})

	taskID, existed, err := shell.enqueueTaskReturningID(context.Background(), "continue this", principal)
	if err != nil {
		t.Fatalf("enqueueTaskReturningID: %v", err)
	}
	if existed {
		t.Fatal("first enqueue should not deduplicate")
	}

	task, err := queue.Get(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	payload := daemon.ParseTaskPayload(task.Payload)
	if payload.SessionID != "sess-bound" {
		t.Fatalf("payload.SessionID = %q, want sess-bound", payload.SessionID)
	}
}

func TestShellEnqueueNoBinderLeavesSessionEmpty(t *testing.T) {
	shell, queue, _, _ := newTestShell(t)
	principal := shell.principalForMessage(Message{UserID: "77"})

	taskID, existed, err := shell.enqueueTaskReturningID(context.Background(), "continue this", principal)
	if err != nil {
		t.Fatalf("enqueueTaskReturningID: %v", err)
	}
	if existed {
		t.Fatal("first enqueue should not deduplicate")
	}

	task, err := queue.Get(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	payload := daemon.ParseTaskPayload(task.Payload)
	if payload.SessionID != "" {
		t.Fatalf("payload.SessionID = %q, want empty", payload.SessionID)
	}
}

func TestShellEnqueueBindingMissLeavesSessionEmpty(t *testing.T) {
	binder, _ := newShellBinder(t)
	shell, queue, _, _ := newTestShellWithOptions(t, nil, WithChatSessionBinder(binder))
	principal := shell.principalForMessage(Message{UserID: "77"})

	taskID, existed, err := shell.enqueueTaskReturningID(context.Background(), "continue this", principal)
	if err != nil {
		t.Fatalf("enqueueTaskReturningID: %v", err)
	}
	if existed {
		t.Fatal("first enqueue should not deduplicate")
	}

	task, err := queue.Get(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	payload := daemon.ParseTaskPayload(task.Payload)
	if payload.SessionID != "" {
		t.Fatalf("payload.SessionID = %q, want empty", payload.SessionID)
	}
}

func TestShellEnqueueIdempotencyKeyIgnoresBoundSession(t *testing.T) {
	binder, validator := newShellBinder(t)
	validator.set("sess-1", true)
	validator.set("sess-2", true)
	if err := binder.Remember("chat-1", "77", "sess-1"); err != nil {
		t.Fatalf("Remember(first): %v", err)
	}
	shell, queue, _, _ := newTestShellWithOptions(t, nil, WithChatSessionBinder(binder))
	principal := shell.principalForMessage(Message{UserID: "77"})

	firstTaskID, existed, err := shell.enqueueTaskReturningID(context.Background(), "same prompt", principal)
	if err != nil {
		t.Fatalf("first enqueueTaskReturningID: %v", err)
	}
	if existed {
		t.Fatal("first enqueue should not deduplicate")
	}
	if err := binder.Remember("chat-1", "77", "sess-2"); err != nil {
		t.Fatalf("Remember(second): %v", err)
	}

	secondTaskID, existed, err := shell.enqueueTaskReturningID(context.Background(), "same prompt", principal)
	if err != nil {
		t.Fatalf("second enqueueTaskReturningID: %v", err)
	}
	if !existed {
		t.Fatal("second enqueue should deduplicate even with different bound session")
	}
	if secondTaskID != firstTaskID {
		t.Fatalf("dedup task ID = %d, want %d", secondTaskID, firstTaskID)
	}

	tasks, err := queue.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("queue tasks = %d, want 1", len(tasks))
	}
	if tasks[0].IdempotencyKey == "" {
		t.Fatal("queue task should persist an idempotency key")
	}
}

func TestShellTrackChatBindingCalledAfterEnqueue(t *testing.T) {
	tracker := &fakeBindingTracker{}
	shell, _, _, _ := newTestShellWithOptions(t, nil, WithTaskTracker(tracker))

	err := shell.HandleUpdate(context.Background(), Update{
		ID: 1,
		Message: Message{
			ChatID:    "chat-1",
			UserID:    "88",
			MessageID: 101,
			Text:      "Queue this task",
		},
	})
	if err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}
	if len(tracker.bindings) != 1 {
		t.Fatalf("TrackChatBinding calls = %d, want 1", len(tracker.bindings))
	}
	if tracker.bindings[0].userID != "88" {
		t.Fatalf("tracked userID = %q, want 88", tracker.bindings[0].userID)
	}
	if len(tracker.userMessages) != 1 {
		t.Fatalf("TrackUserMessage calls = %d, want 1", len(tracker.userMessages))
	}
}

func TestShellNotifyCompletionsUpdatesBinder(t *testing.T) {
	binder, validator := newShellBinder(t)
	validator.set("sess-complete", true)
	shell, queue, _, bot := newTestShellWithOptions(t, nil, WithChatSessionBinder(binder))
	principal := shell.principalForMessage(Message{UserID: "77"})
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:    "follow this up",
		Surface:   "telegram",
		Principal: principal,
	})

	taskID, _, err := queue.Enqueue(context.Background(), payload, "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil || task.ID != taskID {
		t.Fatalf("Next task = %+v, want %d", task, taskID)
	}
	if err := queue.BindSession(context.Background(), taskID, "sess-complete"); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	if err := queue.MarkDone(context.Background(), taskID, "ok", "done"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	if err := shell.NotifyCompletions(context.Background()); err != nil {
		t.Fatalf("NotifyCompletions: %v", err)
	}
	if _, ok := binder.Lookup("chat-1", "77"); !ok {
		t.Fatal("expected binder to remember completed telegram session")
	}
	if len(bot.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1 completion notification", len(bot.sent))
	}
}

func TestShellNotifyCompletionsSkipsNonTelegramSurface(t *testing.T) {
	binder, _ := newShellBinder(t)
	shell, queue, _, _ := newTestShellWithOptions(t, nil, WithChatSessionBinder(binder))
	principal := shell.principalForMessage(Message{UserID: "77"})
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:    "follow this up",
		Surface:   "cli",
		Principal: principal,
	})

	taskID, _, err := queue.Enqueue(context.Background(), payload, "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil || task.ID != taskID {
		t.Fatalf("Next task = %+v, want %d", task, taskID)
	}
	if err := queue.BindSession(context.Background(), taskID, "sess-complete"); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	if err := queue.MarkDone(context.Background(), taskID, "ok", "done"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	if err := shell.NotifyCompletions(context.Background()); err != nil {
		t.Fatalf("NotifyCompletions: %v", err)
	}
	if got, ok := binder.Lookup("chat-1", "77"); ok || got != "" {
		t.Fatalf("binder.Lookup() = (%q, %v), want miss for non-telegram surface", got, ok)
	}
}

func TestShellNotifyCompletionsSkipsEmptyTelegramUserID(t *testing.T) {
	binder, _ := newShellBinder(t)
	shell, queue, _, _ := newTestShellWithOptions(t, nil, WithChatSessionBinder(binder))
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:  "follow this up",
		Surface: "telegram",
	})

	taskID, _, err := queue.Enqueue(context.Background(), payload, "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil || task.ID != taskID {
		t.Fatalf("Next task = %+v, want %d", task, taskID)
	}
	if err := queue.BindSession(context.Background(), taskID, "sess-complete"); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	if err := queue.MarkDone(context.Background(), taskID, "ok", "done"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	if err := shell.NotifyCompletions(context.Background()); err != nil {
		t.Fatalf("NotifyCompletions: %v", err)
	}
	if len(binder.bindings) != 0 {
		t.Fatalf("binder bindings = %+v, want empty when userID is missing", binder.bindings)
	}
}

func TestShellUnknownCommandStillErrors(t *testing.T) {
	shell, _, _, bot := newTestShell(t)

	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", Text: "/nonexistent"},
	}); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].text, "Unknown command") {
		t.Fatalf("reply = %#v, want unknown command error", bot.sent)
	}
}

func TestShellHelpListsSkills(t *testing.T) {
	reg := skill.NewRegistry()
	reg.Add(&skill.Skill{Name: "pr-review", Description: "Review PR with security focus"})
	reg.Add(&skill.Skill{Name: "audit-security", Description: "Audit codebase"})
	shell, _, _, bot := newTestShellWithOptions(t, reg)

	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", Text: "/help"},
	}); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	if len(bot.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(bot.sent))
	}
	checks := []string{
		"/skill-list",
		"/skill-create",
		"🛠 <b>Skills</b>",
		"/pr-review",
		"/audit-security",
	}
	for _, want := range checks {
		if !strings.Contains(bot.sent[0].text, want) {
			t.Fatalf("help reply missing %q\n%s", want, bot.sent[0].text)
		}
	}
}

func TestShellSkillListCommand(t *testing.T) {
	reg := skill.NewRegistry()
	reg.Add(&skill.Skill{Name: "pr-review", Description: "Review PRs", Status: "active"})
	shell, queue, _, bot := newTestShellWithOptions(t, reg)

	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", Text: "/skill-list"},
	}); err != nil {
		t.Fatalf("HandleUpdate(/skill-list) error = %v", err)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].text, "/pr-review") {
		t.Fatalf("reply = %#v, want listed skills", bot.sent)
	}
	tasks, err := queue.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("queued tasks = %d, want 0", len(tasks))
	}
}

func TestShellSkillCreateCommand(t *testing.T) {
	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	reg := skill.NewRegistry()
	creator := skill.NewCreator(store, skill.NewTracker(t.TempDir()), reg)
	shell, _, _, bot := newTestShellWithOptions(t, reg, WithSkillCreator(creator))

	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", Text: "/skill-create deploy-check"},
	}); err != nil {
		t.Fatalf("HandleUpdate(/skill-create) error = %v", err)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].text, "deploy-check") {
		t.Fatalf("reply = %#v, want skill creation confirmation", bot.sent)
	}
	page, err := store.Read("skills/deploy-check.md")
	if err != nil {
		t.Fatalf("Read(created skill) error = %v", err)
	}
	if got := page.Extra["status"]; got != "draft" {
		t.Fatalf("created status = %v, want draft", got)
	}
}

// TestShellSkillListShowsDraftsFromStore guards FU-SkillReload: after
// /skill-create produces a draft (the default status for telegram-created
// skills), /skill-list must surface the draft so the author can find it.
// The in-memory registry intentionally omits drafts, so /skill-list must
// read from the wiki store when available.
func TestShellSkillListShowsDraftsFromStore(t *testing.T) {
	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reg := skill.NewRegistry()
	creator := skill.NewCreator(store, skill.NewTracker(t.TempDir()), reg)
	shell, _, _, bot := newTestShellWithOptions(t, reg,
		WithSkillCreator(creator),
		WithWikiStore(store),
	)

	// Create a draft via /skill-create (default status=draft for telegram).
	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", Text: "/skill-create dogfood-test"},
	}); err != nil {
		t.Fatalf("HandleUpdate(create): %v", err)
	}
	// /skill-list should surface it with a (draft) marker.
	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      2,
		Message: Message{ChatID: "chat-1", Text: "/skill-list"},
	}); err != nil {
		t.Fatalf("HandleUpdate(list): %v", err)
	}
	last := lastSentText(bot)
	if !strings.Contains(last, "/dogfood-test") {
		t.Errorf("skill-list reply missing the draft skill name:\n%s", last)
	}
	if !strings.Contains(last, "draft") {
		t.Errorf("skill-list reply missing the draft status marker:\n%s", last)
	}
}

func TestShellSkillCommandQueuesTask(t *testing.T) {
	reg := skill.NewRegistry()
	reg.Add(&skill.Skill{Name: "pr-review", Description: "Review PR", Trigger: "/pr-review <pr_number>"})
	tracker := &fakeBindingTracker{}
	shell, queue, _, bot := newTestShellWithOptions(t, reg, WithTaskTracker(tracker))

	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", UserID: "88", MessageID: 12, Text: "/pr-review 42"},
	}); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	tasks, err := queue.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	payload := daemon.ParseTaskPayload(tasks[0].Payload)
	if payload.Prompt != "[Skill: pr-review] /pr-review 42" {
		t.Fatalf("payload prompt = %q, want prefixed skill prompt", payload.Prompt)
	}
	if payload.Principal.UserID != "88" || payload.Principal.Surface != "telegram" {
		t.Fatalf("principal = %+v, want telegram principal for user 88", payload.Principal)
	}
	if len(bot.reactions) != 1 || bot.reactions[0].emoji != "👀" || bot.reactions[0].messageID != 12 {
		t.Fatalf("reactions = %#v, want one 👀 reaction for message 12", bot.reactions)
	}
	if len(tracker.userMessages) != 1 || tracker.userMessages[0].messageID != 12 {
		t.Fatalf("tracked user messages = %#v, want message 12 tracked", tracker.userMessages)
	}
	if len(tracker.bindings) != 1 || tracker.bindings[0].userID != "88" {
		t.Fatalf("tracked bindings = %#v, want user 88 tracked", tracker.bindings)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].text, "queued") {
		t.Fatalf("reply = %#v, want queued confirmation", bot.sent)
	}
}

// --- Intent classification mocks and tests ---

type mockClassifier struct {
	intent conversation.Intent
	err    error

	mu          sync.Mutex
	calls       int
	lastHistory []llm.Message
}

func (m *mockClassifier) Classify(_ context.Context, _ llm.Provider, _ string, history []llm.Message) (conversation.Intent, error) {
	m.mu.Lock()
	m.calls++
	m.lastHistory = append([]llm.Message(nil), history...)
	m.mu.Unlock()
	return m.intent, m.err
}

func (m *mockClassifier) snapshotHistory() []llm.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]llm.Message(nil), m.lastHistory...)
}

func (m *mockClassifier) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

type shellMockProvider struct{}

func (m *shellMockProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (m *shellMockProvider) Stream(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "hi"})
	cb(llm.StreamEvent{Type: llm.EventDone})
	return nil
}

func (m *shellMockProvider) Name() string            { return "mock" }
func (m *shellMockProvider) Models() []llm.ModelInfo { return nil }

func newTestShellWithClassifier(t *testing.T, intent conversation.Intent, classifyErr error, extraOpts ...ShellOption) (*Shell, *daemon.Queue, *fakeBotClient) {
	t.Helper()
	db := openTelegramTestDB(t)
	queue, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	approvals, err := daemon.NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	bot := &fakeBotClient{}
	provider := &shellMockProvider{}
	responder := NewChatResponder(provider, bot, "chat-1", nil,
		WithChatPipeline(ChatPipelineDeps{Builder: &stubChatBuilder{result: "SYS"}}),
	)
	classifier := &mockClassifier{intent: intent, err: classifyErr}

	opts := []ShellOption{
		WithChatResponder(responder),
		WithClassifier(classifier, provider),
	}
	opts = append(opts, extraOpts...)
	shell, err := NewShell(queue, approvals, bot, "chat-1",
		filepath.Join(t.TempDir(), "telegram-state.json"),
		nil,
		opts...,
	)
	if err != nil {
		t.Fatalf("NewShell: %v", err)
	}
	return shell, queue, bot
}

func TestShellChatBypassesQueue(t *testing.T) {
	shell, queue, bot := newTestShellWithClassifier(t, conversation.IntentChat, nil)

	err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", UserID: "51", MessageID: 10, Text: "How are you?"},
	})
	if err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	tasks, err := queue.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks in queue, got %d", len(tasks))
	}

	if len(bot.sent) == 0 {
		t.Fatal("expected at least one message sent via bot")
	}
	// The chat responder streams "hi" — verify no "queued" reply.
	for _, msg := range bot.sent {
		if strings.Contains(msg.text, "queued") {
			t.Fatalf("chat message should not produce a queued reply, got %q", msg.text)
		}
	}
}

func TestShellTaskGoesToQueue(t *testing.T) {
	shell, queue, bot := newTestShellWithClassifier(t, conversation.IntentComplexTask, nil)

	err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", UserID: "52", MessageID: 11, Text: "Refactor the auth module"},
	})
	if err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	tasks, err := queue.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task in queue, got %d", len(tasks))
	}
	payload := daemon.ParseTaskPayload(tasks[0].Payload)
	if payload.Principal.UserID != "52" || payload.Principal.Surface != "telegram" {
		t.Fatalf("principal = %+v, want telegram principal for user 52", payload.Principal)
	}

	if len(bot.sent) == 0 {
		t.Fatal("expected ack message for task")
	}
}

func TestShellClassifyErrorFallsBackToQueue(t *testing.T) {
	shell, queue, bot := newTestShellWithClassifier(t, conversation.IntentChat, fmt.Errorf("classifier down"))

	err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", UserID: "53", MessageID: 12, Text: "Hello there"},
	})
	if err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	tasks, err := queue.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task in queue (fallback), got %d", len(tasks))
	}
	payload := daemon.ParseTaskPayload(tasks[0].Payload)
	if payload.Principal.UserID != "53" || payload.Principal.Surface != "telegram" {
		t.Fatalf("principal = %+v, want telegram principal for user 53", payload.Principal)
	}

	if len(bot.sent) == 0 {
		t.Fatal("expected ack message on classify error fallback")
	}
}

// TestShellChatIntentWithPinnedPreferenceGoesToQueue guards FU-CR: when a user
// has pinned a workflow for a chat-classified intent (via /override or derived
// by the routing advisor), the shell must defer to the queue+router instead of
// answering directly through ChatResponder. Without this, telegram users get
// zero benefit from learned routing preferences on chat-like intents.
func TestShellChatIntentWithPinnedPreferenceGoesToQueue(t *testing.T) {
	wikiStore := newTestWikiStoreForShell(t)
	workDir := t.TempDir()
	projectID := identity.ResolveProjectID(workDir, "")
	if projectID == "" {
		t.Fatal("failed to derive projectID from workDir")
	}
	pref := &routing.WorkflowPreference{
		PreferredWorkflows: map[string]string{string(conversation.IntentQuestion): "research"},
	}
	if err := wiki.SaveUserWorkflowPreference(wikiStore, projectID, pref); err != nil {
		t.Fatalf("SaveUserWorkflowPreference: %v", err)
	}

	shell, queue, bot := newTestShellWithClassifier(t, conversation.IntentQuestion, nil,
		WithWikiStore(wikiStore),
		WithWorkDir(workDir),
	)

	err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", UserID: "60", MessageID: 20, Text: "what's the status?"},
	})
	if err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	tasks, err := queue.List(context.Background())
	if err != nil {
		t.Fatalf("queue.List: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1 (pinned pref must divert chat intent to queue)", len(tasks))
	}
	payload := daemon.ParseTaskPayload(tasks[0].Payload)
	if payload.Principal.UserID != "60" {
		t.Errorf("payload userID = %q, want 60", payload.Principal.UserID)
	}
	if payload.Principal.ProjectID != projectID {
		t.Errorf("payload projectID = %q, want %q", payload.Principal.ProjectID, projectID)
	}
	for _, msg := range bot.sent {
		if strings.Contains(msg.text, "hi") {
			t.Errorf("chat stream output leaked; pinned preference should have bypassed chat: %q", msg.text)
		}
	}
}

// TestShellChatIntentWithoutPinShortCircuitsToChat is the regression guard for
// the default chat path: when no routing preference is pinned for the intent,
// the shell must still respond directly via ChatResponder.
func TestShellChatIntentWithoutPinShortCircuitsToChat(t *testing.T) {
	wikiStore := newTestWikiStoreForShell(t)
	shell, queue, bot := newTestShellWithClassifier(t, conversation.IntentQuestion, nil,
		WithWikiStore(wikiStore),
	)

	err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", UserID: "61", MessageID: 21, Text: "what's the status?"},
	})
	if err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	tasks, err := queue.List(context.Background())
	if err != nil {
		t.Fatalf("queue.List: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %d, want 0 (no pin; chat path must run)", len(tasks))
	}
	if len(bot.sent) == 0 {
		t.Fatal("expected chat stream output via bot.sent")
	}
}

// TestShellChatIntentPinForOtherIntentStillChats guards the intent-scope of
// pinning: a preference for a different intent must not divert a chat-classified
// intent away from ChatResponder.
func TestShellChatIntentPinForOtherIntentStillChats(t *testing.T) {
	wikiStore := newTestWikiStoreForShell(t)
	workDir := t.TempDir()
	projectID := identity.ResolveProjectID(workDir, "")
	if projectID == "" {
		t.Fatal("failed to derive projectID from workDir")
	}
	pref := &routing.WorkflowPreference{
		PreferredWorkflows: map[string]string{string(conversation.IntentComplexTask): "team"},
	}
	if err := wiki.SaveUserWorkflowPreference(wikiStore, projectID, pref); err != nil {
		t.Fatalf("SaveUserWorkflowPreference: %v", err)
	}

	shell, queue, bot := newTestShellWithClassifier(t, conversation.IntentQuestion, nil,
		WithWikiStore(wikiStore),
		WithWorkDir(workDir),
	)

	err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", UserID: "62", MessageID: 22, Text: "what's the status?"},
	})
	if err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	tasks, _ := queue.List(context.Background())
	if len(tasks) != 0 {
		t.Fatalf("tasks = %d, want 0 (pin is for unrelated intent; chat path must run)", len(tasks))
	}
	if len(bot.sent) == 0 {
		t.Fatal("expected chat stream output via bot.sent")
	}
}

func newTestLearningStore(t *testing.T) *learning.Store {
	t.Helper()
	return learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
}

func newTestWikiStoreForShell(t *testing.T) *wiki.Store {
	t.Helper()
	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("wiki.NewStore: %v", err)
	}
	return store
}

func lastSentText(bot *fakeBotClient) string {
	if len(bot.sent) == 0 {
		return ""
	}
	return bot.sent[len(bot.sent)-1].text
}

func TestShellRememberCommand(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		withStore  bool
		wantSub    string
		wantStored int
	}{
		{
			name:       "happy path stores lesson",
			text:       "/remember prefer ralph for flaky tests",
			withStore:  true,
			wantSub:    "Remembered",
			wantStored: 1,
		},
		{
			name:       "empty argument returns usage",
			text:       "/remember",
			withStore:  true,
			wantSub:    "Usage:",
			wantStored: 0,
		},
		{
			name:      "nil store reports unavailable",
			text:      "/remember anything",
			withStore: false,
			wantSub:   "Learning store unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var store *learning.Store
			var opts []ShellOption
			if tc.withStore {
				store = newTestLearningStore(t)
				opts = append(opts, WithLearningStore(store))
			}
			shell, _, _, bot := newTestShellWithOptions(t, nil, opts...)

			if err := shell.HandleUpdate(context.Background(), Update{
				Message: Message{ChatID: "chat-1", UserID: "42", Text: tc.text},
			}); err != nil {
				t.Fatalf("HandleUpdate: %v", err)
			}

			got := lastSentText(bot)
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("response = %q, want substring %q", got, tc.wantSub)
			}
			if tc.withStore {
				lessons, err := store.List()
				if err != nil {
					t.Fatalf("store.List: %v", err)
				}
				if len(lessons) != tc.wantStored {
					t.Errorf("stored lessons = %d, want %d", len(lessons), tc.wantStored)
				}
			}
		})
	}
}

func TestShellForgetCommand(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		withStore   bool
		seed        []string
		wantSub     string
		wantRemains int
	}{
		{
			name:        "deletes by id prefix",
			text:        "",
			withStore:   true,
			seed:        []string{"one", "two"},
			wantSub:     "Forgot 1 lesson",
			wantRemains: 1,
		},
		{
			name:      "empty argument returns usage",
			text:      "/forget",
			withStore: true,
			wantSub:   "Usage:",
		},
		{
			name:      "nil store reports unavailable",
			text:      "/forget abcd1234",
			withStore: false,
			wantSub:   "Learning store unavailable",
		},
		{
			name:        "unknown prefix reports zero deletion",
			text:        "/forget zzzzzzzz",
			withStore:   true,
			seed:        []string{"anchor"},
			wantSub:     "Forgot 0 lesson",
			wantRemains: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var store *learning.Store
			var opts []ShellOption
			if tc.withStore {
				store = newTestLearningStore(t)
				opts = append(opts, WithLearningStore(store))
			}
			shell, _, _, bot := newTestShellWithOptions(t, nil, opts...)

			var targetID string
			for _, seedText := range tc.seed {
				lesson := learning.Lesson{Text: seedText, Source: "test"}
				if err := store.Append(lesson); err != nil {
					t.Fatalf("seed append: %v", err)
				}
				if targetID == "" {
					lessons, err := store.List()
					if err != nil {
						t.Fatalf("seed list: %v", err)
					}
					if len(lessons) > 0 {
						targetID = lessons[0].ID
					}
				}
			}

			text := tc.text
			if text == "" {
				if targetID == "" {
					t.Fatal("test setup error: empty text requires seeded lesson to derive id")
				}
				text = "/forget " + targetID
			}

			if err := shell.HandleUpdate(context.Background(), Update{
				Message: Message{ChatID: "chat-1", UserID: "42", Text: text},
			}); err != nil {
				t.Fatalf("HandleUpdate: %v", err)
			}

			got := lastSentText(bot)
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("response = %q, want substring %q", got, tc.wantSub)
			}
			if tc.withStore {
				remaining, err := store.List()
				if err != nil {
					t.Fatalf("store.List: %v", err)
				}
				if len(remaining) != tc.wantRemains {
					t.Errorf("remaining lessons = %d, want %d", len(remaining), tc.wantRemains)
				}
			}
		})
	}
}

func TestShellOverrideCommand(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		withWiki  bool
		wantSub   string
		wantAvoid bool
	}{
		{
			name:     "sets preferred workflow",
			text:     "/override complex_task ralph",
			withWiki: true,
			wantSub:  "Override set: complex_task -> ralph",
		},
		{
			name:     "rejects unknown workflow",
			text:     "/override complex_task cosmic",
			withWiki: true,
			wantSub:  "Unknown workflow",
		},
		{
			name:     "too few arguments returns usage",
			text:     "/override",
			withWiki: true,
			wantSub:  "Usage:",
		},
		{
			name:     "missing workflow argument returns usage",
			text:     "/override complex_task",
			withWiki: true,
			wantSub:  "Usage:",
		},
		{
			name:     "nil wiki store reports unavailable",
			text:     "/override complex_task ralph",
			withWiki: false,
			wantSub:  "Wiki store unavailable",
		},
		{
			name:      "clear removes pinned preference",
			text:      "/override clear",
			withWiki:  true,
			wantSub:   "Override cleared",
			wantAvoid: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var store *wiki.Store
			var opts []ShellOption
			if tc.withWiki {
				store = newTestWikiStoreForShell(t)
				opts = append(opts, WithWikiStore(store))
			}
			shell, _, _, bot := newTestShellWithOptions(t, nil, opts...)

			if err := shell.HandleUpdate(context.Background(), Update{
				Message: Message{ChatID: "chat-1", UserID: "42", Text: tc.text},
			}); err != nil {
				t.Fatalf("HandleUpdate: %v", err)
			}

			got := lastSentText(bot)
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("response = %q, want substring %q", got, tc.wantSub)
			}

			if tc.name == "sets preferred workflow" {
				// The project ID used by the shell is derived from shell.workDir.
				// Just confirm a routing-preferences page exists somewhere under projects/.
				pages, err := store.List()
				if err != nil {
					t.Fatalf("store.List: %v", err)
				}
				var found bool
				for _, p := range pages {
					if strings.HasSuffix(p.Path, "routing-preferences.md") {
						found = true
						if p.PageSource() != wiki.SourceUser {
							t.Errorf("routing-preferences source = %q, want %q", p.PageSource(), wiki.SourceUser)
						}
						break
					}
				}
				if !found {
					t.Errorf("expected routing-preferences page, got %d pages", len(pages))
				}
			}
		})
	}
}

// TestShellEnqueueStampsRealProjectID guards the A.1 outcome-recording fix:
// Telegram-ingested tasks must carry a real ProjectID on their Principal so
// the runtime routes outcomes under the caller's project, not the daemon
// fallback. Specifically, ProjectID must not be empty and must not be the
// legacy "unknown" sentinel.
func TestShellEnqueueStampsRealProjectID(t *testing.T) {
	shell, queue, _, _ := newTestShell(t)
	// Set workDir explicitly so the derived ProjectID is deterministic for
	// the test environment (doesn't depend on TestMain cwd).
	workDir := t.TempDir()
	shell.workDir = workDir

	if err := shell.HandleUpdate(context.Background(), Update{
		Message: Message{ChatID: "chat-1", UserID: "42", Text: "do something useful"},
	}); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	tasks, err := queue.List(context.Background())
	if err != nil {
		t.Fatalf("queue.List: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	payload := daemon.ParseTaskPayload(tasks[0].Payload)
	if payload.Principal.ProjectID == "" {
		t.Fatal("Principal.ProjectID is empty; outcome recording would be skipped")
	}
	if payload.Principal.ProjectID == "unknown" {
		t.Fatalf(`Principal.ProjectID = "unknown"; A.1 sentinel guard would drop this to the daemon fallback`)
	}
}

func TestShellUndoCommand(t *testing.T) {
	t.Run("cancels most recent pending task", func(t *testing.T) {
		shell, queue, _, bot := newTestShell(t)

		taskID, _, err := queue.Enqueue(context.Background(), "a pending task", "")
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}

		if err := shell.HandleUpdate(context.Background(), Update{
			Message: Message{ChatID: "chat-1", UserID: "42", Text: "/undo"},
		}); err != nil {
			t.Fatalf("HandleUpdate: %v", err)
		}
		got := lastSentText(bot)
		wantSub := fmt.Sprintf("Task #%d cancelled", taskID)
		if !strings.Contains(got, wantSub) {
			t.Errorf("response = %q, want substring %q", got, wantSub)
		}

		tasks, err := queue.List(context.Background())
		if err != nil {
			t.Fatalf("queue.List: %v", err)
		}
		for _, task := range tasks {
			if task.ID == taskID && task.Status != daemon.StatusFailed {
				t.Errorf("task %d status = %q, want %q after undo", taskID, task.Status, daemon.StatusFailed)
			}
		}
	})

	t.Run("reports empty when no pending tasks", func(t *testing.T) {
		shell, _, _, bot := newTestShell(t)

		if err := shell.HandleUpdate(context.Background(), Update{
			Message: Message{ChatID: "chat-1", UserID: "42", Text: "/undo"},
		}); err != nil {
			t.Fatalf("HandleUpdate: %v", err)
		}
		got := lastSentText(bot)
		if !strings.Contains(got, "No pending task") {
			t.Errorf("response = %q, want substring %q", got, "No pending task")
		}
	})
}

// --- Classifier history wiring (FU-ClassifierHistoryWire) ---

func buildShellForClassifierTest(t *testing.T, classifier *mockClassifier, extraOpts ...ShellOption) (*Shell, *fakeBotClient) {
	t.Helper()
	db := openTelegramTestDB(t)
	queue, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	approvals, err := daemon.NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	bot := &fakeBotClient{}
	provider := &shellMockProvider{}
	responder := NewChatResponder(provider, bot, "chat-1", nil,
		WithChatPipeline(ChatPipelineDeps{Builder: &stubChatBuilder{result: "SYS"}}),
	)

	opts := []ShellOption{
		WithChatResponder(responder),
		WithClassifier(classifier, provider),
	}
	opts = append(opts, extraOpts...)

	shell, err := NewShell(queue, approvals, bot, "chat-1",
		filepath.Join(t.TempDir(), "telegram-state.json"),
		nil, opts...)
	if err != nil {
		t.Fatalf("NewShell: %v", err)
	}
	return shell, bot
}

func TestShellClassifier_PassesHistoryWhenSessionBound(t *testing.T) {
	binder, validator := newShellBinder(t)
	validator.set("sess-bound", true)
	if err := binder.Remember("chat-1", "51", "sess-bound"); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	loader := &stubHistoryLoader{messages: []llm.Message{
		llm.NewUserMessage("prior 1"),
		llm.NewAssistantMessage("reply 1"),
	}}
	classifier := &mockClassifier{intent: conversation.IntentChat}

	shell, _ := buildShellForClassifierTest(t, classifier,
		WithChatSessionBinder(binder),
		WithChatHistoryLoader(loader),
	)

	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", UserID: "51", MessageID: 10, Text: "follow up"},
	}); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	got := classifier.snapshotHistory()
	if len(got) != 2 {
		t.Fatalf("classifier received history len = %d, want 2 (prior turn pair)", len(got))
	}
	if got[0].Text() != "prior 1" {
		t.Errorf("history[0] = %q, want 'prior 1'", got[0].Text())
	}
}

func TestShellClassifier_NoHistoryWhenUnbound(t *testing.T) {
	binder, _ := newShellBinder(t)
	loader := &stubHistoryLoader{messages: []llm.Message{
		llm.NewUserMessage("should-not-appear"),
	}}
	classifier := &mockClassifier{intent: conversation.IntentChat}

	shell, _ := buildShellForClassifierTest(t, classifier,
		WithChatSessionBinder(binder),
		WithChatHistoryLoader(loader),
	)

	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      2,
		Message: Message{ChatID: "chat-1", UserID: "99", MessageID: 11, Text: "hello"},
	}); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	if n := classifier.callCount(); n != 1 {
		t.Fatalf("Classify call count = %d, want 1", n)
	}
	if got := classifier.snapshotHistory(); len(got) != 0 {
		t.Errorf("classifier received history len = %d, want 0 (unbound session)", len(got))
	}
}

func TestShellClassifier_NilHistoryOnLoaderError(t *testing.T) {
	binder, validator := newShellBinder(t)
	validator.set("sess-err", true)
	if err := binder.Remember("chat-1", "51", "sess-err"); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	loader := &stubHistoryLoader{err: fmt.Errorf("disk full")}
	classifier := &mockClassifier{intent: conversation.IntentChat}

	shell, _ := buildShellForClassifierTest(t, classifier,
		WithChatSessionBinder(binder),
		WithChatHistoryLoader(loader),
	)

	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      3,
		Message: Message{ChatID: "chat-1", UserID: "51", MessageID: 12, Text: "x"},
	}); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	if n := classifier.callCount(); n != 1 {
		t.Fatalf("Classify call count = %d, want 1 (should still be called on loader error)", n)
	}
	if got := classifier.snapshotHistory(); len(got) != 0 {
		t.Errorf("classifier received history len = %d, want 0 on loader error", len(got))
	}
}
