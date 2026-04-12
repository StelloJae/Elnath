package conversation

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/wiki"
)

type recordingEventIngester struct {
	mu      sync.Mutex
	events  []wiki.IngestEvent
	err     error
	started chan struct{}
	done    chan struct{}
	sleep   time.Duration
}

func (r *recordingEventIngester) IngestSession(_ context.Context, event wiki.IngestEvent) error {
	if r.started != nil {
		close(r.started)
	}
	if r.sleep > 0 {
		time.Sleep(r.sleep)
	}
	r.mu.Lock()
	r.events = append(r.events, wiki.IngestEvent{
		SessionID: event.SessionID,
		Messages:  append([]llm.Message(nil), event.Messages...),
		Reason:    event.Reason,
		Principal: event.Principal,
		StartedAt: event.StartedAt,
		Duration:  event.Duration,
	})
	r.mu.Unlock()
	if r.done != nil {
		close(r.done)
	}
	return r.err
}

func (r *recordingEventIngester) snapshot() []wiki.IngestEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]wiki.IngestEvent(nil), r.events...)
}

func newTestLogger(buf io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestSpine_NotifyCompletion_NoIngesterIsNoop(t *testing.T) {
	spine := NewSpine(t.TempDir(), nil, newTestLogger(io.Discard))

	if err := spine.NotifyCompletion(context.Background(), daemon.TaskCompletion{SessionID: "sess-123"}); err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}
}

func TestSpine_NotifyCompletion_EmptySessionIDIsNoop(t *testing.T) {
	spine := NewSpine(t.TempDir(), &recordingEventIngester{}, newTestLogger(io.Discard))

	if err := spine.NotifyCompletion(context.Background(), daemon.TaskCompletion{}); err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}
}

func TestSpine_NotifyCompletion_InvalidSessionIDIsLoggedNoError(t *testing.T) {
	var logs bytes.Buffer
	spine := NewSpine(t.TempDir(), &recordingEventIngester{}, newTestLogger(&logs))

	if err := spine.NotifyCompletion(context.Background(), daemon.TaskCompletion{SessionID: "missing-session"}); err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}
	if !strings.Contains(logs.String(), "conversation spine: load session failed") {
		t.Fatalf("expected warning log, got %q", logs.String())
	}
	if !strings.Contains(logs.String(), "missing-session") {
		t.Fatalf("expected session ID in logs, got %q", logs.String())
	}
}

func TestSpine_NotifyCompletion_LoadsAndDispatchesIngest(t *testing.T) {
	dir := t.TempDir()
	principal := identity.Principal{UserID: "12345", ProjectID: "elnath", Surface: "telegram"}
	sess, err := agent.NewSession(dir, principal)
	if err != nil {
		t.Fatalf("agent.NewSession: %v", err)
	}
	if err := sess.AppendMessages([]llm.Message{
		llm.NewUserMessage("hello spine"),
		llm.NewAssistantMessage("hello back"),
	}); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}

	ing := &recordingEventIngester{done: make(chan struct{})}
	spine := NewSpine(dir, ing, newTestLogger(io.Discard))
	startedAt := time.Date(2026, time.April, 11, 10, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(95 * time.Second)

	if err := spine.NotifyCompletion(context.Background(), daemon.TaskCompletion{
		SessionID:   sess.ID,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
	}); err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}

	select {
	case <-ing.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ingest")
	}

	events := ing.snapshot()
	if len(events) != 1 {
		t.Fatalf("ingest events = %d, want 1", len(events))
	}
	if events[0].SessionID != sess.ID {
		t.Fatalf("session ID = %q, want %q", events[0].SessionID, sess.ID)
	}
	if len(events[0].Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(events[0].Messages))
	}
	if events[0].Reason != "task_completed" {
		t.Fatalf("reason = %q, want %q", events[0].Reason, "task_completed")
	}
	if events[0].Principal != "telegram:12345" {
		t.Fatalf("principal = %q, want %q", events[0].Principal, "telegram:12345")
	}
	if !events[0].StartedAt.Equal(startedAt) {
		t.Fatalf("started_at = %v, want %v", events[0].StartedAt, startedAt)
	}
	if events[0].Duration != 95*time.Second {
		t.Fatalf("duration = %v, want %v", events[0].Duration, 95*time.Second)
	}
}

