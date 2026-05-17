package activation

import (
	"context"
	"errors"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/agentic/followup"
	"github.com/stello/elnath/internal/agentic/triage"
)

const ExecutionPolicyProposeOnly = "propose_only"

type Result struct {
	RunID            int64
	Limit            int
	ExecutionPolicy  string
	EnqueuePerformed bool
	Status           string
	Reason           string
	CreatedAt        time.Time
	Followups        followup.Result
	Signals          triage.Result
}

type Service struct {
	store *agentic.Store
}

func NewService(store *agentic.Store) *Service {
	return &Service{store: store}
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
	followups, err := followup.NewScheduler(s.store).RunOnce(ctx, limit)
	if err != nil {
		result.Followups = followups
		return s.record(ctx, result, err)
	}
	result.Followups = followups
	signals, err := triage.NewTriager(s.store).RunOnce(ctx, limit)
	if err != nil {
		result.Signals = signals
		return s.record(ctx, result, err)
	}
	result.Signals = signals
	return s.record(ctx, result, nil)
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
