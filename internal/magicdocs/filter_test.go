package magicdocs

import (
	"testing"
	"time"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

func TestClassify(t *testing.T) {
	base := event.NewBaseWith(time.Now(), "test-session")

	tests := []struct {
		name string
		ev   event.Event
		want classification
	}{
		{"text_delta", event.TextDeltaEvent{Base: base, Content: "hello"}, drop},
		{"tool_use_start", event.ToolUseStartEvent{Base: base, ID: "1", Name: "read"}, drop},
		{"tool_use_delta", event.ToolUseDeltaEvent{Base: base, ID: "1", Input: "{}"}, drop},
		{"stream_done", event.StreamDoneEvent{Base: base, Usage: llm.UsageStats{}}, drop},
		{"stream_error", event.StreamErrorEvent{Base: base}, drop},
		{"iteration_start", event.IterationStartEvent{Base: base, Iteration: 1, Max: 10}, drop},

		{"research_conclusion", event.ResearchProgressEvent{Base: base, Phase: "conclusion", Round: 3, Message: "done"}, pass},
		{"research_synthesis", event.ResearchProgressEvent{Base: base, Phase: "synthesis", Round: 1, Message: "sum"}, pass},
		{"hypothesis", event.HypothesisEvent{Base: base, HypothesisID: "h1", Statement: "x", Status: "validated"}, pass},
		{"agent_finish", event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}}, pass},
		{"skill_done", event.SkillExecuteEvent{Base: base, SkillName: "tdd", Status: "done"}, pass},
		{"daemon_task_done", event.DaemonTaskEvent{Base: base, TaskID: "t1", Status: "done"}, pass},

		{"research_exploring", event.ResearchProgressEvent{Base: base, Phase: "exploring", Round: 1, Message: "..."}, context_},
		{"skill_started", event.SkillExecuteEvent{Base: base, SkillName: "tdd", Status: "started"}, context_},
		{"daemon_task_started", event.DaemonTaskEvent{Base: base, TaskID: "t1", Status: "started"}, context_},
		{"tool_use_done", event.ToolUseDoneEvent{Base: base, ID: "1", Name: "read", Input: "{}"}, context_},
		{"tool_progress", event.ToolProgressEvent{Base: base, ToolName: "bash", Preview: "ls"}, context_},
		{"compression", event.CompressionEvent{Base: base, BeforeCount: 20, AfterCount: 10}, context_},
		{"workflow_progress", event.WorkflowProgressEvent{Base: base, Intent: "research", Workflow: "deep"}, context_},
		{"usage_progress", event.UsageProgressEvent{Base: base, Summary: "$0.05"}, context_},
		{"session_resume", event.SessionResumeEvent{Base: base, ResumedSessionID: "s1", Surface: "cli"}, context_},
		{"classified_error", event.ClassifiedErrorEvent{Base: base, Classification: "rate_limit"}, context_},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(tt.ev)
			if got != tt.want {
				t.Errorf("classify(%s) = %d, want %d", tt.name, got, tt.want)
			}
		})
	}
}

func TestFilter(t *testing.T) {
	base := event.NewBaseWith(time.Now(), "test-session")
	events := []event.Event{
		event.TextDeltaEvent{Base: base, Content: "hello"},
		event.ResearchProgressEvent{Base: base, Phase: "conclusion", Message: "found it"},
		event.ToolUseDoneEvent{Base: base, ID: "1", Name: "read", Input: "{}"},
		event.IterationStartEvent{Base: base, Iteration: 1, Max: 5},
		event.HypothesisEvent{Base: base, HypothesisID: "h1", Statement: "x", Status: "ok"},
	}
	result := Filter(events)
	if len(result.Signal) != 2 {
		t.Errorf("Signal count = %d, want 2", len(result.Signal))
	}
	if len(result.Context) != 1 {
		t.Errorf("Context count = %d, want 1", len(result.Context))
	}
}

func TestFilter_NoSignal_ReturnsEmpty(t *testing.T) {
	base := event.NewBaseWith(time.Now(), "test-session")
	events := []event.Event{
		event.TextDeltaEvent{Base: base, Content: "hello"},
		event.ToolUseDeltaEvent{Base: base, ID: "1", Input: "{}"},
	}
	result := Filter(events)
	if len(result.Signal) != 0 {
		t.Errorf("Signal count = %d, want 0", len(result.Signal))
	}
}
