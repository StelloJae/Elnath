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
	VerificationCommand  string
	CompletionWarning    string
	EditIntent           bool
	EditObserved         *bool
	ReasoningEffort      string
	ReasoningEffortMode  string
	ProviderName         string
	ProviderEffort       string
	ProviderEffortNote   string
	CorrectionAttempted  bool
	CorrectionAttempts   int
	CorrectionDecision   string
	CorrectionReason     string
	RetryDecision        string
	RetryReason          string
}

const (
	completionRetryDecisionRunVerification   = "run_verification"
	completionRetryDecisionRetrySmallerScope = "retry_smaller_scope"
)

var verificationCommandRE = regexp.MustCompile(`(?i)(^|[;&|()\s])((go\s+test|go\s+vet|git\s+diff\s+--check|bash\s+-n|make\s+(test|lint|vet)|npm\s+(test|run\s+test|run\s+lint)|pnpm\s+(test|run\s+test|run\s+lint)|yarn\s+(test|run\s+test|run\s+lint)|bun\s+test|pytest|python\d*(\.\d+)?\s+-m\s+pytest|ruff\s+check|cargo\s+test|mvn\s+test|gradle\s+test))([;&|()\s]|$)`)

var mutatingBashCommandRE = regexp.MustCompile(`(?i)(^|[;&|()\s])((apply_patch|gofmt\s+-w|sed\s+-i|perl\s+-pi|tee\s+|touch\s+|mkdir\s+|rm\s+|mv\s+|cp\s+|cat\s+>|python\d*(\.\d+)?\s+(-c|-)\b))`)

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

	verificationCommand := observedVerificationCommand(result.Messages)
	observed := verificationCommand != ""
	if summary.VerificationHint || observed {
		summary.VerificationObserved = &observed
	}
	summary.VerificationCommand = verificationCommand
	editIntent := editIntentDetected(result.Messages)
	editObserved := mutationObservedInMessages(result.Messages)
	summary.EditIntent = editIntent
	if editIntent || editObserved {
		summary.EditObserved = &editObserved
	}
	if finalAssistantReportsIncomplete(result.Messages) {
		summary.CompletionWarning = "final_response_reports_incomplete"
	}
	if summary.CompletionWarning == "" && editIntent && !editObserved {
		summary.CompletionWarning = "edit_intent_without_mutation"
	}
	summary.RetryDecision, summary.RetryReason = completionRetryPlan(summary)
	return summary
}

func withProviderCapabilities(summary completionContractSummary, provider llm.Provider) completionContractSummary {
	caps := llm.CapabilitiesOf(provider)
	summary.ProviderName = caps.Name
	summary.ProviderEffort = caps.ReasoningEffort
	summary.ProviderEffortNote = caps.ReasoningEffortFallback
	return summary
}

func completionRetryPlan(summary completionContractSummary) (string, string) {
	if summary.CompletionWarning == "final_response_reports_incomplete" {
		return completionRetryDecisionRetrySmallerScope, summary.CompletionWarning
	}
	if summary.CompletionWarning == "edit_intent_without_mutation" {
		return completionRetryDecisionRetrySmallerScope, summary.CompletionWarning
	}
	if summary.VerificationObserved != nil && !*summary.VerificationObserved {
		return completionRetryDecisionRunVerification, "verification_hint_not_observed"
	}
	return "", ""
}

func verificationObservedInMessages(messages []llm.Message) bool {
	return observedVerificationCommand(messages) != ""
}

func observedVerificationCommand(messages []llm.Message) string {
	for _, msg := range messages {
		for _, block := range msg.Content {
			toolUse, ok := block.(llm.ToolUseBlock)
			if !ok {
				continue
			}
			if toolUse.Name != "bash" {
				continue
			}
			command := bashCommandFromToolInput(toolUse.Input)
			if isVerificationCommand(command) {
				return strings.TrimSpace(command)
			}
		}
	}
	return ""
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

func editIntentDetected(messages []llm.Message) bool {
	text := strings.ToLower(strings.TrimSpace(userMessageText(messages)))
	if text == "" {
		return false
	}
	return completionContainsAny(text, []string{
		"fix",
		"repair",
		"implement",
		"change",
		"modify",
		"update",
		"refactor",
		"patch",
		"write",
		"edit",
		"수정",
		"고쳐",
		"구현",
		"변경",
		"패치",
		"리팩터",
	})
}

func mutationObservedInMessages(messages []llm.Message) bool {
	for _, msg := range messages {
		for _, block := range msg.Content {
			toolUse, ok := block.(llm.ToolUseBlock)
			if !ok {
				continue
			}
			if mutatingToolUseObserved(toolUse) {
				return true
			}
		}
	}
	return false
}

func mutatingToolUseObserved(toolUse llm.ToolUseBlock) bool {
	switch toolUse.Name {
	case "write_file", "edit_file", "wiki_write":
		return true
	case "git":
		var payload struct {
			Subcommand string `json:"subcommand"`
		}
		if err := json.Unmarshal(toolUse.Input, &payload); err != nil {
			return false
		}
		return payload.Subcommand == "commit"
	case "bash":
		return bashCommandLooksMutating(bashCommandFromToolInput(toolUse.Input))
	default:
		return false
	}
}

func bashCommandLooksMutating(command string) bool {
	return mutatingBashCommandRE.MatchString(command)
}

func userMessageText(messages []llm.Message) string {
	var parts []string
	for _, msg := range messages {
		if msg.Role != llm.RoleUser {
			continue
		}
		if text := strings.TrimSpace(msg.Text()); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func completionContainsAny(s string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
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
