package agent

import (
	"strings"

	"github.com/stello/elnath/internal/llm"
)

const (
	reasoningEffortModeAuto   = "auto"
	reasoningEffortModeManual = "manual"
)

func (a *Agent) resolveReasoningEffort(messages []llm.Message) string {
	mode := strings.ToLower(strings.TrimSpace(a.reasoningEffortMode))
	if mode == reasoningEffortModeAuto {
		if effort := autoReasoningEffort(messages); effort != "" {
			return effort
		}
		if effort := strings.TrimSpace(a.reasoningEffort); effort != "" {
			return effort
		}
		return "medium"
	}
	return strings.TrimSpace(a.reasoningEffort)
}

func autoReasoningEffort(messages []llm.Message) string {
	text := strings.ToLower(strings.TrimSpace(userTaskText(messages)))
	if text == "" {
		return ""
	}

	if containsAny(text, []string{
		"full benchmark",
		"baseline comparison",
		"root cause",
		"security",
		"threat model",
		"architecture",
		"race condition",
		"critical",
		"autonomous",
	}) {
		return "xhigh"
	}

	if len(text) > 600 || containsAny(text, []string{
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
		return "high"
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
		return "low"
	}

	return "medium"
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
