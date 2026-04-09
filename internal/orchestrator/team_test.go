package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/llm"
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
		wantID  int
		wantErr bool
	}{
		{
			name:   "plain JSON",
			raw:    `[{"id":1,"title":"A","instruction":"Do A"},{"id":2,"title":"B","instruction":"Do B"}]`,
			want:   2,
			wantID: 1,
		},
		{
			name:   "with code fence",
			raw:    "```json\n[{\"id\":1,\"title\":\"A\",\"instruction\":\"Do A\"}]\n```",
			want:   1,
			wantID: 1,
		},
		{
			name:    "no JSON array",
			raw:     "just text with no array",
			wantErr: true,
		},
		{
			name:   "JSON with surrounding text",
			raw:    "Here is the plan:\n[{\"id\":1,\"title\":\"Only task\",\"instruction\":\"Do it\"}]\nDone.",
			want:   1,
			wantID: 1,
		},
		{
			name: "multiple arrays choose last valid plan",
			raw: "Example array:\n" +
				`[{"id":99,"title":"Example","instruction":"Ignore this"}]` + "\n" +
				"Actual plan:\n" +
				`[{"id":1,"title":"Inspect","instruction":"Inspect the failure"},{"id":2,"title":"Fix","instruction":"Implement the patch"}]`,
			want:   2,
			wantID: 1,
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
			if tt.want > 0 && got[0].ID != tt.wantID {
				t.Errorf("first subtask id = %d, want %d", got[0].ID, tt.wantID)
			}
		})
	}
}

func TestTeamWorkflow_PlannerFailureFallsBackToSingle(t *testing.T) {
	ctx := context.Background()

	provider := newTestProvider(
		"planner output with no usable JSON array",
		"Recovered via single workflow",
	)

	wf := NewTeamWorkflow()
	input := testInput("Ship the smallest safe fix", provider)

	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("TeamWorkflow.Run fallback after planner failure: %v", err)
	}

	if provider.CallCount() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.CallCount())
	}
	if result.Workflow != "single" {
		t.Fatalf("workflow = %q, want %q", result.Workflow, "single")
	}
	if !strings.Contains(result.Summary, "Recovered via single workflow") {
		t.Fatalf("summary %q should contain fallback answer", result.Summary)
	}
}

type promptCaptureProvider struct {
	prompt string
}

func (p *promptCaptureProvider) Name() string            { return "test" }
func (p *promptCaptureProvider) Models() []llm.ModelInfo { return nil }
func (p *promptCaptureProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, nil
}
func (p *promptCaptureProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	if len(req.Messages) > 0 {
		p.prompt = req.Messages[len(req.Messages)-1].Text()
	}
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: `[]`})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
	return nil
}

func TestTeamPlannerPromptIncludesBrownfieldRules(t *testing.T) {
	provider := &promptCaptureProvider{}
	wf := NewTeamWorkflow()
	input := testInput("Modify the existing server middleware and verify the change with tests", provider)

	_, _ = wf.planSubtasks(context.Background(), input)

	for _, needle := range []string{
		"at least one subtask MUST modify code",
		"at least one subtask MUST verify the change",
		"Do not return analysis-only subtasks",
		"actual working-tree diff",
	} {
		if !strings.Contains(provider.prompt, needle) {
			t.Fatalf("planner prompt missing %q:\n%s", needle, provider.prompt)
		}
	}
}
