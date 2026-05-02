package enqueue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/secret"
)

var (
	ErrNotEligible      = errors.New("enqueue: task is not eligible")
	ErrConfigDisallows  = errors.New("enqueue: config disallows requested mode")
	ErrAlreadyQueueBack = errors.New("enqueue: task is already linked to queue task")
)

type Queue interface {
	EnqueueTx(ctx context.Context, tx *sql.Tx, payload string, idemKey string) (int64, bool, error)
}

type Options struct {
	EnforcementMode    string
	CompletionGateMode string
}

type Request struct {
	TaskID                  int64
	OperatorID              string
	Reason                  string
	RequestedEnforcement    string
	RequestedCompletionGate string
}

type Result struct {
	Task        *agentic.AgenticTask
	Decision    *agentic.TaskEnqueueDecision
	QueueTaskID int64
	Existed     bool
}

type Service struct {
	store *agentic.Store
	queue Queue
	opts  Options
}

func NewService(store *agentic.Store, queue Queue, opts Options) *Service {
	return &Service{store: store, queue: queue, opts: opts}
}

func (s *Service) Enqueue(ctx context.Context, req Request) (*Result, error) {
	if s == nil || s.store == nil || s.queue == nil {
		return nil, errors.New("enqueue: nil service")
	}
	task, err := s.store.GetAgenticTask(ctx, req.TaskID)
	if err != nil {
		return nil, fmt.Errorf("enqueue: get task: %w", err)
	}
	if task.QueueTaskID != 0 {
		decision, ok, err := latestEnqueuedDecision(ctx, s.store, task.ID)
		if err != nil {
			return nil, err
		}
		if ok && decision.QueueTaskID == task.QueueTaskID {
			return &Result{Task: task, Decision: decision, QueueTaskID: task.QueueTaskID, Existed: true}, nil
		}
		return nil, fmt.Errorf("%w %d", ErrAlreadyQueueBack, task.QueueTaskID)
	}
	if task.Status != agentic.TaskStatusProposed {
		return nil, fmt.Errorf("%w: status %q", ErrNotEligible, task.Status)
	}

	req.RequestedEnforcement = strings.ToLower(strings.TrimSpace(req.RequestedEnforcement))
	req.RequestedCompletionGate = strings.ToLower(strings.TrimSpace(req.RequestedCompletionGate))
	if err := s.validateModes(req); err != nil {
		return nil, err
	}

	decision := agentic.TaskEnqueueDecision{
		TaskID:                  task.ID,
		OperatorID:              strings.TrimSpace(req.OperatorID),
		Decision:                agentic.TaskEnqueueDecisionApproved,
		Reason:                  strings.TrimSpace(secret.NewDetector().RedactString(req.Reason)),
		RequestedEnforcement:    req.RequestedEnforcement,
		RequestedCompletionGate: req.RequestedCompletionGate,
		Status:                  agentic.TaskEnqueueStatusPending,
	}

	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:                task.Prompt,
		AgenticEnforcement:    req.RequestedEnforcement,
		AgenticCompletionGate: req.RequestedCompletionGate,
	})
	decisionResult, task, queueTaskID, existed, err := s.store.EnqueueProposedTask(ctx, task.ID, decision, s.queue, payload, idempotencyKey(task.ID, req.RequestedEnforcement, req.RequestedCompletionGate))
	if err != nil {
		if isActiveDecisionConflict(err) {
			if result, ok := s.existingResult(ctx, task.ID); ok {
				return result, nil
			}
			return nil, fmt.Errorf("%w: concurrent enqueue already in progress", ErrAlreadyQueueBack)
		}
		return nil, fmt.Errorf("enqueue: queue proposed task: %w", err)
	}
	return &Result{Task: task, Decision: decisionResult, QueueTaskID: queueTaskID, Existed: existed}, nil
}

func (s *Service) existingResult(ctx context.Context, taskID int64) (*Result, bool) {
	task, err := s.store.GetAgenticTask(ctx, taskID)
	if err != nil || task.QueueTaskID == 0 {
		return nil, false
	}
	decision, ok, err := latestEnqueuedDecision(ctx, s.store, task.ID)
	if err != nil || !ok || decision.QueueTaskID != task.QueueTaskID {
		return nil, false
	}
	return &Result{Task: task, Decision: decision, QueueTaskID: task.QueueTaskID, Existed: true}, true
}

func isActiveDecisionConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "idx_task_enqueue_decisions_task_active") ||
		(strings.Contains(msg, "unique") && strings.Contains(msg, "task_enqueue_decisions"))
}

func (s *Service) validateModes(req Request) error {
	switch req.RequestedEnforcement {
	case "":
	case config.AgenticEnforcementModeGateway:
		if strings.ToLower(strings.TrimSpace(s.opts.EnforcementMode)) != config.AgenticEnforcementModeGateway {
			return fmt.Errorf("%w: agentic.enforcement.mode does not permit gateway", ErrConfigDisallows)
		}
	default:
		return fmt.Errorf("enqueue: unsupported agentic enforcement mode %q", req.RequestedEnforcement)
	}
	switch req.RequestedCompletionGate {
	case "":
	case config.AgenticCompletionGateModeVerification:
		if strings.ToLower(strings.TrimSpace(s.opts.CompletionGateMode)) != config.AgenticCompletionGateModeVerification {
			return fmt.Errorf("%w: agentic.completion_gate.mode does not permit verification", ErrConfigDisallows)
		}
	default:
		return fmt.Errorf("enqueue: unsupported completion gate mode %q", req.RequestedCompletionGate)
	}
	return nil
}

func latestEnqueuedDecision(ctx context.Context, store *agentic.Store, taskID int64) (*agentic.TaskEnqueueDecision, bool, error) {
	decisions, err := store.ListTaskEnqueueDecisionsByTask(ctx, taskID)
	if err != nil {
		return nil, false, fmt.Errorf("enqueue: list decisions: %w", err)
	}
	for i := len(decisions) - 1; i >= 0; i-- {
		if decisions[i].Status == agentic.TaskEnqueueStatusEnqueued {
			return &decisions[i], true, nil
		}
	}
	return nil, false, nil
}

func idempotencyKey(taskID int64, enforcement, completion string) string {
	return fmt.Sprintf("agentic-proposed-task:%d:enforcement:%s:completion:%s", taskID, enforcement, completion)
}
