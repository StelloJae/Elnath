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

type SignalBridge interface {
	RecordScheduledSignal(ctx context.Context, task ScheduledTask, queueTaskID int64, existed bool, enqueueErr error) error
}

type Option func(*Scheduler)

type Scheduler struct {
	tasks        []ScheduledTask
	enq          Enqueuer
	logger       *slog.Logger
	signalBridge SignalBridge
}

func WithSignalBridge(bridge SignalBridge) Option {
	return func(s *Scheduler) {
		s.signalBridge = bridge
	}
}

func New(tasks []ScheduledTask, enq Enqueuer, logger *slog.Logger, opts ...Option) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Scheduler{tasks: tasks, enq: enq, logger: logger}
	for _, opt := range opts {
		opt(s)
	}
	return s
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
		Type:            mapTaskType(task.Type),
		Prompt:          task.Prompt,
		SessionID:       task.SessionID,
		Surface:         task.Surface,
		DeliveryTargets: task.DeliveryTargets,
	}
	encoded := daemon.EncodeTaskPayload(payload)
	idemKey := "scheduled:" + task.Name

	id, existed, err := s.enq.Enqueue(ctx, encoded, idemKey)
	if s.signalBridge != nil {
		if bridgeErr := s.signalBridge.RecordScheduledSignal(ctx, task, id, existed, err); bridgeErr != nil {
			s.logger.Warn("scheduler: signal bridge failed", "task", task.Name, "error", bridgeErr)
		}
	}
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
	if raw == "skill-promote" {
		return daemon.TaskTypeSkillPromote
	}
	return daemon.TaskTypeAgent
}
