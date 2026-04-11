package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

// toolCallProvider returns a tool_use call on the first call,
// then a text-only response on the second call.
type toolCallProvider struct {
	callNum int
	calls   []llm.ChatRequest
}

func (p *toolCallProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.callNum++
	p.calls = append(p.calls, req)

	if p.callNum == 1 {
		cb(llm.StreamEvent{
			Type:     llm.EventToolUseStart,
			ToolCall: &llm.ToolUseEvent{ID: "t1", Name: "bash"},
		})
		cb(llm.StreamEvent{
			Type:     llm.EventToolUseDone,
			ToolCall: &llm.ToolUseEvent{ID: "t1", Name: "bash", Input: `{"command":"echo hi"}`},
		})
		cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
		return nil
	}

	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "done"})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 5, OutputTokens: 3}})
	return nil
}

func (p *toolCallProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (p *toolCallProvider) Name() string            { return "test" }
func (p *toolCallProvider) Models() []llm.ModelInfo { return nil }

func TestSingleWorkflow_PermissionPropagated(t *testing.T) {
	ctx := context.Background()

	// Register a bash tool that records execution.
	var bashExecuted bool
	reg := tools.NewRegistry()
	reg.Register(&testTool{
		name: "bash",
		executeFn: func(_ context.Context, _ json.RawMessage) (*tools.Result, error) {
			bashExecuted = true
			return tools.SuccessResult("ok"), nil
		},
	})

	// ModePlan denies bash.
	perm := agent.NewPermission(agent.WithMode(agent.ModePlan))

	provider := &toolCallProvider{}
	wf := NewSingleWorkflow()

	input := WorkflowInput{
		Message:  "run bash",
		Messages: nil,
		Tools:    reg,
		Provider: provider,
		Config: WorkflowConfig{
			MaxIterations: 10,
			Permission:    perm,
		},
	}

	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if bashExecuted {
		t.Error("bash was executed despite ModePlan permission — Permission not propagated to agent")
	}

	// The tool result should contain "permission denied".
	found := false
	for _, msg := range result.Messages {
		text := msg.Text()
		if strings.Contains(text, "permission denied") {
			found = true
			break
		}
		for _, block := range msg.Content {
			if tr, ok := block.(llm.ToolResultBlock); ok {
				if strings.Contains(tr.Content, "permission denied") {
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Error("expected 'permission denied' in tool result messages")
	}
}

// testTool is a minimal tools.Tool for orchestrator tests.
type testTool struct {
	name      string
	executeFn func(context.Context, json.RawMessage) (*tools.Result, error)
}

func (t *testTool) Name() string                           { return t.name }
func (t *testTool) Description() string                    { return "test tool" }
func (t *testTool) Schema() json.RawMessage                { return json.RawMessage(`{"type":"object"}`) }
func (t *testTool) IsConcurrencySafe(json.RawMessage) bool { return false }
func (t *testTool) Reversible() bool                       { return false }
func (t *testTool) Scope(json.RawMessage) tools.ToolScope  { return tools.ConservativeScope() }
func (t *testTool) ShouldCancelSiblingsOnError() bool      { return false }
func (t *testTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	if t.executeFn != nil {
		return t.executeFn(ctx, params)
	}
	return tools.SuccessResult("ok"), nil
}
