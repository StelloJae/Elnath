package telegram

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stello/elnath/internal/daemon"
)

type sinkBotClient struct {
	mu        sync.Mutex
	sent      []sentMessage
	edits     []editedMessage
	reactions []reactionCall
	nextID    int64
}

type editedMessage struct {
	chatID    string
	messageID int64
	text      string
}

type reactionCall struct {
	chatID    string
	messageID int64
	emoji     string
}

func newSinkBot() *sinkBotClient {
	return &sinkBotClient{nextID: 1}
}

func (f *sinkBotClient) SendMessage(_ context.Context, chatID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{chatID: chatID, text: text})
	return nil
}

func (f *sinkBotClient) SendMessageReturningID(_ context.Context, chatID, text string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{chatID: chatID, text: text})
	id := f.nextID
	f.nextID++
	return id, nil
}

func (f *sinkBotClient) EditMessage(_ context.Context, chatID string, messageID int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, editedMessage{chatID: chatID, messageID: messageID, text: text})
	return nil
}

func (f *sinkBotClient) SetReaction(_ context.Context, chatID string, messageID int64, emoji string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactions = append(f.reactions, reactionCall{chatID: chatID, messageID: messageID, emoji: emoji})
	return nil
}

func (f *sinkBotClient) GetUpdates(context.Context, int64, int) ([]Update, error) {
	return nil, nil
}

func (f *sinkBotClient) getSent() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]sentMessage, len(f.sent))
	copy(cp, f.sent)
	return cp
}

func (f *sinkBotClient) getEdits() []editedMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]editedMessage, len(f.edits))
	copy(cp, f.edits)
	return cp
}

func (f *sinkBotClient) getReactions() []reactionCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]reactionCall, len(f.reactions))
	copy(cp, f.reactions)
	return cp
}

