package main

import (
	"context"

	agenticcompletion "github.com/stello/elnath/internal/agentic/completion"
	"github.com/stello/elnath/internal/daemon"
)

func (rt *executionRuntime) rememberAgenticCompletionContext(agenticTaskID int64, summary completionContractSummary) {
	if rt == nil || agenticTaskID == 0 {
		return
	}
	rt.completionCtxMu.Lock()
	defer rt.completionCtxMu.Unlock()
	if rt.completionCtxs == nil {
		rt.completionCtxs = make(map[int64]completionContractSummary)
	}
	rt.completionCtxs[agenticTaskID] = summary
}

func (rt *executionRuntime) CompletionContext(_ context.Context, _ daemon.Task, agenticTaskID int64) (agenticcompletion.CompletionContext, error) {
	if rt == nil || agenticTaskID == 0 {
		return agenticcompletion.CompletionContext{}, nil
	}
	rt.completionCtxMu.Lock()
	summary, ok := rt.completionCtxs[agenticTaskID]
	if ok {
		delete(rt.completionCtxs, agenticTaskID)
	}
	rt.completionCtxMu.Unlock()
	if !ok {
		return agenticcompletion.CompletionContext{}, nil
	}
	return agenticcompletion.CompletionContext{
		VerificationHint:     summary.VerificationHint,
		VerificationObserved: summary.VerificationObserved,
		VerificationCommand:  summary.VerificationCommand,
		CompletionWarning:    summary.CompletionWarning,
		ReasoningEffort:      summary.ReasoningEffort,
		ReasoningEffortMode:  summary.ReasoningEffortMode,
		RetryDecision:        summary.RetryDecision,
		RetryReason:          summary.RetryReason,
	}, nil
}
