package main

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/orchestrator"
)

type completionContractSummary struct {
	VerificationHint     bool
	VerificationObserved *bool
	CompletionWarning    string
	ReasoningEffort      string
	ReasoningEffortMode  string
	RetryDecision        string
	RetryReason          string
}

const (
	completionRetryDecisionRunVerification   = "run_verification"
	completionRetryDecisionRetrySmallerScope = "retry_smaller_scope"
)

var verificationCommandRE = regexp.MustCompile(`(?i)(^|[;&|()\s])((go\s+test|go\s+vet|git\s+diff\s+--check|bash\s+-n|make\s+(test|lint|vet)|npm\s+(test|run\s+test|run\s+lint)|pnpm\s+(test|run\s+test|run\s+lint)|yarn\s+(test|run\s+test|run\s+lint)|bun\s+test|pytest|python\d*(\.\d+)?\s+-m\s+pytest|ruff\s+check|cargo\s+test|mvn\s+test|gradle\s+test))([;&|()\s]|$)`)

func summarizeCompletionContract(routeCtx *orchestrator.RoutingContext, cfg orchestrator.WorkflowConfig, result *orchestrator.WorkflowResult) completionContractSummary {
	summary := completionContractSummary{
		ReasoningEffort:     strings.TrimSpace(cfg.ReasoningEffort),
		ReasoningEffortMode: strings.TrimSpace(cfg.ReasoningEffortMode),
	}
	if routeCtx != nil {
		summary.VerificationHint = routeCtx.VerificationHint
	}
	if result == nil {
		return summary
	}

	observed := verificationObservedInMessages(result.Messages)
	if summary.VerificationHint || observed {
		summary.VerificationObserved = &observed
	}
	if finalAssistantReportsIncomplete(result.Messages) {
		summary.CompletionWarning = "final_response_reports_incomplete"
	}
	summary.RetryDecision, summary.RetryReason = completionRetryPlan(summary)
	return summary
}

func completionRetryPlan(summary completionContractSummary) (string, string) {
	if summary.CompletionWarning == "final_response_reports_incomplete" {
		return completionRetryDecisionRetrySmallerScope, summary.CompletionWarning
	}
	if summary.VerificationObserved != nil && !*summary.VerificationObserved {
		return completionRetryDecisionRunVerification, "verification_hint_not_observed"
	}
	return "", ""
}

func verificationObservedInMessages(messages []llm.Message) bool {
	for _, msg := range messages {
		for _, block := range msg.Content {
			toolUse, ok := block.(llm.ToolUseBlock)
			if !ok {
				continue
			}
			if toolUse.Name != "bash" {
				continue
			}
			if isVerificationCommand(bashCommandFromToolInput(toolUse.Input)) {
				return true
			}
		}
	}
	return false
}

func bashCommandFromToolInput(input json.RawMessage) string {
	var payload struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return ""
	}
	return payload.Command
}

func isVerificationCommand(command string) bool {
	return verificationCommandRE.MatchString(command)
}

func finalAssistantReportsIncomplete(messages []llm.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleAssistant {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(messages[i].Text()))
		if text == "" {
			return false
		}
		for _, marker := range []string{
			"could not finish",
			"couldn't finish",
			"did not finish",
			"didn't finish",
			"not complete",
			"incomplete",
			"still need",
			"unable to complete",
			"완료하지 못",
			"아직 완료",
			"아직 남",
		} {
			if strings.Contains(text, marker) {
				return true
			}
		}
		return false
	}
	return false
}