func TestSinkOnProgressRoutesToProgressReporter(t *testing.T) {
	bot := newSinkBot()
	sink := NewTelegramSink(bot, "chat-1", nil)

	ev := daemon.EncodeProgressEvent(daemon.TextProgressEvent("[autopilot] stage: plan"))
	sink.OnProgress(1, ev)

	// Give the progress reporter loop time to flush.
	time.Sleep(200 * time.Millisecond)

	// Trigger completion to stop background goroutines.
	sink.NotifyCompletion(context.Background(), daemon.TaskCompletion{
		TaskID:      1,
		Status:      daemon.StatusDone,
		Summary:     "done",
		StartedAt:   time.Now().Add(-time.Second),
		CompletedAt: time.Now(),
	})

	sent := bot.getSent()
	found := false
	for _, m := range sent {
		if m.chatID == "chat-1" && len(m.text) > 0 {
			found = true
			break
		}
	}
	if !found {
		edits := bot.getEdits()
		for _, e := range edits {
			if e.chatID == "chat-1" && len(e.text) > 0 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("expected progress reporter to send or edit a message for tool progress")
	}
}

func TestSinkOnProgressSummaryRoutesToStream(t *testing.T) {
	bot := newSinkBot()
	sink := NewTelegramSink(bot, "chat-1", nil)

	ev := daemon.EncodeProgressEvent(daemon.TextProgressEvent("[summary] Hello world"))
	sink.OnProgress(1, ev)

	// Give stream consumer time to flush.
	time.Sleep(500 * time.Millisecond)

	// Finish the stream consumer so it flushes final text.
	sink.OnStreamDone(1)

	// Clean up.
	sink.NotifyCompletion(context.Background(), daemon.TaskCompletion{
		TaskID:      1,
		Status:      daemon.StatusDone,
		Summary:     "Hello world",
		StartedAt:   time.Now().Add(-time.Second),
		CompletedAt: time.Now(),
	})

	sent := bot.getSent()
	edits := bot.getEdits()

	found := false
	for _, m := range sent {
		if m.chatID == "chat-1" && contains(m.text, "Hello world") {
			found = true
			break
		}
	}
	if !found {
		for _, e := range edits {
			if e.chatID == "chat-1" && contains(e.text, "Hello world") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("expected stream consumer to send 'Hello world', sent=%+v, edits=%+v", sent, edits)
	}
}

func TestSinkNotifyCompletionSetsReaction(t *testing.T) {
	tests := []struct {
		name      string
		status    daemon.TaskStatus
		wantEmoji string
	}{
		{"success", daemon.StatusDone, "👍"},
		{"failure", daemon.StatusFailed, "😢"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bot := newSinkBot()
			sink := NewTelegramSink(bot, "chat-1", nil)

			sink.TrackUserMessage(42, 100)

			err := sink.NotifyCompletion(context.Background(), daemon.TaskCompletion{
				TaskID:      42,
				Status:      tt.status,
				Summary:     "test summary",
				StartedAt:   time.Unix(10, 0),
				CompletedAt: time.Unix(20, 0),
			})
			if err != nil {
				t.Fatalf("NotifyCompletion: %v", err)
			}

			reactions := bot.getReactions()
			if len(reactions) != 1 {
				t.Fatalf("reactions = %d, want 1", len(reactions))
			}
			if reactions[0].emoji != tt.wantEmoji {
				t.Fatalf("reaction emoji = %q, want %q", reactions[0].emoji, tt.wantEmoji)
			}
			if reactions[0].messageID != 100 {
				t.Fatalf("reaction messageID = %d, want 100", reactions[0].messageID)
			}
		})
	}
}

func TestSinkNotifyCompletionSendsSummaryWhenNotStreamed(t *testing.T) {
	bot := newSinkBot()
	sink := NewTelegramSink(bot, "chat-1", nil)

	sink.TrackUserMessage(42, 100)

	err := sink.NotifyCompletion(context.Background(), daemon.TaskCompletion{
		TaskID:      42,
		Status:      daemon.StatusDone,
		Summary:     "Task finished successfully.",
		StartedAt:   time.Unix(10, 0),
		CompletedAt: time.Unix(20, 0),
	})
	if err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}

	sent := bot.getSent()
	found := false
	for _, m := range sent {
		if contains(m.text, "Task finished successfully.") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected summary message, sent=%+v", sent)
	}
}

func TestSinkTrackChatBindingStoresUserID(t *testing.T) {
	bot := newSinkBot()
	sink := NewTelegramSink(bot, "chat-1", nil)

	sink.TrackChatBinding(42, "user-1")

	sink.mu.Lock()
	task := sink.active[42]
	sink.mu.Unlock()
	if task == nil {
		t.Fatal("expected active task after TrackChatBinding")
	}
	if task.userID != "user-1" {
		t.Fatalf("task.userID = %q, want user-1", task.userID)
	}

	task.progress.Finish()
	task.progress.Wait()
	task.stream.Finish()
	task.stream.Wait()
}

func TestSinkNotifyCompletionRemembersBinding(t *testing.T) {
	binder, validator := newShellBinder(t)
	validator.set("sess-1", true)
	bot := newSinkBot()
	sink := NewTelegramSink(bot, "chat-1", nil, WithSinkBinder(binder))

	sink.TrackUserMessage(42, 100)
	sink.TrackChatBinding(42, "user-1")

	err := sink.NotifyCompletion(context.Background(), daemon.TaskCompletion{
		TaskID:      42,
		SessionID:   "sess-1",
		Status:      daemon.StatusDone,
		Summary:     "ok",
		StartedAt:   time.Unix(10, 0),
		CompletedAt: time.Unix(20, 0),
	})
	if err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}
	got, ok := binder.Lookup("chat-1", "user-1")
	if !ok || got != "sess-1" {
		t.Fatalf("binder.Lookup() = (%q, %v), want (sess-1, true)", got, ok)
	}
}

