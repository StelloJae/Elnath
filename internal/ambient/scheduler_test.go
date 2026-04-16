package ambient

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/event"
)

func makeRunner(result daemon.TaskResult, err error) TaskRunFunc {
	return func(_ context.Context, _ string, _ event.Sink) (daemon.TaskResult, error) {
		return result, err
	}
}

func TestScheduler_StartupTask(t *testing.T) {
	var calls atomic.Int64
	runner := func(_ context.Context, _ string, _ event.Sink) (daemon.TaskResult, error) {
		calls.Add(1)
		return daemon.TaskResult{Summary: "done"}, nil
	}

	cfg := Config{
		Tasks: []BootTask{
			{Title: "startup", Schedule: Schedule{Type: ScheduleStartup}, Silent: true},
		},
		Runner:        runner,
		MaxConcurrent: 2,
	}
	s := NewScheduler(cfg)
	s.Start(context.Background())
	s.Stop()

	if calls.Load() != 1 {
		t.Errorf("expected runner called once, got %d", calls.Load())
	}
}

func TestScheduler_IntervalTask(t *testing.T) {
	var calls atomic.Int64
	runner := func(_ context.Context, _ string, _ event.Sink) (daemon.TaskResult, error) {
		calls.Add(1)
		return daemon.TaskResult{Summary: "done"}, nil
	}

	cfg := Config{
		Tasks: []BootTask{
			{
				Title:    "interval",
				Schedule: Schedule{Type: ScheduleInterval, Interval: 20 * time.Millisecond},
				Silent:   true,
			},
		},
		Runner:        runner,
		MaxConcurrent: 2,
	}
	s := NewScheduler(cfg)
	s.Start(context.Background())
	time.Sleep(80 * time.Millisecond)
	s.Stop()

	if calls.Load() < 2 {
		t.Errorf("expected runner called ≥2 times in 80ms, got %d", calls.Load())
	}
}

func TestScheduler_NotifiesOnCompletion(t *testing.T) {
	var notifyCalls atomic.Int64
	var lastTitle, lastBody atomic.Value

	notifyFn := func(_ context.Context, title, body string) error {
		notifyCalls.Add(1)
		lastTitle.Store(title)
		lastBody.Store(body)
		return nil
	}

	cfg := Config{
		Tasks: []BootTask{
			{
				Title:    "my task",
				Schedule: Schedule{Type: ScheduleStartup},
				Silent:   false,
			},
		},
		Runner:        makeRunner(daemon.TaskResult{Summary: "all good"}, nil),
		NotifyFn:      notifyFn,
		MaxConcurrent: 2,
	}
	s := NewScheduler(cfg)
	s.Start(context.Background())
	s.Stop()

	if notifyCalls.Load() != 1 {
		t.Errorf("expected 1 notification, got %d", notifyCalls.Load())
	}
	if title, ok := lastTitle.Load().(string); !ok || title != "my task" {
		t.Errorf("unexpected notification title: %v", lastTitle.Load())
	}
	if body, ok := lastBody.Load().(string); !ok || body != "all good" {
		t.Errorf("unexpected notification body: %v", lastBody.Load())
	}
}

func TestScheduler_SilentNoNotify(t *testing.T) {
	var notifyCalls atomic.Int64

	notifyFn := func(_ context.Context, _, _ string) error {
		notifyCalls.Add(1)
		return nil
	}

	cfg := Config{
		Tasks: []BootTask{
			{
				Title:    "silent task",
				Schedule: Schedule{Type: ScheduleStartup},
				Silent:   true,
			},
		},
		Runner:        makeRunner(daemon.TaskResult{Summary: "done"}, nil),
		NotifyFn:      notifyFn,
		MaxConcurrent: 2,
	}
	s := NewScheduler(cfg)
	s.Start(context.Background())
	s.Stop()

	if notifyCalls.Load() != 0 {
		t.Errorf("expected 0 notifications for silent task, got %d", notifyCalls.Load())
	}
}

func TestScheduler_ConcurrencyLimit(t *testing.T) {
	var active atomic.Int64
	var maxObserved atomic.Int64

	runner := func(ctx context.Context, _ string, _ event.Sink) (daemon.TaskResult, error) {
		cur := active.Add(1)
		defer active.Add(-1)
		// Update max if current exceeds it (compare-and-swap loop).
		for {
			old := maxObserved.Load()
			if cur <= old {
				break
			}
			if maxObserved.CompareAndSwap(old, cur) {
				break
			}
		}
		// Simulate work so tasks overlap.
		time.Sleep(30 * time.Millisecond)
		return daemon.TaskResult{Summary: "done"}, nil
	}

	tasks := make([]BootTask, 5)
	for i := range tasks {
		tasks[i] = BootTask{
			Title:    "task",
			Schedule: Schedule{Type: ScheduleStartup},
			Silent:   true,
		}
	}

	cfg := Config{
		Tasks:         tasks,
		Runner:        runner,
		MaxConcurrent: 2,
	}
	s := NewScheduler(cfg)
	s.Start(context.Background())
	s.Stop()

	if maxObserved.Load() > 2 {
		t.Errorf("expected max concurrent ≤ 2, observed %d", maxObserved.Load())
	}
}

func TestScheduler_StopGraceful(t *testing.T) {
	blocker := make(chan struct{})

	runner := func(ctx context.Context, _ string, _ event.Sink) (daemon.TaskResult, error) {
		select {
		case <-ctx.Done():
			return daemon.TaskResult{}, ctx.Err()
		case <-blocker:
			return daemon.TaskResult{Summary: "done"}, nil
		}
	}

	cfg := Config{
		Tasks: []BootTask{
			{
				Title:    "slow",
				Schedule: Schedule{Type: ScheduleInterval, Interval: 10 * time.Second},
				Silent:   true,
			},
		},
		Runner:        runner,
		MaxConcurrent: 2,
	}
	s := NewScheduler(cfg)
	s.Start(context.Background())

	// Give the first execution time to start and block.
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Stop returned promptly after context cancellation unblocked the runner.
	case <-time.After(5 * time.Second):
		t.Error("Stop() did not return within 5 seconds")
	}
}

func TestScheduler_NotifiesOnFailure(t *testing.T) {
	var notifyCalls atomic.Int64
	var lastBody atomic.Value

	notifyFn := func(_ context.Context, _, body string) error {
		notifyCalls.Add(1)
		lastBody.Store(body)
		return nil
	}

	cfg := Config{
		Tasks: []BootTask{
			{
				Title:    "failing task",
				Schedule: Schedule{Type: ScheduleStartup},
				Silent:   false,
			},
		},
		Runner:        makeRunner(daemon.TaskResult{}, errors.New("boom")),
		NotifyFn:      notifyFn,
		MaxConcurrent: 2,
	}
	s := NewScheduler(cfg)
	s.Start(context.Background())
	s.Stop()

	if notifyCalls.Load() != 1 {
		t.Errorf("expected 1 failure notification, got %d", notifyCalls.Load())
	}
	if body, ok := lastBody.Load().(string); !ok || body != "Task failed: boom" {
		t.Errorf("unexpected failure body: %v", lastBody.Load())
	}
}
