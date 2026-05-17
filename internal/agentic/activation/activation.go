package activation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/agentic"
	agenticenqueue "github.com/stello/elnath/internal/agentic/enqueue"
	"github.com/stello/elnath/internal/agentic/followup"
	"github.com/stello/elnath/internal/agentic/triage"
)

const ExecutionPolicyProposeOnly = "propose_only"
const ExecutionPolicyAutoEnqueueLowRisk = "auto_enqueue_low_risk"

type AutoEnqueueOptions struct {
	Enabled                 bool
	Limit                   int
	OperatorID              string
	Reason                  string
	MaxRiskLevel            string
	RequestedEnforcement    string
	RequestedCompletionGate string
}

type AutoEnqueueResult struct {
	Considered      int
	Enqueued        int
	Skipped         int
	Failed          int
	EnqueuedTaskIDs []int64
	QueueTaskIDs    []int64
}

type Result struct {
	RunID            int64
	Limit            int
	ExecutionPolicy  string
	EnqueuePerformed bool
	Status           string
	Reason           string
	CreatedAt        time.Time
	ProposedTaskIDs  []int64
	AutoEnqueue      AutoEnqueueResult
	Followups        followup.Result
	Signals          triage.Result
}

type enqueuer interface {
	Enqueue(context.Context, agenticenqueue.Request) (*agenticenqueue.Result, error)
}

type Service struct {
	store            *agentic.Store
	autoEnqueue      AutoEnqueueOptions
	autoEnqueueQueue enqueuer
}

type Option func(*Service)

func WithAutoEnqueue(queue enqueuer, opts AutoEnqueueOptions) Option {
	return func(s *Service) {
		s.autoEnqueueQueue = queue
		s.autoEnqueue = opts
	}
}

func NewService(store *agentic.Store, opts ...Option) *Service {
	s := &Service{store: store}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) RunOnce(ctx context.Context, limit int) (Result, error) {
	if s == nil || s.store == nil {
		return Result{}, errors.New("activation: nil service")
	}
	if limit <= 0 {
		limit = 25
	}
	result := Result{
		Limit:           limit,
		ExecutionPolicy: ExecutionPolicyProposeOnly,
		Status:          agentic.ActivationRunStatusSucceeded,
	}
	if s.autoEnqueue.Enabled {
		result.ExecutionPolicy = ExecutionPolicyAutoEnqueueLowRisk
	}
	followups, err := followup.NewScheduler(s.store).RunOnce(ctx, limit)
	if err != nil {
		result.Followups = followups
		return s.record(ctx, result, err)
	}
	result.Followups = followups
	signals, err := triage.NewTriager(s.store).RunOnce(ctx, limit)
	if err != nil {
		result.Signals = signals
		result.ProposedTaskIDs = activationTaskIDs(result.Followups, result.Signals)
		return s.record(ctx, result, err)
	}
	result.Signals = signals
	result.ProposedTaskIDs = activationTaskIDs(result.Followups, result.Signals)
	autoEnqueue, err := s.autoEnqueueProposed(ctx, result.ProposedTaskIDs)
	result.AutoEnqueue = autoEnqueue
	result.EnqueuePerformed = autoEnqueue.Enqueued > 0
	if err != nil {
		return s.record(ctx, result, err)
	}
	return s.record(ctx, result, nil)
}

