package ambient

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/event"
)

const defaultMaxConcurrent = 2

// TaskRunFunc executes a task payload and streams events through the sink.
type TaskRunFunc func(ctx context.Context, payload string, sink event.Sink) (daemon.TaskResult, error)

// NotifyFunc sends a user-visible notification with the given title and body.
type NotifyFunc func(ctx context.Context, title, body string) error

// Config holds the dependencies and tuning parameters for a Scheduler.
type Config struct {
	Tasks         []BootTask
	Runner        TaskRunFunc
	NotifyFn      NotifyFunc
	MaxConcurrent int
	Logger        *slog.Logger
}

// Scheduler runs BootTasks according to their declared schedules.
type Scheduler struct {
	cfg    Config
	cancel context.CancelFunc
	wg     sync.WaitGroup
	sem    chan struct{}
	logger *slog.Logger
}

// NewScheduler constructs a Scheduler from cfg. Applies defaults for
// MaxConcurrent and Logger when they are zero/nil.
func NewScheduler(cfg Config) *Scheduler {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = defaultMaxConcurrent
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Scheduler{
		cfg:    cfg,
		sem:    make(chan struct{}, cfg.MaxConcurrent),
		logger: cfg.Logger,
	}
}

// Start launches goroutines for each configured task. It returns immediately;
// use Stop to wait for all goroutines to finish.
func (s *Scheduler) Start(ctx context.Context) {
	schedCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	for _, task := range s.cfg.Tasks {
		switch task.Schedule.Type {
		case ScheduleStartup:
			s.wg.Add(1)
			go func(t BootTask) {
				defer s.wg.Done()
				s.executeTask(schedCtx, t)
			}(task)

		case ScheduleInterval:
			s.wg.Add(1)
			go func(t BootTask) {
				defer s.wg.Done()
				s.executeTask(schedCtx, t)
				ticker := time.NewTicker(t.Schedule.Interval)
				defer ticker.Stop()
				for {
					select {
					case <-schedCtx.Done():
						return
					case <-ticker.C:
						s.executeTask(schedCtx, t)
					}
				}
			}(task)

		case ScheduleDaily:
			s.wg.Add(1)
			go func(t BootTask) {
				defer s.wg.Done()
				for {
					delay := nextDailyRun(time.Now(), t.Schedule.DailyAt)
					timer := time.NewTimer(delay)
					select {
					case <-schedCtx.Done():
						timer.Stop()
						return
					case <-timer.C:
						s.executeTask(schedCtx, t)
					}
				}
			}(task)
		}
	}

	s.logger.Info("ambient scheduler started", "tasks", len(s.cfg.Tasks))
}

// executeTask acquires the concurrency semaphore, runs the task, and optionally
// notifies the user on success or failure.
func (s *Scheduler) executeTask(ctx context.Context, task BootTask) {
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-s.sem }()

	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("boot task panic", "title", task.Title, "recover", r)
		}
	}()

	result, err := s.cfg.Runner(ctx, task.Prompt, event.NopSink{})
	if err != nil {
		s.logger.Error("boot task failed",
			"title", task.Title,
			"path", task.Path,
			"error", err,
		)
		if !task.Silent && s.cfg.NotifyFn != nil {
			_ = s.cfg.NotifyFn(ctx, task.Title, "Task failed: "+err.Error())
		}
		return
	}

	s.logger.Info("boot task completed",
		"title", task.Title,
		"path", task.Path,
	)

	if !task.Silent && s.cfg.NotifyFn != nil {
		summary := result.Summary
		if summary == "" {
			summary = result.Result
		}
		if len(summary) > 2000 {
			summary = summary[:2000] + "..."
		}
		_ = s.cfg.NotifyFn(ctx, task.Title, summary)
	}
}

// Stop cancels all scheduled goroutines and waits for them to exit.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	s.logger.Info("ambient scheduler stopped")
}
