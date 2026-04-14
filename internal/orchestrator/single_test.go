package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/self"
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

type scriptedSingleProvider struct {
	messages []llm.Message
	usages   []llm.UsageStats
	callNum  int
}

func (p *scriptedSingleProvider) Stream(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
	if p.callNum >= len(p.messages) {
		return errors.New("unexpected stream call")
	}
	msg := p.messages[p.callNum]
	usage := llm.UsageStats{InputTokens: 1, OutputTokens: 1}
	if p.callNum < len(p.usages) {
		usage = p.usages[p.callNum]
	}
	p.callNum++

	for _, block := range msg.Content {
		switch b := block.(type) {
		case llm.TextBlock:
			if b.Text != "" {
				cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: b.Text})
			}
		case llm.ToolUseBlock:
			cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: b.ID, Name: b.Name}})
			cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: b.ID, Name: b.Name, Input: string(b.Input)}})
		}
	}

	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &usage})
	return nil
}

func (p *scriptedSingleProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (p *scriptedSingleProvider) Name() string            { return "test" }
func (p *scriptedSingleProvider) Models() []llm.ModelInfo { return nil }

func assistantStep(text string, toolCalls ...llm.CompletedToolCall) llm.Message {
	return llm.BuildAssistantMessage([]string{text}, toolCalls)
}

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

func TestSingleWorkflow_Learning_Nil(t *testing.T) {
	ctx := context.Background()
	lessonPath := filepath.Join(t.TempDir(), "lessons.jsonl")
	reg := tools.NewRegistry()
	reg.Register(&testTool{name: "bash"})

	provider := &scriptedSingleProvider{messages: []llm.Message{
		assistantStep("", llm.CompletedToolCall{ID: "bash-1", Name: "bash", Input: `{}`}),
		assistantStep("done"),
	}}

	_, err := NewSingleWorkflow().Run(ctx, WorkflowInput{
		Message:  "run bash",
		Tools:    reg,
		Provider: provider,
		Config: WorkflowConfig{
			MaxIterations: 5,
			Permission:    agent.NewPermission(agent.WithMode(agent.ModeBypass)),
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(lessonPath); !os.IsNotExist(err) {
		t.Fatalf("lessons file stat err = %v, want not exists", err)
	}
}

func TestSingleWorkflow_Learning_RuleATrigger(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store := learning.NewStore(filepath.Join(dataDir, "lessons.jsonl"))
	reg := tools.NewRegistry()
	bashCalls := 0
	reg.Register(&testTool{
		name: "bash",
		executeFn: func(context.Context, json.RawMessage) (*tools.Result, error) {
			bashCalls++
			if bashCalls <= 3 {
				return nil, errors.New("boom")
			}
			return tools.SuccessResult("ok"), nil
		},
	})

	provider := &scriptedSingleProvider{messages: []llm.Message{
		assistantStep("", llm.CompletedToolCall{ID: "bash-1", Name: "bash", Input: `{}`}),
		assistantStep("", llm.CompletedToolCall{ID: "bash-2", Name: "bash", Input: `{}`}),
		assistantStep("", llm.CompletedToolCall{ID: "bash-3", Name: "bash", Input: `{}`}),
		assistantStep("", llm.CompletedToolCall{ID: "bash-4", Name: "bash", Input: `{}`}),
		assistantStep("", llm.CompletedToolCall{ID: "bash-5", Name: "bash", Input: `{}`}),
		assistantStep("done"),
	}}

	_, err := NewSingleWorkflow().Run(ctx, WorkflowInput{
		Message:  "repo cleanup",
		Tools:    reg,
		Provider: provider,
		Config: WorkflowConfig{
			MaxIterations: 10,
			Permission:    agent.NewPermission(agent.WithMode(agent.ModeBypass)),
		},
		Learning: &LearningDeps{Store: store},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(lessons) != 1 {
		t.Fatalf("len(lessons) = %d, want 1", len(lessons))
	}
	if lessons[0].Source != "agent:single" {
		t.Fatalf("lesson source = %q, want agent:single", lessons[0].Source)
	}
	if !strings.Contains(lessons[0].Text, "bash") {
		t.Fatalf("lesson text = %q, want bash mention", lessons[0].Text)
	}
}

func TestSingleWorkflow_Learning_PersonaApplied(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store := learning.NewStore(filepath.Join(dataDir, "lessons.jsonl"))
	state := self.New(dataDir)
	before := state.GetPersona()
	reg := tools.NewRegistry()
	reg.Register(&testTool{name: "bash"})

	provider := &scriptedSingleProvider{messages: []llm.Message{
		assistantStep("", llm.CompletedToolCall{ID: "bash-1", Name: "bash", Input: `{}`}),
	}}

	_, err := NewSingleWorkflow().Run(ctx, WorkflowInput{
		Message:  "investigate build issue",
		Tools:    reg,
		Provider: provider,
		Config: WorkflowConfig{
			MaxIterations: 1,
			Permission:    agent.NewPermission(agent.WithMode(agent.ModeBypass)),
		},
		Learning: &LearningDeps{Store: store, SelfState: state},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	after := state.GetPersona()
	if after.Caution <= before.Caution {
		t.Fatalf("Caution = %v, want > %v", after.Caution, before.Caution)
	}
	saved, err := self.Load(dataDir)
	if err != nil {
		t.Fatalf("self.Load: %v", err)
	}
	if saved.GetPersona().Caution <= before.Caution {
		t.Fatalf("saved caution = %v, want > %v", saved.GetPersona().Caution, before.Caution)
	}
}

func TestSingleWorkflow_Learning_StoreAppendError(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	badParent := filepath.Join(dataDir, "not-a-dir")
	if err := os.WriteFile(badParent, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store := learning.NewStore(filepath.Join(badParent, "lessons.jsonl"))
	reg := tools.NewRegistry()
	reg.Register(&testTool{name: "bash"})
	provider := &scriptedSingleProvider{
		messages: []llm.Message{
			assistantStep("", llm.CompletedToolCall{ID: "bash-1", Name: "bash", Input: `{}`}),
		},
		usages: []llm.UsageStats{{InputTokens: 1, OutputTokens: 60000}},
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	_, err := NewSingleWorkflow().Run(ctx, WorkflowInput{
		Message:  "summarize the repo",
		Tools:    reg,
		Provider: provider,
		Config: WorkflowConfig{
			MaxIterations: 1,
			Permission:    agent.NewPermission(agent.WithMode(agent.ModeBypass)),
		},
		Learning: &LearningDeps{Store: store, Logger: logger},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.Count(logs.String(), "agent learning: append failed"); got != 2 {
		t.Fatalf("append warning count = %d, want 2\nlogs:\n%s", got, logs.String())
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
