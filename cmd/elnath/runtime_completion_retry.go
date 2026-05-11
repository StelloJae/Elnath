package main

import (
	"context"
	"fmt"

	"github.com/stello/elnath/internal/orchestrator"
)

func (rt *executionRuntime) maybeRunCompletionRetry(
	ctx context.Context,
	wf orchestrator.Workflow,
	input orchestrator.WorkflowInput,
	result *orchestrator.WorkflowResult,
	summary completionContractSummary,
) (*orchestrator.WorkflowResult, completionContractSummary) {
	if rt == nil || rt.completionRetryMax <= 0 || wf == nil || result == nil {
		return result, summary
	}
	if summary.RetryDecision != completionRetryDecisionRetrySmallerScope {
		return result, summary
	}

	retryInput := input
	retryInput.Messages = result.Messages
	retryInput.Message = completionRetryPrompt(summary)
	retryResult, err := wf.Run(ctx, retryInput)
	if err != nil {
		rt.app.Logger.Warn("completion correction retry failed",
			"decision", summary.RetryDecision,
			"reason", summary.RetryReason,
			"error", err,
		)
		return result, summary
	}
	retrySummary := withProviderCapabilities(summarizeCompletionContract(nil, retryInput.Config, retryResult), rt.provider)
	retrySummary.CorrectionAttempted = true
	retrySummary.CorrectionAttempts = 1
	retrySummary.CorrectionDecision = summary.RetryDecision
	retrySummary.CorrectionReason = summary.RetryReason
	return retryResult, retrySummary
}

func completionRetryPrompt(summary completionContractSummary) string {
	return fmt.Sprintf(
		"Run one bounded correction pass for the previous task.\nRetry decision: %s\nRetry reason: %s\nKeep scope smaller than the original attempt. Provide a concrete completed result, or explicitly state what remains incomplete.",
		summary.RetryDecision,
		summary.RetryReason,
	)
}
