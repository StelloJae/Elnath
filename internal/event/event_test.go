package event

import (
	"testing"
	"time"

	"github.com/stello/elnath/internal/llm"
)

func TestNewBase(t *testing.T) {
	before := time.Now()
	b := NewBase()
	after := time.Now()

	if b.Timestamp().Before(before) || b.Timestamp().After(after) {
		t.Fatalf("timestamp %v not between %v and %v", b.Timestamp(), before, after)
	}
	if b.SessionID() != "" {
		t.Fatalf("default session ID should be empty, got %q", b.SessionID())
	}
}

func TestNewBaseWith(t *testing.T) {
	ts := time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	b := NewBaseWith(ts, "sess-123")

	if b.Timestamp() != ts {
		t.Fatalf("expected %v, got %v", ts, b.Timestamp())
	}
	if b.SessionID() != "sess-123" {
		t.Fatalf("expected sess-123, got %q", b.SessionID())
	}
}

func TestTextDeltaEvent(t *testing.T) {
	e := TextDeltaEvent{Base: NewBase(), Content: "hello"}

	if e.EventType() != "text_delta" {
		t.Fatalf("expected text_delta, got %q", e.EventType())
	}
	if e.Content != "hello" {
		t.Fatalf("expected hello, got %q", e.Content)
	}
	// Satisfies Event interface.
	var _ Event = e
}

func TestToolProgressEvent(t *testing.T) {
	e := ToolProgressEvent{
		Base:     NewBase(),
		ToolName: "bash",
		Preview:  "ls -la",
	}

	if e.EventType() != "tool_progress" {
		t.Fatalf("expected tool_progress, got %q", e.EventType())
	}
	if e.ToolName != "bash" {
		t.Fatalf("expected bash, got %q", e.ToolName)
	}
	var _ Event = e
}

func TestWorkflowProgressEvent(t *testing.T) {
	e := WorkflowProgressEvent{
		Base:     NewBase(),
		Intent:   "research",
		Workflow: "deep_research",
	}

	if e.EventType() != "workflow_progress" {
		t.Fatalf("expected workflow_progress, got %q", e.EventType())
	}
	if e.Intent != "research" || e.Workflow != "deep_research" {
		t.Fatalf("unexpected fields: %+v", e)
	}
	var _ Event = e
}

func TestResearchProgressEvent(t *testing.T) {
	ts := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	e := ResearchProgressEvent{
		Base:    NewBaseWith(ts, "sess-abc"),
		Phase:   "hypothesis",
		Round:   2,
		Message: "evaluating sources",
	}

	if e.EventType() != "research_progress" {
		t.Fatalf("expected research_progress, got %q", e.EventType())
	}
	if e.Round != 2 {
		t.Fatalf("expected round 2, got %d", e.Round)
	}
	if e.SessionID() != "sess-abc" {
		t.Fatalf("expected sess-abc, got %q", e.SessionID())
	}
	var _ Event = e
}

func TestStreamDoneEventUsage(t *testing.T) {
	usage := llm.UsageStats{InputTokens: 100, OutputTokens: 50, CacheRead: 10}
	e := StreamDoneEvent{Base: NewBase(), Usage: usage}

	if e.EventType() != "stream_done" {
		t.Fatalf("expected stream_done, got %q", e.EventType())
	}
	if e.Usage.InputTokens != 100 {
		t.Fatalf("expected 100 input tokens, got %d", e.Usage.InputTokens)
	}
	var _ Event = e
}
