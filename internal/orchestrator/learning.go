package orchestrator

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/self"
)

const llmExtractTimeout = 30 * time.Second

func applyAgentLearning(deps *LearningDeps, info learning.AgentResultInfo) {
	if deps == nil || deps.Store == nil {
		return
	}
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}

	ruleLessons := learning.ExtractAgent(info)
	ruleChanged := appendAndApply(deps, log, ruleLessons)
	if ruleChanged && deps.SelfState != nil {
		if err := deps.SelfState.Save(); err != nil {
			log.Warn("agent learning: selfState save (rule) failed", "error", err)
		}
	}

	if deps.LLMExtractor == nil {
		return
	}
	if !deps.ComplexityGate.ShouldExtract(deps.MessageCount, deps.ToolCallCount) {
		return
	}
	if deps.FailCounter != nil && !deps.FailCounter.Allow() {
		log.Debug("llm lesson: fail counter open, skip", "session_id", deps.SessionID)
		return
	}

	since := 0
	if deps.CursorStore != nil {
		since, _ = deps.CursorStore.Get(deps.SessionID)
	}

	summary := ""
	lastLine := 0
	if deps.CompactSummary != nil {
		summary, lastLine = deps.CompactSummary()
	}

	req := learning.ExtractRequest{
		SessionID:       deps.SessionID,
		Topic:           info.Topic,
		Workflow:        info.Workflow,
		CompactSummary:  summary,
		ToolStats:       info.ToolStats,
		FinishReason:    info.FinishReason,
		Iterations:      info.Iterations,
		MaxIterations:   info.MaxIterations,
		RetryCount:      info.RetryCount,
		ExistingLessons: buildLessonManifest(deps.Store, 50),
		SinceLine:       since,
	}

	ctx, cancel := context.WithTimeout(context.Background(), llmExtractTimeout)
	llmLessons, err := deps.LLMExtractor.Extract(ctx, req)
	cancel()

	if deps.FailCounter != nil {
		deps.FailCounter.Record(err)
	}
	if err != nil {
		log.Warn("llm lesson: extract failed", "error", err, "session_id", deps.SessionID)
		return
	}

	for i := range llmLessons {
		llmLessons[i].Source = llmSourceFor(info.Workflow)
		applyPersonaHint(&llmLessons[i])
	}
	llmChanged := appendAndApply(deps, log, llmLessons)
	if deps.CursorStore != nil && lastLine > 0 {
		if err := deps.CursorStore.Update(deps.SessionID, lastLine); err != nil {
			log.Warn("llm lesson: cursor update failed", "error", err)
		}
	}
	if llmChanged && deps.SelfState != nil {
		if err := deps.SelfState.Save(); err != nil {
			log.Warn("agent learning: selfState save (llm) failed", "error", err)
		}
	}
}

func appendAndApply(deps *LearningDeps, log *slog.Logger, lessons []learning.Lesson) bool {
	personaChanged := false
	for _, lesson := range lessons {
		added, err := deps.Store.AppendNew(lesson)
		if err != nil {
			log.Warn("agent learning: append failed", "error", err)
			continue
		}
		if !added {
			continue
		}
		if deps.SelfState != nil && len(lesson.PersonaDelta) > 0 {
			deps.SelfState.ApplyLessons(lesson.PersonaDelta)
			personaChanged = true
		}
	}
	return personaChanged
}

func buildLessonManifest(store *learning.Store, maxEntries int) []learning.LessonManifestEntry {
	if store == nil {
		return nil
	}
	recent, err := store.Recent(maxEntries)
	if err != nil {
		return nil
	}
	out := make([]learning.LessonManifestEntry, 0, len(recent))
	for _, lesson := range recent {
		out = append(out, learning.LessonManifestEntry{
			ID:    lesson.ID,
			Topic: lesson.Topic,
			Text:  lesson.Text,
		})
	}
	return out
}

func llmSourceFor(workflow string) string {
	if workflow == "" {
		return "agent:llm"
	}
	return "agent:llm:" + workflow
}

func applyPersonaHint(lesson *learning.Lesson) {
	if lesson == nil || lesson.PersonaDirection == "" || lesson.PersonaMagnitude == "" {
		return
	}
	delta := learning.PersonaDeltaFromHint(lesson.PersonaDirection, lesson.PersonaMagnitude)
	if delta == 0 {
		return
	}
	if len(lesson.PersonaDelta) == 0 {
		if lesson.PersonaParam == "" {
			return
		}
		lesson.PersonaDelta = []self.Lesson{{Param: lesson.PersonaParam, Delta: delta}}
		return
	}
	if lesson.PersonaParam == "" {
		for i := range lesson.PersonaDelta {
			lesson.PersonaDelta[i].Delta = delta
		}
		return
	}
	for i := range lesson.PersonaDelta {
		if lesson.PersonaDelta[i].Param == "" {
			lesson.PersonaDelta[i].Param = lesson.PersonaParam
		}
		lesson.PersonaDelta[i].Delta = delta
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

func workflowToolCallCount(src []agent.ToolStat) int {
	total := 0
	for _, stat := range src {
		total += stat.Calls
	}
	return total
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