func TestSpine_NotifyCompletion_IsNonBlocking(t *testing.T) {
	sess, dir := newTestSession(t)
	if err := sess.AppendMessage(llm.NewUserMessage("blocking test")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	ing := &recordingEventIngester{
		started: make(chan struct{}),
		done:    make(chan struct{}),
		sleep:   500 * time.Millisecond,
	}
	spine := NewSpine(dir, ing, newTestLogger(io.Discard)).WithIngestTimeout(time.Second)

	start := time.Now()
	if err := spine.NotifyCompletion(context.Background(), daemon.TaskCompletion{SessionID: sess.ID}); err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("NotifyCompletion blocked for %v", elapsed)
	}

	select {
	case <-ing.started:
	case <-time.After(time.Second):
		t.Fatal("ingest goroutine did not start")
	}

	select {
	case <-ing.done:
		t.Fatal("ingest finished too early")
	case <-time.After(400 * time.Millisecond):
	}

	select {
	case <-ing.done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for background ingest to finish")
	}
}

func TestSpine_Snapshot_IsStable(t *testing.T) {
	sess, dir := newTestSession(t)
	if err := sess.AppendMessages([]llm.Message{
		llm.NewUserMessage("before one"),
		llm.NewAssistantMessage("before two"),
	}); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}

	ing := &recordingEventIngester{done: make(chan struct{}), sleep: 100 * time.Millisecond}
	spine := NewSpine(dir, ing, newTestLogger(io.Discard))

	if err := spine.NotifyCompletion(context.Background(), daemon.TaskCompletion{SessionID: sess.ID}); err != nil {
		t.Fatalf("NotifyCompletion: %v", err)
	}
	if err := sess.AppendMessage(llm.NewUserMessage("after notify")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := sess.AppendMessage(llm.NewAssistantMessage("after notify two")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	select {
	case <-ing.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ingest")
	}

	events := ing.snapshot()
	if len(events) != 1 {
		t.Fatalf("ingest events = %d, want 1", len(events))
	}
	if len(events[0].Messages) != 2 {
		t.Fatalf("snapshot message count = %d, want 2", len(events[0].Messages))
	}
	if got := events[0].Messages[0].Text(); got != "before one" {
		t.Fatalf("first message = %q, want %q", got, "before one")
	}
	if got := events[0].Messages[1].Text(); got != "before two" {
		t.Fatalf("second message = %q, want %q", got, "before two")
	}
	if events[0].Reason != "task_completed" {
		t.Fatalf("reason = %q, want %q", events[0].Reason, "task_completed")
	}
}

func TestIngestSession_EmptySessionIDIsNoop(t *testing.T) {
	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ing := wiki.NewIngester(store, nil)

	err = ing.IngestSession(context.Background(), wiki.IngestEvent{Messages: []llm.Message{llm.NewUserMessage("hello")}})
	if err != nil {
		t.Fatalf("IngestSession: %v", err)
	}

	pages, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(pages) != 0 {
		t.Fatalf("expected no pages, got %d", len(pages))
	}
}

func TestIngestSession_EmptyMessagesIsNoop(t *testing.T) {
	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ing := wiki.NewIngester(store, nil)

	err = ing.IngestSession(context.Background(), wiki.IngestEvent{SessionID: "sess-empty"})
	if err != nil {
		t.Fatalf("IngestSession: %v", err)
	}

	pages, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(pages) != 0 {
		t.Fatalf("expected no pages, got %d", len(pages))
	}
}

func TestIngestSession_CreatesStructuredSessionPage(t *testing.T) {
	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ing := wiki.NewIngester(store, nil)

	err = ing.IngestSession(context.Background(), wiki.IngestEvent{
		SessionID: "sess-event",
		Messages: []llm.Message{
			llm.NewUserMessage("Hello from event"),
			llm.NewAssistantMessage("Hello from ingest"),
		},
		Reason:    "task_completed",
		Principal: "cli:stello",
	})
	if err != nil {
		t.Fatalf("IngestSession: %v", err)
	}

	page, err := store.Read("sessions/sess-event.md")
	if err != nil {
		t.Fatalf("store.Read: %v", err)
	}
	if !strings.Contains(page.Content, "## Session Metadata") {
		t.Fatalf("expected metadata section, got:\n%s", page.Content)
	}
	if !strings.Contains(page.Content, "Hello from event") {
		t.Fatalf("expected event transcript content, got:\n%s", page.Content)
	}
	if !strings.Contains(page.Content, "assistant: Hello from ingest") {
		t.Fatalf("expected assistant transcript content, got:\n%s", page.Content)
	}
}

func TestSpine_String(t *testing.T) {
	if got := NewSpine(t.TempDir(), nil, newTestLogger(io.Discard)).String(); got != "ConversationSpine" {
		t.Fatalf("String() = %q, want %q", got, "ConversationSpine")
	}
}
