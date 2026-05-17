package activation

import (
	"context"
	"errors"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/agentic/followup"
	"github.com/stello/elnath/internal/agentic/triage"
)

const ExecutionPolicyProposeOnly = "propose_only"

type Result struct {
	Limit            int
	ExecutionPolicy  string
	EnqueuePerformed bool
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
	}
	followups, err := followup.NewScheduler(s.store).RunOnce(ctx, limit)
	if err != nil {
		result.Followups = followups
		return result, err
	}
	result.Followups = followups
	signals, err := triage.NewTriager(s.store).RunOnce(ctx, limit)
	if err != nil {
		result.Signals = signals
		return result, err
	}
	result.Signals = signals
	return result, nil
}