func activationTaskIDs(followups followup.Result, signals triage.Result) []int64 {
	seen := make(map[int64]bool)
	var out []int64
	for _, id := range followups.CreatedTaskIDs {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, ids := range [][]int64{signals.CreatedTaskIDs, signals.LinkedTaskIDs} {
		for _, id := range ids {
			if id == 0 || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (s *Service) autoEnqueueProposed(ctx context.Context, taskIDs []int64) (AutoEnqueueResult, error) {
	var result AutoEnqueueResult
	if !s.autoEnqueue.Enabled {
		return result, nil
	}
	if s.autoEnqueueQueue == nil {
		return result, errors.New("activation: auto enqueue enabled without enqueue service")
	}
	limit := s.autoEnqueue.Limit
	if limit <= 0 || limit > len(taskIDs) {
		limit = len(taskIDs)
	}
	maxRisk := strings.ToLower(strings.TrimSpace(s.autoEnqueue.MaxRiskLevel))
	if maxRisk == "" {
		maxRisk = agentic.RiskLevelLow
	}
	operatorID := strings.TrimSpace(s.autoEnqueue.OperatorID)
	if operatorID == "" {
		operatorID = "agentic-activation"
	}
	reason := strings.TrimSpace(s.autoEnqueue.Reason)
	if reason == "" {
		reason = "agentic activation auto enqueue"
	}
	for _, taskID := range taskIDs[:limit] {
		result.Considered++
		task, err := s.store.GetAgenticTask(ctx, taskID)
		if err != nil {
			result.Failed++
			return result, fmt.Errorf("activation: load proposed task %d for auto enqueue: %w", taskID, err)
		}
		if task.Status != agentic.TaskStatusProposed || task.QueueTaskID != 0 || !riskAllowed(task.RiskLevel, maxRisk) {
			result.Skipped++
			continue
		}
		enqueued, err := s.autoEnqueueQueue.Enqueue(ctx, agenticenqueue.Request{
			TaskID:                  task.ID,
			OperatorID:              operatorID,
			Reason:                  reason,
			RequestedEnforcement:    s.autoEnqueue.RequestedEnforcement,
			RequestedCompletionGate: s.autoEnqueue.RequestedCompletionGate,
		})
		if err != nil {
			result.Failed++
			return result, err
		}
		if enqueued == nil {
			result.Failed++
			return result, errors.New("activation: auto enqueue returned nil result")
		}
		result.Enqueued++
		result.EnqueuedTaskIDs = append(result.EnqueuedTaskIDs, task.ID)
		result.QueueTaskIDs = append(result.QueueTaskIDs, enqueued.QueueTaskID)
	}
	return result, nil
}

func riskAllowed(taskRisk, maxRisk string) bool {
	taskRank, ok := riskRank(strings.ToLower(strings.TrimSpace(taskRisk)))
	if !ok {
		return false
	}
	maxRank, ok := riskRank(maxRisk)
	if !ok {
		maxRank = 0
	}
	return taskRank <= maxRank
}

func riskRank(risk string) (int, bool) {
	switch risk {
	case "", agentic.RiskLevelLow:
		return 0, true
	case agentic.RiskLevelMedium:
		return 1, true
	case agentic.RiskLevelHigh:
		return 2, true
	case agentic.RiskLevelCritical:
		return 3, true
	default:
		return 0, false
	}
}

func (s *Service) record(ctx context.Context, result Result, runErr error) (Result, error) {
	if runErr != nil {
		result.Status = agentic.ActivationRunStatusFailed
		result.Reason = runErr.Error()
	}
	run, recordErr := s.store.CreateActivationRun(ctx, agentic.ActivationRun{
		ExecutionPolicy:   result.ExecutionPolicy,
		Limit:             result.Limit,
		FollowupProcessed: result.Followups.Processed,
		FollowupCreated:   result.Followups.Created,
		FollowupSkipped:   result.Followups.Skipped,
		FollowupFailed:    result.Followups.Failed,
		SignalProcessed:   result.Signals.Processed,
		SignalCreated:     result.Signals.Created,
		SignalLinked:      result.Signals.Linked,
		SignalFailed:      result.Signals.Failed,
		EnqueuePerformed:  result.EnqueuePerformed,
		ProposedTaskIDs:   result.ProposedTaskIDs,
		Status:            result.Status,
		Reason:            result.Reason,
	})
	if recordErr != nil {
		if runErr != nil {
			return result, errors.Join(runErr, recordErr)
		}
		return result, recordErr
	}
	result.RunID = run.ID
	result.CreatedAt = run.CreatedAt
	if runErr != nil {
		return result, runErr
	}
	return result, nil
}
