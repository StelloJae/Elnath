package daemon

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// recordingSink captures all completions it receives.
type recordingSink struct {
	received []TaskCompletion
	err      error
}

func (s *recordingSink) NotifyCompletion(_ context.Context, c TaskCompletion) error {
	s.received = append(s.received, c)
	return s.err
}

func testCompletion() TaskCompletion {
	now := time.Now()
	return TaskCompletion{
		TaskID:      42,
		SessionID:   "sess-abc",
		Summary:     "all done",
		Status:      StatusDone,
		CreatedAt:   now.Add(-5 * time.Second),
		StartedAt:   now.Add(-2 * time.Second),
		CompletedAt: now,
	}
}

func TestDeliveryRouter_NoSinks(t *testing.T) {
	router := NewDeliveryRouter(slog.Default())
	if err := router.Deliver(context.Background(), testCompletion()); err != nil {
		t.Errorf("expected nil error with no sinks, got: %v", err)
	}
}

func TestDeliveryRouter_OneSinkSuccess(t *testing.T) {
	router := NewDeliveryRouter(slog.Default())
	sink := &recordingSink{}
	router.Register(sink)

	c := testCompletion()
	if err := router.Deliver(context.Background(), c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sink.received) != 1 {
		t.Fatalf("sink received %d completions, want 1", len(sink.received))
	}
	if sink.received[0].TaskID != c.TaskID {
		t.Errorf("task_id = %d, want %d", sink.received[0].TaskID, c.TaskID)
	}
}

func TestDeliveryRouter_OneSinkFailure(t *testing.T) {
	router := NewDeliveryRouter(slog.Default())
	sinkErr := errors.New("sink unavailable")
	sink := &recordingSink{err: sinkErr}
	router.Register(sink)

	// With a single failing sink, all sinks fail → error returned.
	err := router.Deliver(context.Background(), testCompletion())
	if err == nil {
		t.Fatal("expected error when all sinks fail, got nil")
	}
	if !errors.Is(err, sinkErr) {
		t.Errorf("error = %v, want to wrap %v", err, sinkErr)
	}
}

func TestDeliveryRouter_MixedSinks(t *testing.T) {
	router := NewDeliveryRouter(slog.Default())
	failing := &recordingSink{err: errors.New("boom")}
	succeeding := &recordingSink{}
	router.Register(failing)
	router.Register(succeeding)

	// Partial success: one sink succeeds, one fails → no error returned.
	if err := router.Deliver(context.Background(), testCompletion()); err != nil {
		t.Errorf("expected nil when at least one sink succeeds, got: %v", err)
	}
	if len(succeeding.received) != 1 {
		t.Errorf("succeeding sink received %d completions, want 1", len(succeeding.received))
	}
	// Failing sink still had NotifyCompletion called.
	if len(failing.received) != 1 {
		t.Errorf("failing sink received %d completions, want 1", len(failing.received))
	}
}

func TestDeliveryRouter_AllSinksFail(t *testing.T) {
	router := NewDeliveryRouter(slog.Default())
	err1 := errors.New("err1")
	err2 := errors.New("err2")
	router.Register(&recordingSink{err: err1})
	router.Register(&recordingSink{err: err2})

	err := router.Deliver(context.Background(), testCompletion())
	if err == nil {
		t.Fatal("expected error when all sinks fail, got nil")
	}
	if !errors.Is(err, err1) {
		t.Errorf("error does not wrap err1: %v", err)
	}
	if !errors.Is(err, err2) {
		t.Errorf("error does not wrap err2: %v", err)
	}
}

func TestLogSink(t *testing.T) {
	sink := NewLogSink(slog.Default())
	c := testCompletion()
	if err := sink.NotifyCompletion(context.Background(), c); err != nil {
		t.Errorf("LogSink.NotifyCompletion returned error: %v", err)
	}
}

func TestLogSink_NilLogger(t *testing.T) {
	// Should not panic when nil logger is passed.
	sink := NewLogSink(nil)
	if err := sink.NotifyCompletion(context.Background(), testCompletion()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
