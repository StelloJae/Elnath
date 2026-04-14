package orchestrator

import (
	"log/slog"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/learning"
)

func applyAgentLearning(deps *LearningDeps, info learning.AgentResultInfo) {
	if deps == nil || deps.Store == nil {
		return
	}

	lessons := learning.ExtractAgent(info)
	if len(lessons) == 0 {
		return
	}

	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}

	personaChanged := false
	for _, lesson := range lessons {
		if err := deps.Store.Append(lesson); err != nil {
			log.Warn("agent learning: append failed", "error", err)
			continue
		}
		if deps.SelfState != nil && len(lesson.PersonaDelta) > 0 {
			deps.SelfState.ApplyLessons(lesson.PersonaDelta)
			personaChanged = true
		}
	}

	if personaChanged && deps.SelfState != nil {
		if err := deps.SelfState.Save(); err != nil {
			log.Warn("agent learning: selfState save failed", "error", err)
		}
	}
}

func firstMessageSnippet(msg string, n int) string {
	msg = strings.TrimSpace(msg)
	if msg == "" || n <= 0 {
		return ""
	}
	runes := []rune(msg)
	if len(runes) <= n {
		return msg
	}
	return strings.TrimSpace(string(runes[:n]))
}

func toAgentToolStats(src []agent.ToolStat) []learning.AgentToolStat {
	out := make([]learning.AgentToolStat, 0, len(src))
	for _, stat := range src {
		out = append(out, learning.AgentToolStat{
			Name:      stat.Name,
			Calls:     stat.Calls,
			Errors:    stat.Errors,
			TotalTime: stat.TotalTime,
		})
	}
	return out
}

func toWorkflowToolStats(src []learning.AgentToolStat) []agent.ToolStat {
	out := make([]agent.ToolStat, 0, len(src))
	for _, stat := range src {
		out = append(out, agent.ToolStat{
			Name:      stat.Name,
			Calls:     stat.Calls,
			Errors:    stat.Errors,
			TotalTime: stat.TotalTime,
		})
	}
	return out
}

// aggregateFinishReason picks the most informative reason across sub-runs.
// Precedence: budget_exceeded > error > ack_loop > stop.
func aggregateFinishReason(reasons []string) string {
	priority := map[string]int{
		"budget_exceeded": 4,
		"error":           3,
		"ack_loop":        2,
		"stop":            1,
		"":                0,
	}
	best := ""
	for _, reason := range reasons {
		if priority[reason] > priority[best] {
			best = reason
		}
	}
	return best
}