func TestSinkNotifyCompletionFailedStatusDoesNotRemember(t *testing.T) {
	binder, _ := newShellBinder(t)
	bot := newSinkBot()
	sink := NewTelegramSink(bot, "chat-1", nil, WithSinkBinder(binder))

	sink.TrackUserMessage(42, 100)
	sink.TrackChatBinding(42, "user-1")

	err := sink.NotifyCompletion(context.Background(), daemon.TaskCompletion{
		TaskID:      42,
		SessionID:   "sess-1",
		Status:      daemon.StatusFailed,
		Summary:     "failed",
		StartedAt:   time.Unix(10, 0),
		CompletedAt: time.Unix(20, 0),
	})
	if err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}
	if got, ok := binder.Lookup("chat-1", "user-1"); ok || got != "" {
		t.Fatalf("binder.Lookup() = (%q, %v), want miss after failed completion", got, ok)
	}
}

func TestSinkNotifyCompletionNoBinderIsNoOp(t *testing.T) {
	bot := newSinkBot()
	sink := NewTelegramSink(bot, "chat-1", nil)

	sink.TrackUserMessage(42, 100)
	sink.TrackChatBinding(42, "user-1")

	err := sink.NotifyCompletion(context.Background(), daemon.TaskCompletion{
		TaskID:      42,
		SessionID:   "sess-1",
		Status:      daemon.StatusDone,
		Summary:     "ok",
		StartedAt:   time.Unix(10, 0),
		CompletedAt: time.Unix(20, 0),
	})
	if err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}
	reactions := bot.getReactions()
	if len(reactions) != 1 {
		t.Fatalf("reactions = %d, want 1", len(reactions))
	}
}

func TestSinkNotifyCompletionMissingUserIDIsNoOp(t *testing.T) {
	binder, _ := newShellBinder(t)
	bot := newSinkBot()
	sink := NewTelegramSink(bot, "chat-1", nil, WithSinkBinder(binder))

	sink.TrackUserMessage(42, 100)

	err := sink.NotifyCompletion(context.Background(), daemon.TaskCompletion{
		TaskID:      42,
		SessionID:   "sess-1",
		Status:      daemon.StatusDone,
		Summary:     "ok",
		StartedAt:   time.Unix(10, 0),
		CompletedAt: time.Unix(20, 0),
	})
	if err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}
	if got, ok := binder.Lookup("chat-1", "user-1"); ok || got != "" {
		t.Fatalf("binder.Lookup() = (%q, %v), want miss when userID is unknown", got, ok)
	}
}

func TestSinkEnsureTaskCreatesOnce(t *testing.T) {
	bot := newSinkBot()
	sink := NewTelegramSink(bot, "chat-1", nil)

	sink.mu.Lock()
	task1 := sink.ensureTask(99)
	task2 := sink.ensureTask(99)
	sink.mu.Unlock()

	if task1 != task2 {
		t.Fatal("ensureTask returned different activeTask instances for same taskID")
	}

	if task1.progress == nil {
		t.Fatal("activeTask.progress is nil")
	}
	if task1.stream == nil {
		t.Fatal("activeTask.stream is nil")
	}

	// Clean up goroutines.
	task1.progress.Finish()
	task1.progress.Wait()
	task1.stream.Finish()
	task1.stream.Wait()
}

func TestSinkNotifyCompletionEditsProgressMessage(t *testing.T) {
	bot := newSinkBot()
	sink := NewTelegramSink(bot, "chat-1", nil)

	// Send stage progress to create a progress message.
	ev := daemon.EncodeProgressEvent(daemon.TextProgressEvent("[autopilot] stage: code"))
	sink.OnProgress(42, ev)

	// Wait for progress reporter to flush and create the message.
	time.Sleep(200 * time.Millisecond)

	sink.TrackUserMessage(42, 100)

	err := sink.NotifyCompletion(context.Background(), daemon.TaskCompletion{
		TaskID:      42,
		Status:      daemon.StatusDone,
		Summary:     "Build succeeded.",
		StartedAt:   time.Unix(10, 0),
		CompletedAt: time.Unix(16, 0),
	})
	if err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}

	edits := bot.getEdits()
	found := false
	for _, e := range edits {
		if contains(e.text, "<b>Complete</b>") && contains(e.text, "(6s)") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected completion header edit with elapsed time, edits=%+v", edits)
	}
}

