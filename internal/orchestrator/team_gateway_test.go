package orchestrator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

type teamGatewaySpy struct {
	mu    sync.Mutex
	calls int
	seen  tools.AgenticContext
}

func (s *teamGatewaySpy) Execute(ctx context.Context, _ string, _ json.RawMessage) (*tools.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	c, _ := tools.AgenticContextFrom(ctx)
	s.seen = c
	return tools.SuccessResult("gateway read ok"), nil
}

func (s *teamGatewaySpy) snapshot() (int, tools.AgenticContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.seen
}

func TestTeamWorkflow_WithGatewayOptInPropagatesGatewayToSubtasks(t *testing.T) {
	ctx := context.Background()
	provider := &teamLearningProvider{
		planner: `[{"id":1,"title":"Read","instruction":"inspect files"}]`,
		synth:   "Combined result",
		scripts: map[string][]llm.Message{
			"inspect files": {
				assistantStep("", llm.CompletedToolCall{ID: "read-1", Name: "read", Input: `{}`}),
				assistantStep("subtask done"),
			},
		},
		indexes: map[string]int{},
	}
	reg := tools.NewRegistry()
	reg.Register(&testTool{name: "read"})
	spy := &teamGatewaySpy{}
	input := testInput("inspect the repo", provider)
	input.Tools = reg
	input.AgenticTaskID = 42
	input.Config.ToolExecutor = spy

	ctx = tools.WithAgenticContext(ctx, tools.AgenticContext{TaskID: input.AgenticTaskID})
	if _, err := NewTeamWorkflow().Run(ctx, input); err != nil {
		t.Fatalf("Run: %v", err)
	}

	calls, seen := spy.snapshot()
	if calls != 1 {
		t.Fatalf("gateway calls = %d, want 1", calls)
	}
	if seen.TaskID != input.AgenticTaskID || seen.ToolCallID != "read-1" {
		t.Fatalf("agentic context = %+v, want task_id=%d tool_call_id=read-1", seen, input.AgenticTaskID)
	}
}

func TestTeamWorkflow_WithoutGatewayOptInUsesPlainRegistry(t *testing.T) {
	ctx := context.Background()
	provider := &teamLearningProvider{
		planner: `[{"id":1,"title":"Read","instruction":"inspect files"}]`,
		synth:   "Combined result",
		scripts: map[string][]llm.Message{
			"inspect files": {
				assistantStep("", llm.CompletedToolCall{ID: "read-1", Name: "read", Input: `{}`}),
				assistantStep("subtask done"),
			},
		},
		indexes: map[string]int{},
	}
	reg := tools.NewRegistry()
	var registryCalls int
	reg.Register(&testTool{
		name: "read",
		executeFn: func(context.Context, json.RawMessage) (*tools.Result, error) {
			registryCalls++
			return tools.SuccessResult("registry read ok"), nil
		},
	})
	input := testInput("inspect the repo", provider)
	input.Tools = reg

	if _, err := NewTeamWorkflow().Run(ctx, input); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if registryCalls != 1 {
		t.Fatalf("registry calls = %d, want 1", registryCalls)
	}
}
