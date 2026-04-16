package magicdocs

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
)

func TestAccumulatorObserver_AccumulatesAndTriggers(t *testing.T) {
	ch := make(chan ExtractionRequest, 16)
	obs := NewAccumulatorObserver(ch, "test-session", slog.Default())
	base := event.NewBaseWith(time.Now(), "test-session")

	obs.OnEvent(event.TextDeltaEvent{Base: base, Content: "hello"})
	obs.OnEvent(event.ToolUseDoneEvent{Base: base, ID: "1", Name: "read", Input: "{}"})
	obs.OnEvent(event.ResearchProgressEvent{Base: base, Phase: "exploring", Round: 1, Message: "..."})

	select {
	case <-ch:
		t.Fatal("should not trigger yet")
	default:
	}

	obs.OnEvent(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})

	select {
	case req := <-ch:
		if len(req.Events) != 4 {
			t.Errorf("Events count = %d, want 4", len(req.Events))
		}
		if req.SessionID != "test-session" {
			t.Errorf("SessionID = %q, want %q", req.SessionID, "test-session")
		}
		if req.Trigger != "agent_finish" {
			t.Errorf("Trigger = %q, want %q", req.Trigger, "agent_finish")
		}
	default:
		t.Fatal("expected extraction request on channel")
	}
}

func TestAccumulatorObserver_BufferResetsAfterTrigger(t *testing.T) {
	ch := make(chan ExtractionRequest, 16)
	obs := NewAccumulatorObserver(ch, "test-session", slog.Default())
	base := event.NewBaseWith(time.Now(), "test-session")

	obs.OnEvent(event.TextDeltaEvent{Base: base, Content: "first"})
	obs.OnEvent(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})
	<-ch

	obs.OnEvent(event.TextDeltaEvent{Base: base, Content: "second"})
	obs.OnEvent(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})

	req := <-ch
	if len(req.Events) != 2 {
		t.Errorf("second batch Events count = %d, want 2 (not 4)", len(req.Events))
	}
}

func TestAccumulatorObserver_BackpressureDrop(t *testing.T) {
	ch := make(chan ExtractionRequest, 1)
	obs := NewAccumulatorObserver(ch, "test-session", slog.Default())
	base := event.NewBaseWith(time.Now(), "test-session")

	obs.OnEvent(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})
	obs.OnEvent(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})

	select {
	case <-ch:
	default:
		t.Fatal("first request should be in channel")
	}
}

func TestIsTrigger(t *testing.T) {
	base := event.NewBaseWith(time.Now(), "test-session")
	tests := []struct {
		name string
		ev   event.Event
		want bool
	}{
		{"agent_finish", event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}}, true},
		{"research_conclusion", event.ResearchProgressEvent{Base: base, Phase: "conclusion"}, true},
		{"research_synthesis", event.ResearchProgressEvent{Base: base, Phase: "synthesis"}, true},
		{"research_exploring", event.ResearchProgressEvent{Base: base, Phase: "exploring"}, false},
		{"skill_done", event.SkillExecuteEvent{Base: base, SkillName: "s", Status: "done"}, true},
		{"skill_started", event.SkillExecuteEvent{Base: base, SkillName: "s", Status: "started"}, false},
		{"daemon_done", event.DaemonTaskEvent{Base: base, TaskID: "t", Status: "done"}, true},
		{"daemon_queued", event.DaemonTaskEvent{Base: base, TaskID: "t", Status: "queued"}, false},
		{"text_delta", event.TextDeltaEvent{Base: base, Content: "hi"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTrigger(tt.ev); got != tt.want {
				t.Errorf("isTrigger(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
