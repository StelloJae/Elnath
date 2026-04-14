package daemon

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// recordingSink captures all completions it receives.
type recordingSink struct {
	name     string
	received []TaskCompletion
	err      error
	errs     []error
	mu       sync.Mutex
}

func (s *recordingSink) NotifyCompletion(_ context.Context, c TaskCompletion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append(s.received, c)
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		return err
	}
	return s.err
}

func (s *recordingSink) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.received)
}

func (s *recordingSink) Completion(i int) TaskCompletion {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.received[i]
}

func (s *recordingSink) String() string {
	if s.name != "" {
		return s.name
	}
	return "recordingSink"
}

func mustNewDeliveryRouter(t *testing.T, db *sql.DB) *DeliveryRouter {
	t.Helper()
	router, err := NewDeliveryRouter(db, slog.Default())
	if err != nil {
		t.Fatalf("NewDeliveryRouter: %v", err)
	}
	return router
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
	router := mustNewDeliveryRouter(t, nil)
	if err := router.Deliver(context.Background(), testCompletion()); err != nil {
		t.Errorf("expected nil error with no sinks, got: %v", err)
	}
}

func TestDeliveryRouter_OneSinkSuccess(t *testing.T) {
	router := mustNewDeliveryRouter(t, nil)
	sink := &recordingSink{}
	router.Register(sink)

	c := testCompletion()
	if err := router.Deliver(context.Background(), c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sink.Count() != 1 {
		t.Fatalf("sink received %d completions, want 1", sink.Count())
	}
	if sink.Completion(0).TaskID != c.TaskID {
		t.Errorf("task_id = %d, want %d", sink.Completion(0).TaskID, c.TaskID)
	}
}

func TestDeliverDedupSameTaskSameSink(t *testing.T) {
	router := mustNewDeliveryRouter(t, openTestDB(t))
	sink := &recordingSink{name: "telegram"}
	router.Register(sink)

	completion := testCompletion()
	if err := router.Deliver(context.Background(), completion); err != nil {
		t.Fatalf("Deliver(first): %v", err)
	}
	if err := router.Deliver(context.Background(), completion); err != nil {
		t.Fatalf("Deliver(second): %v", err)
	}
	if sink.Count() != 1 {
		t.Fatalf("sink received %d completions, want 1", sink.Count())
	}
}

func TestDeliverDifferentSinksBothCalled(t *testing.T) {
	router := mustNewDeliveryRouter(t, openTestDB(t))
	first := &recordingSink{name: "telegram"}
	second := &recordingSink{name: "log"}
	router.Register(first)
	router.Register(second)

	if err := router.Deliver(context.Background(), testCompletion()); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if first.Count() != 1 {
		t.Fatalf("first sink received %d completions, want 1", first.Count())
	}
	if second.Count() != 1 {
		t.Fatalf("second sink received %d completions, want 1", second.Count())
	}
}

func TestDeliverNoDBSkipsDedup(t *testing.T) {
	router := mustNewDeliveryRouter(t, nil)
	sink := &recordingSink{name: "telegram"}
	router.Register(sink)

	completion := testCompletion()
	if err := router.Deliver(context.Background(), completion); err != nil {
		t.Fatalf("Deliver(first): %v", err)
	}
	if err := router.Deliver(context.Background(), completion); err != nil {
		t.Fatalf("Deliver(second): %v", err)
	}
	if sink.Count() != 2 {
		t.Fatalf("sink received %d completions, want 2", sink.Count())
	}
}

func TestDeliverRetriesAfterSinkFailure(t *testing.T) {
	router := mustNewDeliveryRouter(t, openTestDB(t))
	sink := &recordingSink{name: "telegram", errs: []error{errors.New("temporary failure")}}
	router.Register(sink)

	completion := testCompletion()
	if err := router.Deliver(context.Background(), completion); err == nil {
		t.Fatal("first delivery should fail when the only sink fails")
	}
	if err := router.Deliver(context.Background(), completion); err != nil {
		t.Fatalf("second delivery should retry successfully: %v", err)
	}
	if sink.Count() != 2 {
		t.Fatalf("sink received %d completions, want 2", sink.Count())
	}
}

func TestDeliveryRouter_OneSinkFailure(t *testing.T) {
	router := mustNewDeliveryRouter(t, nil)
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
	router := mustNewDeliveryRouter(t, nil)
	failing := &recordingSink{err: errors.New("boom")}
	succeeding := &recordingSink{}
	router.Register(failing)
	router.Register(succeeding)

	// Partial success: one sink succeeds, one fails → no error returned.
	if err := router.Deliver(context.Background(), testCompletion()); err != nil {
		t.Errorf("expected nil when at least one sink succeeds, got: %v", err)
	}
	if succeeding.Count() != 1 {
		t.Errorf("succeeding sink received %d completions, want 1", succeeding.Count())
	}
	// Failing sink still had NotifyCompletion called.
	if failing.Count() != 1 {
		t.Errorf("failing sink received %d completions, want 1", failing.Count())
	}
}

func TestDeliveryRouter_AllSinksFail(t *testing.T) {
	router := mustNewDeliveryRouter(t, nil)
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
