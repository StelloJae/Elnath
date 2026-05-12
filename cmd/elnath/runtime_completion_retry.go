package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/orchestrator"
	"github.com/stello/elnath/internal/tools"
)

func (rt *executionRuntime) maybeRunCompletionRetry(
	ctx context.Context,
	wf orchestrator.Workflow,
	input orchestrator.WorkflowInput,
	result *orchestrator.WorkflowResult,
	summary completionContractSummary,
) (*orchestrator.WorkflowResult, completionContractSummary) {
	if rt == nil || rt.completionRetryMax <= 0 || result == nil {
		return result, summary
	}
	switch summary.RetryDecision {
	case completionRetryDecisionRetrySmallerScope:
		if wf == nil {
			return result, summary
		}
		return rt.runSmallerScopeCompletionRetry(ctx, wf, input, result, summary)
	case completionRetryDecisionRunVerification:
		return rt.runVerificationCompletionRetry(ctx, input, result, summary)
	default:
		return result, summary
	}
}

func (rt *executionRuntime) runSmallerScopeCompletionRetry(
	ctx context.Context,
	wf orchestrator.Workflow,
	input orchestrator.WorkflowInput,
	result *orchestrator.WorkflowResult,
	summary completionContractSummary,
) (*orchestrator.WorkflowResult, completionContractSummary) {
	retryInput := input
	retryInput.Messages = result.Messages
	retryInput.Message = completionRetryPrompt(summary)
	retryEffort, retryEffortReason := completionRetryEscalatedEffort(rt.provider, summary)
	if retryEffort != "" {
		retryInput.Config.ReasoningEffort = retryEffort
		retryInput.Config.ReasoningEffortMode = "manual"
	}
	retryResult, err := wf.Run(ctx, retryInput)
	if err != nil {
		rt.app.Logger.Warn("completion correction retry failed",
			"decision", summary.RetryDecision,
			"reason", summary.RetryReason,
			"error", err,
		)
		return result, completionCorrectionFailedSummary(summary, "workflow_error")
	}
	retrySummary := withProviderCapabilities(summarizeCompletionContract(completionRetryRoutingContext(summary), retryInput.Config, retryResult), rt.provider)
	retrySummary.CorrectionAttempted = true
	retrySummary.CorrectionAttempts = 1
	retrySummary.CorrectionDecision = summary.RetryDecision
	retrySummary.CorrectionReason = summary.RetryReason
	if retrySummary.CompletionWarning != "" {
		retrySummary.CorrectionStatus = "failed"
		retrySummary.CorrectionFailureFamily = "completion_warning_unresolved"
	} else {
		retrySummary.CorrectionStatus = "succeeded"
	}
	if retryEffortReason != "" {
		retrySummary.ReasoningEffort = retryEffort
		retrySummary.ReasoningEffortMode = "manual"
		retrySummary.ReasoningEffortReason = retryEffortReason
	}
	return retryResult, retrySummary
}

func completionRetryRoutingContext(summary completionContractSummary) *orchestrator.RoutingContext {
	if !summary.VerificationHint && summary.VerificationObserved == nil {
		return nil
	}
	return &orchestrator.RoutingContext{VerificationHint: true}
}

func completionCorrectionFailedSummary(summary completionContractSummary, failureFamily string) completionContractSummary {
	updated := summary
	updated.CorrectionAttempted = true
	updated.CorrectionAttempts = 1
	updated.CorrectionDecision = summary.RetryDecision
	updated.CorrectionReason = summary.RetryReason
	updated.CorrectionStatus = "failed"
	updated.CorrectionFailureFamily = failureFamily
	return updated
}

func completionCorrectionSkippedSummary(summary completionContractSummary, failureFamily string) completionContractSummary {
	updated := summary
	updated.CorrectionAttempted = false
	updated.CorrectionAttempts = 0
	updated.CorrectionDecision = summary.RetryDecision
	updated.CorrectionReason = summary.RetryReason
	updated.CorrectionStatus = "skipped"
	updated.CorrectionFailureFamily = failureFamily
	return updated
}

