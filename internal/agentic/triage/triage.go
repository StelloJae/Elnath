package triage

import (
	"context"

	"github.com/stello/elnath/internal/agentic"
)

type Result struct {
	Processed int
	Created   int
	Linked    int
	Failed    int
}

type Triager struct {
	store *agentic.Store
}

func NewTriager(store *agentic.Store) *Triager {
	return &Triager{store: store}
}

func (t *Triager) TriageSignal(ctx context.Context, signalID int64) (*agentic.AgenticTask, error) {
	result, err := t.store.TriageGoalSignal(ctx, signalID)
	if err != nil {
		return nil, err
	}
	if result.Failed {
		return nil, agentic.ErrSignalTriageFailed
	}
	return result.Task, nil
}

func (t *Triager) RunOnce(ctx context.Context, limit int) (Result, error) {
	signals, err := t.store.ListGoalSignalsByStatus(ctx, agentic.SignalStatusNew, limit)
	if err != nil {
		return Result{}, err
	}
	var result Result
	for _, signal := range signals {
		triaged, err := t.store.TriageGoalSignal(ctx, signal.ID)
		if err != nil {
			return result, err
		}
		result.Processed++
		if triaged.Failed {
			result.Failed++
			continue
		}
		if triaged.Created {
			result.Created++
		}
		if triaged.Linked {
			result.Linked++
		}
	}
	return result, nil
}
