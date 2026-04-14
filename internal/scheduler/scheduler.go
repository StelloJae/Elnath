package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/stello/elnath/internal/daemon"
)

type Enqueuer interface {
	Enqueue(ctx context.Context, payload string, idemKey string) (int64, bool, error)
}

type Scheduler struct {
	tasks  []ScheduledTask
	enq    Enqueuer
	logger *slog.Logger
}

func New(tasks []ScheduledTask, enq Enqueuer, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{tasks: tasks, enq: enq, logger: logger}
}

func (s *Scheduler) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for _, task := range s.tasks {
		task := task
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runTask(ctx, task)
		}()
	}
	wg.Wait()
	return nil
}

func (s *Scheduler) runTask(ctx context.Context, task ScheduledTask) {
	if task.RunOnStart {
		s.enqueueOnce(ctx, task)
	}

	ticker := time.NewTicker(task.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.enqueueOnce(ctx, task)
		}
	}
}

func (s *Scheduler) enqueueOnce(ctx context.Context, task ScheduledTask) {
	if ctx.Err() != nil {
		return
	}

	payload := daemon.TaskPayload{
		Type:      mapTaskType(task.Type),
		Prompt:    task.Prompt,
		SessionID: task.SessionID,
		Surface:   task.Surface,
	}
	encoded := daemon.EncodeTaskPayload(payload)
	idemKey := "scheduled:" + task.Name

	id, existed, err := s.enq.Enqueue(ctx, encoded, idemKey)
	switch {
	case err != nil:
		s.logger.Warn("scheduler: enqueue failed", "task", task.Name, "error", err)
	case existed:
		s.logger.Info("scheduler: skipped (previous run still active)", "task", task.Name)
	default:
		s.logger.Info("scheduler: enqueued", "task", task.Name, "task_id", id)
	}
}

func mapTaskType(raw string) daemon.TaskType {
	if raw == "research" {
		return daemon.TaskTypeResearch
	}
	return daemon.TaskTypeAgent
}