func completionRetryEscalatedEffort(provider llm.Provider, summary completionContractSummary) (string, string) {
	if strings.EqualFold(strings.TrimSpace(summary.ReasoningEffortMode), "manual") {
		return "", ""
	}
	switch llm.CapabilitiesOf(provider).ReasoningEffort {
	case llm.ReasoningEffortIgnored, llm.ReasoningEffortUnsupported, llm.ReasoningEffortThinkingBudgetOnly:
		return "", ""
	}
	switch strings.ToLower(strings.TrimSpace(summary.ReasoningEffort)) {
	case "xhigh":
		return "xhigh", "correction_retry_preserve_xhigh"
	case "high":
		return "xhigh", "correction_retry_escalation"
	default:
		return "high", "correction_retry_escalation"
	}
}

func (rt *executionRuntime) runVerificationCompletionRetry(
	ctx context.Context,
	input orchestrator.WorkflowInput,
	result *orchestrator.WorkflowResult,
	summary completionContractSummary,
) (*orchestrator.WorkflowResult, completionContractSummary) {
	command := explicitCompletionVerificationCommand(result.Messages)
	if command == "" {
		return result, completionCorrectionSkippedSummary(summary, "missing_explicit_verification_command")
	}
	exec := completionVerificationExecutor(input)
	if exec == nil {
		return result, completionCorrectionSkippedSummary(summary, "missing_verification_executor")
	}

	params, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		return result, summary
	}
	toolCtx := rt.toolContextForSession(ctx, input.Session)
	toolResult, err := exec.Execute(toolCtx, "bash", params)
	if err != nil {
		rt.app.Logger.Warn("completion verification correction failed",
			"decision", summary.RetryDecision,
			"reason", summary.RetryReason,
			"error", err,
		)
		return result, completionCorrectionFailedSummary(summary, "verification_executor_error")
	}
	if toolResult == nil || toolResult.IsError {
		rt.app.Logger.Warn("completion verification correction returned error",
			"decision", summary.RetryDecision,
			"reason", summary.RetryReason,
			"command", command,
		)
		return result, completionCorrectionFailedSummary(summary, "verification_command_failed")
	}

	observed := true
	updated := summary
	updated.VerificationObserved = &observed
	updated.VerificationCommand = command
	updated.CorrectionAttempted = true
	updated.CorrectionAttempts = 1
	updated.CorrectionDecision = summary.RetryDecision
	updated.CorrectionReason = summary.RetryReason
	updated.CorrectionStatus = "succeeded"
	updated.RetryDecision = ""
	updated.RetryReason = ""
	return result, updated
}

func completionRetryPrompt(summary completionContractSummary) string {
	return fmt.Sprintf(
		"Run one bounded correction pass for the previous task.\nRetry decision: %s\nRetry reason: %s\nKeep scope smaller than the original attempt. Provide a concrete completed result, or explicitly state what remains incomplete.",
		summary.RetryDecision,
		summary.RetryReason,
	)
}

func completionVerificationExecutor(input orchestrator.WorkflowInput) tools.Executor {
	if input.Config.ToolExecutor != nil {
		return input.Config.ToolExecutor
	}
	if input.Tools != nil {
		return input.Tools
	}
	return nil
}

func explicitCompletionVerificationCommand(messages []llm.Message) string {
	for _, msg := range messages {
		if msg.Role != llm.RoleUser {
			continue
		}
		for _, line := range strings.Split(msg.Text(), "\n") {
			command := normalizeExplicitVerificationLine(line)
			if command == "" {
				continue
			}
			if explicitVerificationCommandLine(command) && isVerificationCommand(command) {
				return command
			}
		}
	}
	return ""
}

func normalizeExplicitVerificationLine(line string) string {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"- ", "* "} {
		line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
	}
	line = strings.Trim(line, "`")
	return strings.TrimSpace(line)
}

func explicitVerificationCommandLine(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	prefixes := []string{
		"go test",
		"go vet",
		"git diff --check",
		"bash -n",
		"make test",
		"make lint",
		"make vet",
		"npm test",
		"npm run test",
		"npm run lint",
		"pnpm test",
		"pnpm run test",
		"pnpm run lint",
		"yarn test",
		"yarn run test",
		"yarn run lint",
		"bun test",
		"pytest",
		"python -m pytest",
		"python3 -m pytest",
		"ruff check",
		"cargo test",
		"mvn test",
		"gradle test",
	}
	for _, prefix := range prefixes {
		if lower == prefix || strings.HasPrefix(lower, prefix+" ") {
			return true
		}
	}
	return false
}
