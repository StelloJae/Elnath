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

const maxCompletionRetryAttempts = 2

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
	if summary.RetryDecision != "" {
		summary.CorrectionMaxAttempts = rt.completionRetryMax
	}
	currentResult := result
	currentSummary := summary
	attempts := 0
	for currentSummary.RetryDecision != "" && attempts < rt.completionRetryMax {
		attempts++
		currentSummary.CorrectionMaxAttempts = rt.completionRetryMax
		switch currentSummary.RetryDecision {
		case completionRetryDecisionRetrySmallerScope:
			if wf == nil {
				return currentResult, completionCorrectionSkippedSummary(currentSummary, "missing_retry_workflow", attempts)
			}
			currentResult, currentSummary = rt.runSmallerScopeCompletionRetry(ctx, wf, input, currentResult, currentSummary, attempts)
		case completionRetryDecisionRunVerification:
			currentResult, currentSummary = rt.runVerificationCompletionRetry(ctx, input, currentResult, currentSummary, attempts)
		default:
			return currentResult, currentSummary
		}
		if currentSummary.CorrectionStatus == "failed" || currentSummary.CorrectionStatus == "skipped" {
			return currentResult, currentSummary
		}
	}
	return currentResult, currentSummary
}

func (rt *executionRuntime) runSmallerScopeCompletionRetry(
	ctx context.Context,
	wf orchestrator.Workflow,
	input orchestrator.WorkflowInput,
	result *orchestrator.WorkflowResult,
	summary completionContractSummary,
	attempt int,
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
		return result, completionCorrectionFailedSummary(summary, "workflow_error", attempt)
	}
	retrySummary := withProviderCapabilities(summarizeCompletionContract(completionRetryRoutingContext(summary), retryInput.Config, retryResult), rt.provider)
	retrySummary.CorrectionAttempted = true
	retrySummary.CorrectionAttempts = attempt
	retrySummary.CorrectionMaxAttempts = summary.CorrectionMaxAttempts
	retrySummary.CorrectionDecision = summary.RetryDecision
	retrySummary.CorrectionReason = summary.RetryReason
	if retrySummary.CompletionWarning != "" {
		if completionWarningFailsClosed(retrySummary.CompletionWarning) {
			retrySummary.CorrectionStatus = "failed"
			retrySummary.CorrectionFailureFamily = retrySummary.CompletionWarning
			retrySummary.RetryDecision = ""
			retrySummary.RetryReason = ""
		} else if attempt >= summary.CorrectionMaxAttempts {
			retrySummary.CorrectionStatus = "failed"
			retrySummary.CorrectionFailureFamily = "completion_warning_unresolved"
		} else {
			retrySummary.CorrectionStatus = "retrying"
		}
	} else {
		retrySummary.CorrectionStatus = "succeeded"
	}
	if retryEffortReason != "" {
		retrySummary.ReasoningEffort = retryEffort
		retrySummary.ReasoningEffortMode = "manual"
		retrySummary.ReasoningEffortReason = retryEffortReason
	}
	retrySummary.CorrectionAttemptDetails = appendCompletionCorrectionAttemptDetail(summary, completionCorrectionAttemptReceipt{
		Attempt:           attempt,
		Decision:          summary.RetryDecision,
		Reason:            summary.RetryReason,
		Status:            retrySummary.CorrectionStatus,
		FailureFamily:     retrySummary.CorrectionFailureFamily,
		CompletionWarning: retrySummary.CompletionWarning,
		OutOfScopeFiles:   append([]string(nil), retrySummary.OutOfScopeChangedFiles...),
	})
	return retryResult, retrySummary
}

func completionWarningFailsClosed(warning string) bool {
	return warning == "scope_drift"
}

func completionRetryRoutingContext(summary completionContractSummary) *orchestrator.RoutingContext {
	if !summary.VerificationHint && summary.VerificationObserved == nil {
		return nil
	}
	return &orchestrator.RoutingContext{VerificationHint: true}
}

func completionCorrectionFailedSummary(summary completionContractSummary, failureFamily string, attempt int) completionContractSummary {
	updated := summary
	updated.CorrectionAttempted = true
	updated.CorrectionAttempts = attempt
	updated.CorrectionMaxAttempts = summary.CorrectionMaxAttempts
	updated.CorrectionDecision = summary.RetryDecision
	updated.CorrectionReason = summary.RetryReason
	updated.CorrectionStatus = "failed"
	updated.CorrectionFailureFamily = failureFamily
	updated.CorrectionAttemptDetails = appendCompletionCorrectionAttemptDetail(summary, completionCorrectionAttemptReceipt{
		Attempt:       attempt,
		Decision:      summary.RetryDecision,
		Reason:        summary.RetryReason,
		Status:        updated.CorrectionStatus,
		FailureFamily: updated.CorrectionFailureFamily,
	})
	return updated
}

func completionCorrectionSkippedSummary(summary completionContractSummary, failureFamily string, attempt int) completionContractSummary {
	updated := summary
	updated.CorrectionAttempted = summary.CorrectionAttempted
	updated.CorrectionAttempts = summary.CorrectionAttempts
	updated.CorrectionMaxAttempts = summary.CorrectionMaxAttempts
	updated.CorrectionDecision = summary.RetryDecision
	updated.CorrectionReason = summary.RetryReason
	updated.CorrectionStatus = "skipped"
	updated.CorrectionFailureFamily = failureFamily
	updated.CorrectionAttemptDetails = appendCompletionCorrectionAttemptDetail(summary, completionCorrectionAttemptReceipt{
		Attempt:             attempt,
		Decision:            summary.RetryDecision,
		Reason:              summary.RetryReason,
		Status:              updated.CorrectionStatus,
		FailureFamily:       updated.CorrectionFailureFamily,
		VerificationCommand: summary.VerificationCommand,
	})
	return updated
}