func TestSinkNotifyCompletionRedactsSummary(t *testing.T) {
	bot := newSinkBot()
	redactor := func(s string) string {
		return strings.ReplaceAll(s, "sk-secret-key-12345", "[REDACTED]")
	}
	sink := NewTelegramSink(bot, "chat-1", nil, WithRedactor(redactor))

	sink.TrackUserMessage(42, 100)

	err := sink.NotifyCompletion(context.Background(), daemon.TaskCompletion{
		TaskID:      42,
		Status:      daemon.StatusDone,
		Summary:     "Result: sk-secret-key-12345 done",
		StartedAt:   time.Unix(10, 0),
		CompletedAt: time.Unix(20, 0),
	})
	if err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}

	sent := bot.getSent()
	for _, m := range sent {
		if strings.Contains(m.text, "sk-secret-key-12345") {
			t.Fatalf("secret leaked in sent message: %q", m.text)
		}
	}
	found := false
	for _, m := range sent {
		if strings.Contains(m.text, "[REDACTED]") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected redacted summary in sent messages, sent=%+v", sent)
	}
}

func TestSinkNoRedactorPassesThrough(t *testing.T) {
	bot := newSinkBot()
	sink := NewTelegramSink(bot, "chat-1", nil)

	sink.TrackUserMessage(42, 100)

	err := sink.NotifyCompletion(context.Background(), daemon.TaskCompletion{
		TaskID:      42,
		Status:      daemon.StatusDone,
		Summary:     "plain text summary",
		StartedAt:   time.Unix(10, 0),
		CompletedAt: time.Unix(20, 0),
	})
	if err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}

	sent := bot.getSent()
	found := false
	for _, m := range sent {
		if contains(m.text, "plain text summary") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected plain summary in sent messages, sent=%+v", sent)
	}
}

func TestParseSummaryStream(t *testing.T) {
	text, ok := parseSummaryStream("[summary] Hello world")
	if !ok {
		t.Fatal("expected parseSummaryStream to return true")
	}
	if text != "Hello world" {
		t.Fatalf("text = %q, want %q", text, "Hello world")
	}

	_, ok = parseSummaryStream("regular text")
	if ok {
		t.Fatal("expected parseSummaryStream to return false for non-summary text")
	}
}

func TestParseStageMarker(t *testing.T) {
	tests := []struct {
		input string
		stage string
		ok    bool
	}{
		{"[autopilot] stage: plan", "plan", true},
		{"[team] code", "code", true},
		{"[research] analyzing", "analyzing", true},
		{"regular text", "", false},
		{"[autopilot] stage: ", "", false},
	}
	for _, tt := range tests {
		stage, ok := parseStageMarker(tt.input)
		if ok != tt.ok || stage != tt.stage {
			t.Errorf("parseStageMarker(%q) = (%q, %v), want (%q, %v)", tt.input, stage, ok, tt.stage, tt.ok)
		}
	}
}

func TestFormatElapsed(t *testing.T) {
	tests := []struct {
		start, end time.Time
		want       string
	}{
		{time.Unix(10, 0), time.Unix(16, 0), " (6s)"},
		{time.Unix(10, 0), time.Unix(75, 0), " (1m5s)"},
		{time.Time{}, time.Unix(10, 0), ""},
	}
	for _, tt := range tests {
		got := formatElapsed(tt.start, tt.end)
		if got != tt.want {
			t.Errorf("formatElapsed(%v, %v) = %q, want %q", tt.start, tt.end, got, tt.want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
