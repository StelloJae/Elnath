//go:build fault_e2e

package fault

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/fault/faulttype"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

type e2eProvider struct{}

func (p *e2eProvider) Name() string            { return "anthropic" }
func (p *e2eProvider) Models() []llm.ModelInfo { return nil }
func (p *e2eProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (p *e2eProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	results := latestToolResults(req.Messages)
	if len(results) == 0 {
		emitToolCall(cb, "tool-1", "bash", `{"command":"pwd"}`)
		return nil
	}
	last := results[len(results)-1]
	if last.IsError && len(results) < 4 {
		emitToolCall(cb, "tool-retry", "bash", `{"command":"pwd"}`)
		return nil
	}
	text := "scenario complete"
	if last.IsError {
		text = "scenario failed"
	}
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: text})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
	return nil
}

type e2eTool struct{}

func (e *e2eTool) Name() string                           { return "bash" }
func (e *e2eTool) Description() string                    { return "bash" }
func (e *e2eTool) Schema() json.RawMessage                { return json.RawMessage(`{"type":"object"}`) }
func (e *e2eTool) IsConcurrencySafe(json.RawMessage) bool { return false }
func (e *e2eTool) Reversible() bool                       { return false }
func (e *e2eTool) Scope(json.RawMessage) tools.ToolScope  { return tools.ConservativeScope() }
func (e *e2eTool) ShouldCancelSiblingsOnError() bool      { return false }
func (e *e2eTool) Execute(context.Context, json.RawMessage) (*tools.Result, error) {
	return tools.SuccessResult("/tmp"), nil
}

func TestFaultE2EToolBashTransientFail(t *testing.T) {
	passes := 0
	for run := 0; run < 20; run++ {
		s := testScenario("tool-bash-transient-fail", faulttype.CategoryTool, faulttype.FaultTransientError)
		s.FaultRate = 0.20
		reg := tools.NewRegistry()
		reg.Register(&e2eTool{})
		inj := NewScenarioInjector(s, int64(run+1))
		a := agent.New(&e2eProvider{}, reg,
			agent.WithPermission(agent.NewPermission(agent.WithMode(agent.ModeBypass))),
			agent.WithToolExecutor(NewToolFaultHook(reg, inj, s)),
		)
		result, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("run scenario")}, nil)
		if err == nil && result.FinishReason == agent.FinishReasonStop && strings.Contains(result.Messages[len(result.Messages)-1].Text(), "scenario complete") {
			passes++
		}
	}
	if passes < 19 {
		t.Fatalf("passes = %d, want >= 19", passes)
	}
}

func emitToolCall(cb func(llm.StreamEvent), id, name, input string) {
	cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: id, Name: name}})
	cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: id, Name: name, Input: input}})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
}

func latestToolResults(messages []llm.Message) []llm.ToolResultBlock {
	var results []llm.ToolResultBlock
	for _, msg := range messages {
		for _, block := range msg.Content {
			if tr, ok := block.(llm.ToolResultBlock); ok {
				results = append(results, tr)
			}
		}
	}
	return results
}
