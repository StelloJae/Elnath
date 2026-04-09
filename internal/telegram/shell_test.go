package telegram

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/stello/elnath/internal/daemon"
)

type fakeBotClient struct {
	sent []sentMessage
}

type sentMessage struct {
	chatID string
	text   string
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

func (f *fakeBotClient) SetReaction(_ context.Context, _ string, _ int64, _ string) error {
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
	shell, err := NewShell(queue, approvals, bot, "chat-1", filepath.Join(t.TempDir(), "telegram-state.json"))
	if err != nil {
		t.Fatalf("NewShell: %v", err)
	}
	return shell, queue, approvals, bot
}

func TestShellHandleUpdateStatusAndFollowUp(t *testing.T) {
	shell, queue, _, bot := newTestShell(t)

	taskID, err := queue.Enqueue(context.Background(), "existing task")
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
		ID: 1,
		Message: Message{ChatID: "chat-1", Text: "/approvals"},
	}); err != nil {
		t.Fatalf("HandleUpdate approvals: %v", err)
	}
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].text, "git status") {
		t.Fatalf("approvals reply = %#v, want tool details", bot.sent)
	}

	if err := shell.HandleUpdate(context.Background(), Update{
		ID: 2,
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

	taskID, err := queue.Enqueue(context.Background(), "terminal task")
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
		Message: Message{ChatID: "chat-1", Text: "/submit Write a haiku about Go"},
	}); err != nil {
		t.Fatalf("HandleUpdate submit: %v", err)
	}

	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].text, "queued") {
		t.Fatalf("submit reply = %#v, want task confirmation", bot.sent)
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
}

func TestShellPlainTextAutoSubmit(t *testing.T) {
	shell, queue, _, bot := newTestShell(t)

	if err := shell.HandleUpdate(context.Background(), Update{
		ID:      1,
		Message: Message{ChatID: "chat-1", Text: "Refactor the auth module"},
	}); err != nil {
		t.Fatalf("HandleUpdate plain text: %v", err)
	}

	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0].text, "queued") {
		t.Fatalf("plain text reply = %#v, want task confirmation", bot.sent)
	}

	tasks, _ := queue.List(context.Background())
	if len(tasks) == 0 {
		t.Fatal("expected at least 1 task after plain text")
	}
	payload := daemon.ParseTaskPayload(tasks[0].Payload)
	if payload.Prompt != "Refactor the auth module" {
		t.Fatalf("prompt = %q, want 'Refactor the auth module'", payload.Prompt)
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
