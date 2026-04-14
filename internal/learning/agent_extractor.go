package learning

import (
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/self"
)

// AgentResultInfo is the minimal view of an agent run consumed by ExtractAgent.
type AgentResultInfo struct {
	Topic         string
	FinishReason  string
	Iterations    int
	MaxIterations int
	OutputTokens  int
	InputTokens   int
	TotalCost     float64
	ToolStats     []AgentToolStat
}

type AgentToolStat struct {
	Name      string
	Calls     int
	Errors    int
	TotalTime time.Duration
}

const (
	agentToolFailureThreshold  = 3
	agentVerboseOutputTokens   = 50_000
	agentEfficientIterationPct = 0.3
	agentStalledReason         = "budget_exceeded"
)

// ExtractAgent derives lessons from a completed agent run using fixed rules.
func ExtractAgent(info AgentResultInfo) []Lesson {
	var lessons []Lesson
	now := time.Now().UTC()
	topic := strings.TrimSpace(info.Topic)
	if topic == "" {
		topic = "agent-task"
	}

	for _, ts := range info.ToolStats {
		if ts.Errors >= agentToolFailureThreshold {
			lessons = append(lessons, Lesson{
				Text:       truncate(fmt.Sprintf("Tool %q failed %dx on %s; reconsider before retrying the same approach.", ts.Name, ts.Errors, topic), maxLessonTextLen),
				Topic:      topic,
				Source:     "agent",
				Confidence: "medium",
				PersonaDelta: []self.Lesson{{
					Param: "caution",
					Delta: 0.02,
				}},
				Created: now,
			})
		}
	}

	if info.FinishReason == agentStalledReason {
		lessons = append(lessons, Lesson{
			Text:       truncate(fmt.Sprintf("Task stalled at iteration %d/%d on %s; scope or decompose earlier.", info.Iterations, info.MaxIterations, topic), maxLessonTextLen),
			Topic:      topic,
			Source:     "agent",
			Confidence: "medium",
			PersonaDelta: []self.Lesson{
				{Param: "caution", Delta: 0.03},
				{Param: "verbosity", Delta: -0.01},
			},
			Created: now,
		})
	}

	if info.FinishReason == "stop" && info.MaxIterations > 0 {
		pct := float64(info.Iterations) / float64(info.MaxIterations)
		if pct > 0 && pct <= agentEfficientIterationPct && totalCalls(info.ToolStats) > 0 {
			lessons = append(lessons, Lesson{
				Text:       truncate(fmt.Sprintf("Efficient completion on %s: %d/%d iterations; pattern worth repeating.", topic, info.Iterations, info.MaxIterations), maxLessonTextLen),
				Topic:      topic,
				Source:     "agent",
				Confidence: "high",
				PersonaDelta: []self.Lesson{{
					Param: "persistence",
					Delta: 0.01,
				}},
				Created: now,
			})
		}
	}

	if info.OutputTokens >= agentVerboseOutputTokens {
		lessons = append(lessons, Lesson{
			Text:       truncate(fmt.Sprintf("Verbose output on %s: %d tokens; tighten summaries.", topic, info.OutputTokens), maxLessonTextLen),
			Topic:      topic,
			Source:     "agent",
			Confidence: "medium",
			PersonaDelta: []self.Lesson{{
				Param: "verbosity",
				Delta: -0.02,
			}},
			Created: now,
		})
	}

	return lessons
}

func totalCalls(stats []AgentToolStat) int {
	total := 0
	for _, stat := range stats {
		total += stat.Calls
	}
	return total
}
