package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestResumeContextNodeEmpty(t *testing.T) {
	got, err := NewResumeContextNode(35).Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "" {
		t.Fatalf("Render() = %q, want empty", got)
	}
}

func TestResumeContextNodeRendersQuotedContext(t *testing.T) {
	got, err := NewResumeContextNode(35).Render(context.Background(), &RenderState{
		ResumeContext: "task_id: 42\nsummary: continue handoff",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"Task resume handoff context:",
		"task_id: 42",
		"summary: continue handoff",
		"quoted continuity context",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render() = %q, want %q", got, want)
		}
	}
}

func TestResumeContextNodeSkippedInBenchmarkMode(t *testing.T) {
	got, err := NewResumeContextNode(35).Render(context.Background(), &RenderState{
		BenchmarkMode: true,
		ResumeContext: "task_id: 42",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "" {
		t.Fatalf("Render() = %q, want empty in benchmark mode", got)
	}
}
