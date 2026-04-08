package orchestrator

import (
	"context"
	"strings"
	"testing"
)

func TestTeamWorkflow_E2E(t *testing.T) {
	ctx := context.Background()

	subtasksJSON := `[
		{"id":1,"title":"Research API","instruction":"List REST API patterns"},
		{"id":2,"title":"Draft schema","instruction":"Design the data model"}
	]`

	provider := newTestProvider(
		subtasksJSON,                                     // planner
		"API patterns: REST, GraphQL",                    // subtask 1
		"Schema: users, posts tables",                    // subtask 2
		"Combined: REST API with users and posts tables", // synthesizer
	)

	wf := NewTeamWorkflow()
	input := testInput("Design a blog API", provider)
	var streamed strings.Builder
	input.OnText = func(s string) { streamed.WriteString(s) }

	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("TeamWorkflow.Run: %v", err)
	}

	if result.Workflow != "team" {
		t.Errorf("workflow = %q, want %q", result.Workflow, "team")
	}

	// planner + 2 subtasks + synthesizer = 4
	if provider.CallCount() != 4 {
		t.Errorf("provider calls = %d, want 4", provider.CallCount())
	}

	if !strings.Contains(result.Summary, "Combined") {
		t.Errorf("summary %q should contain synthesized content", result.Summary)
	}

	if result.Usage.InputTokens == 0 {
		t.Error("usage input tokens should be > 0")
	}
	if !strings.Contains(streamed.String(), "[team]") {
		t.Errorf("expected team progress output, got %q", streamed.String())
	}
	if !strings.Contains(streamed.String(), "Combined") {
		t.Errorf("expected synthesized stream output, got %q", streamed.String())
	}
}

func TestTeamWorkflow_FallbackToSingle(t *testing.T) {
	ctx := context.Background()

	provider := newTestProvider(
		"[]",                   // planner returns empty subtasks
		"Direct single answer", // fallback single workflow
	)

	wf := NewTeamWorkflow()
	input := testInput("Simple question", provider)

	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("TeamWorkflow.Run fallback: %v", err)
	}

	if result.Summary == "" {
		t.Error("summary should not be empty for fallback case")
	}
}

func TestParseSubtasks(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{
			name: "plain JSON",
			raw:  `[{"id":1,"title":"A","instruction":"Do A"},{"id":2,"title":"B","instruction":"Do B"}]`,
			want: 2,
		},
		{
			name: "with code fence",
			raw:  "```json\n[{\"id\":1,\"title\":\"A\",\"instruction\":\"Do A\"}]\n```",
			want: 1,
		},
		{
			name:    "no JSON array",
			raw:     "just text with no array",
			wantErr: true,
		},
		{
			name: "JSON with surrounding text",
			raw:  "Here is the plan:\n[{\"id\":1,\"title\":\"Only task\",\"instruction\":\"Do it\"}]\nDone.",
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSubtasks(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSubtasks: %v", err)
			}
			if len(got) != tt.want {
				t.Errorf("got %d subtasks, want %d", len(got), tt.want)
			}
		})
	}
}
