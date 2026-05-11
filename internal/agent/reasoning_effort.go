package agent

import (
	"strings"

	"github.com/stello/elnath/internal/llm"
)

const (
	reasoningEffortModeAuto   = "auto"
	reasoningEffortModeManual = "manual"
)

type reasoningEffortDecision struct {
	Effort string
	Mode   string
	Reason string
}

func (a *Agent) resolveReasoningEffort(messages []llm.Message) string {
	return a.resolveReasoningEffortDecision(messages).Effort
}

func (a *Agent) resolveReasoningEffortDecision(messages []llm.Message) reasoningEffortDecision {
	mode := strings.ToLower(strings.TrimSpace(a.reasoningEffortMode))
	if mode == reasoningEffortModeAuto {
		if decision := autoReasoningEffortDecision(messages); decision.Effort != "" {
			if unsupported := providerUnsupportedAutoEffort(a.provider); unsupported != "" {
				return reasoningEffortDecision{Mode: reasoningEffortModeAuto, Reason: unsupported}
			}
			decision.Mode = reasoningEffortModeAuto
			return decision
		}
		if effort := strings.TrimSpace(a.reasoningEffort); effort != "" {
			if unsupported := providerUnsupportedAutoEffort(a.provider); unsupported != "" {
				return reasoningEffortDecision{Mode: reasoningEffortModeAuto, Reason: unsupported}
			}
			return reasoningEffortDecision{Effort: effort, Mode: reasoningEffortModeAuto, Reason: "configured_fallback"}
		}
		return reasoningEffortDecision{Effort: "medium", Mode: reasoningEffortModeAuto, Reason: "empty_task_default"}
	}
	return reasoningEffortDecision{Effort: strings.TrimSpace(a.reasoningEffort), Mode: reasoningEffortModeManual, Reason: "manual"}
}

func providerUnsupportedAutoEffort(provider llm.Provider) string {
	switch llm.CapabilitiesOf(provider).ReasoningEffort {
	case llm.ReasoningEffortIgnored:
		return "provider_effort_ignored"
	case llm.ReasoningEffortUnsupported:
		return "provider_effort_unsupported"
	case llm.ReasoningEffortThinkingBudgetOnly:
		return "provider_effort_thinking_budget_only"
	default:
		return ""
	}
}

func autoReasoningEffort(messages []llm.Message) string {
	return autoReasoningEffortDecision(messages).Effort
}

func autoReasoningEffortDecision(messages []llm.Message) reasoningEffortDecision {
	text := strings.ToLower(strings.TrimSpace(userTaskText(messages)))
	if text == "" {
		return reasoningEffortDecision{Reason: "empty_task"}
	}

	if containsAny(text, []string{
		"root cause",
		"security",
		"threat model",
		"architecture",
		"race condition",
		"critical",
		"autonomous",
	}) {
		return reasoningEffortDecision{Effort: "xhigh", Reason: "critical_keyword"}
	}

	if len(text) > 600 {
		return reasoningEffortDecision{Effort: "high", Reason: "long_task"}
	}
	if containsAny(text, []string{
		"implement",
		"refactor",
		"debug",
		"repair",
		"fix",
		"benchmark",
		"v8",
		"pull request",
		"merge",
		"ci",
		"test",
		"daemon",
		"provider",
		"policy",
		"구현",
		"수정",
		"고쳐",
		"디버그",
		"벤치마크",
		"비교",
		"머지",
		"테스트",
		"자율",
	}) {
		return reasoningEffortDecision{Effort: "high", Reason: "work_keyword"}
	}

	if len(text) <= 160 && containsAny(text, []string{
		"what",
		"when",
		"where",
		"who",
		"translate",
		"summarize",
		"status",
		"time",
		"date",
		"간단",
		"번역",
		"요약",
		"상태",
	}) {
		return reasoningEffortDecision{Effort: "low", Reason: "simple_keyword"}
	}

	return reasoningEffortDecision{Effort: "medium", Reason: "default_medium"}
}

func userTaskText(messages []llm.Message) string {
	var parts []string
	for _, msg := range messages {
		if msg.Role == llm.RoleUser {
			if text := strings.TrimSpace(msg.Text()); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func containsAny(s string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