func appendCompletionCorrectionAttemptDetail(summary completionContractSummary, detail completionCorrectionAttemptReceipt) []completionCorrectionAttemptReceipt {
	details := append([]completionCorrectionAttemptReceipt(nil), summary.CorrectionAttemptDetails...)
	if detail.Attempt <= 0 {
		return details
	}
	details = append(details, detail)
	return details
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
	attempt int,
) (*orchestrator.WorkflowResult, completionContractSummary) {
	command := explicitCompletionVerificationCommand(result.Messages)
	if command == "" {
		return result, completionCorrectionSkippedSummary(summary, "missing_explicit_verification_command", attempt)
	}
	summary = completionVerificationObservedSummary(summary, command)
	exec := completionVerificationExecutor(input)
	if exec == nil {
		return result, completionCorrectionSkippedSummary(summary, "missing_verification_executor", attempt)
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
		return result, completionCorrectionFailedSummary(summary, "verification_executor_error", attempt)
	}
	if toolResult == nil || toolResult.IsError {
		rt.app.Logger.Warn("completion verification correction returned error",
			"decision", summary.RetryDecision,
			"reason", summary.RetryReason,
			"command", command,
		)
		return result, completionCorrectionFailedSummary(summary, "verification_command_failed", attempt)
	}

	updated := summary
	updated.CorrectionAttempted = true
	updated.CorrectionAttempts = attempt
	updated.CorrectionMaxAttempts = summary.CorrectionMaxAttempts
	updated.CorrectionDecision = summary.RetryDecision
	updated.CorrectionReason = summary.RetryReason
	updated.CorrectionStatus = "succeeded"
	updated.CorrectionAttemptDetails = appendCompletionCorrectionAttemptDetail(summary, completionCorrectionAttemptReceipt{
		Attempt:             attempt,
		Decision:            summary.RetryDecision,
		Reason:              summary.RetryReason,
		Status:              updated.CorrectionStatus,
		VerificationCommand: command,
	})
	updated.RetryDecision = ""
	updated.RetryReason = ""
	return result, updated
}

func completionVerificationObservedSummary(summary completionContractSummary, command string) completionContractSummary {
	observed := true
	updated := summary
	updated.VerificationObserved = &observed
	updated.VerificationCommand = command
	return updated
}

func completionRetryPrompt(summary completionContractSummary) string {
	reasonGuidance := completionRetryReasonGuidance(summary)
	if reasonGuidance != "" {
		reasonGuidance = "\nReason-specific guidance:\n" + reasonGuidance
	}
	scopeGuidance := completionRetryScopeGuidance(summary)
	if scopeGuidance != "" {
		scopeGuidance = "\nScope lock:\n" + scopeGuidance
	}
	return fmt.Sprintf(
		"Run one bounded correction pass for the previous task.\nRetry decision: %s\nRetry reason: %s\nKeep scope smaller than the original attempt. Provide a concrete completed result, or explicitly state what remains incomplete.%s%s",
		summary.RetryDecision,
		summary.RetryReason,
		reasonGuidance,
		scopeGuidance,
	)
}

func completionRetryScopeGuidance(summary completionContractSummary) string {
	var parts []string
	if summary.RecoveryScopeLabel != "" {
		parts = append(parts, "- Scope label: "+summary.RecoveryScopeLabel)
	}
	if len(summary.AllowedRecoveryPaths) > 0 {
		parts = append(parts, "- Allowed recovery paths: "+strings.Join(summary.AllowedRecoveryPaths, ", "))
	}
	if len(summary.ForbiddenRecoveryPaths) > 0 {
		parts = append(parts, "- Forbidden recovery paths: "+strings.Join(summary.ForbiddenRecoveryPaths, ", "))
	}
	if len(summary.AllowedRecoveryPaths) > 0 || len(summary.ForbiddenRecoveryPaths) > 0 {
		parts = append(parts, "- If the root cause appears outside this scope, stop and report scope_drift instead of editing unrelated files.")
	}
	return strings.Join(parts, "\n")
}

func completionRetryReasonGuidance(summary completionContractSummary) string {
	switch summary.RetryReason {
	case "edit_intent_without_mutation":
		return "- Previous attempt intended to edit but left no accepted mutation.\n- Do not spend the whole retry re-reading broad context.\n- Make the smallest concrete file edit that satisfies the task before the final answer.\n- If editing is impossible, state the exact blocker instead of claiming completion."
	case "budget_exceeded_after_edit_intent":
		return "- Previous attempt reached budget after edit intent.\n- Resume from the already identified seam; do not restart broad investigation.\n- Complete the smallest concrete patch and run the configured verification before claiming completion."
	case "verification_command_failed":
		return "- Previous verification failed.\n- Inspect the failure, patch only the smallest root cause, and rerun the same verification command."
	case "unsupported_verification_success_claim":
		return "- Previous answer claimed verification without an observed command.\n- Run an explicit verification command, or remove the success claim and report the blocker."
	case "final_response_reports_incomplete":
		return "- Previous final answer self-reported incomplete work.\n- Finish only the smallest missing slice, then verify or state the exact remaining blocker."
	default:
		return ""
	}
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
