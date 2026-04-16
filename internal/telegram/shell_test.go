package telegram

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/llm"
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
}

func (m *mockClassifier) Classify(_ context.Context, _ llm.Provider, _ string, _ []llm.Message) (conversation.Intent, error) {
	return m.intent, m.err
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

func newTestShellWithClassifier(t *testing.T, intent conversation.Intent, classifyErr error) (*Shell, *daemon.Queue, *fakeBotClient) {
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
	responder := NewChatResponder(provider, bot, "chat-1", nil)
	classifier := &mockClassifier{intent: intent, err: classifyErr}

	shell, err := NewShell(queue, approvals, bot, "chat-1",
		filepath.Join(t.TempDir(), "telegram-state.json"),
		nil,
		WithChatResponder(responder),
		WithClassifier(classifier, provider),
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
