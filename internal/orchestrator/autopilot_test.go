package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

func TestAutopilotWorkflow_E2E(t *testing.T) {
	ctx := context.Background()

	provider := newTestProvider(
		"Plan: 1. Create endpoint 2. Add validation 3. Write tests",     // plan
		"func handleCreate(w http.ResponseWriter, r *http.Request) { }", // code
		"Tests: 3/3 passed. Coverage: 85%",                              // test
		"Verification: COMPLETE. All requirements met.",                 // verify
		"완료했습니다! 사용자 등록 엔드포인트를 만들었습니다.",                                 // summary synthesis
	)

	wf := NewAutopilotWorkflow()
	input := testInput("Create a user registration endpoint", provider)

	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("AutopilotWorkflow.Run: %v", err)
	}

	if result.Workflow != "autopilot" {
		t.Errorf("workflow = %q, want %q", result.Workflow, "autopilot")
	}

	// 4 stages (streamed) + 1 summary synthesis (streamed) = 5 provider calls
	if provider.CallCount() != 5 {
		t.Errorf("provider calls = %d, want 5", provider.CallCount())
	}

	// Messages accumulate: initial user + 4 stages * (instruction + response) = 9
	if len(result.Messages) < 9 {
		t.Errorf("messages = %d, want >= 9", len(result.Messages))
	}

	// Summary is synthesized in assistant tone, not verify stage output.
	if !strings.Contains(result.Summary, "완료") {
		t.Errorf("summary %q should contain assistant-tone completion", result.Summary)
	}

	// Usage accumulates across 4 streamed stages + 1 streamed summary
	wantTokens := 5 * 10 // 5 Stream calls × 10 input tokens each
	if result.Usage.InputTokens != wantTokens {
		t.Errorf("input tokens = %d, want %d", result.Usage.InputTokens, wantTokens)
	}
}

func TestAutopilotWorkflow_StageNames(t *testing.T) {
	if len(autopilotStages) != 4 {
		t.Fatalf("expected 4 autopilot stages, got %d", len(autopilotStages))
	}

	wantNames := []string{"plan", "code", "test", "verify"}
	for i, s := range autopilotStages {
		if s.name != wantNames[i] {
			t.Errorf("stage[%d].name = %q, want %q", i, s.name, wantNames[i])
		}
		// Each stage's instruction function should produce non-empty output.
		instruction := s.instruction("test task")
		if instruction == "" {
			t.Errorf("stage %q produced empty instruction", s.name)
		}
	}
}

type failOnCallProvider struct {
	call   int
	failOn int
}

func (p *failOnCallProvider) Name() string            { return "test" }
func (p *failOnCallProvider) Models() []llm.ModelInfo { return nil }
func (p *failOnCallProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (p *failOnCallProvider) Stream(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.call++
	if p.call == p.failOn {
		return errors.New("provider unavailable")
	}
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "stage ok"})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
	return nil
}

func TestAutopilotWorkflow_StopsOnStageFailure(t *testing.T) {
	ctx := context.Background()
	provider := &failOnCallProvider{failOn: 2}

	wf := NewAutopilotWorkflow()
	input := testInput("Implement a safe planner recovery path", provider)

	result, err := wf.Run(ctx, input)
	if err == nil {
		t.Fatal("expected stage failure error")
	}
	if result == nil {
		t.Fatal("expected partial result on stage failure")
	}
	if provider.call != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.call)
	}
	if !strings.Contains(err.Error(), `stage "code" failed`) {
		t.Fatalf("error %q should mention failing stage", err)
	}
	if !strings.Contains(result.Summary, `stage "code" failed`) {
		t.Fatalf("summary %q should mention failing stage", result.Summary)
	}
}

func TestAutopilotWorkflow_LearningAllStagesPass(t *testing.T) {
	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	provider := &scriptedSingleProvider{messages: []llm.Message{
		assistantStep("", llm.CompletedToolCall{ID: "plan-bash", Name: "bash", Input: `{}`}),
		assistantStep("Plan complete"),
		assistantStep("", llm.CompletedToolCall{ID: "code-bash", Name: "bash", Input: `{}`}),
		assistantStep("Code complete"),
		assistantStep("", llm.CompletedToolCall{ID: "test-bash", Name: "bash", Input: `{}`}),
		assistantStep("Tests complete"),
		assistantStep("", llm.CompletedToolCall{ID: "verify-bash", Name: "bash", Input: `{}`}),
		assistantStep("Verification complete"),
		llm.NewAssistantMessage("완료했습니다! 구현과 검증을 마쳤습니다."),
	}}

	result, err := NewAutopilotWorkflow().Run(context.Background(), autopilotLearningInput("ship the patch", provider, store))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Workflow != "autopilot" {
		t.Fatalf("workflow = %q, want autopilot", result.Workflow)
	}

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(lessons) != 1 {
		t.Fatalf("len(lessons) = %d, want 1", len(lessons))
	}
	if lessons[0].Source != "agent:autopilot" {
		t.Fatalf("source = %q, want agent:autopilot", lessons[0].Source)
	}
}

func TestAutopilotWorkflow_LLMExtractionUsesMergedRunContext(t *testing.T) {
	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	extractor := &countingExtractor{}
	provider := &scriptedSingleProvider{messages: []llm.Message{
		assistantStep("", llm.CompletedToolCall{ID: "plan-bash", Name: "bash", Input: `{}`}),
		assistantStep("Plan complete"),
		assistantStep("", llm.CompletedToolCall{ID: "code-bash", Name: "bash", Input: `{}`}),
		assistantStep("Code complete"),
		assistantStep("", llm.CompletedToolCall{ID: "test-bash", Name: "bash", Input: `{}`}),
		assistantStep("Tests complete"),
		assistantStep("", llm.CompletedToolCall{ID: "verify-bash", Name: "bash", Input: `{}`}),
		assistantStep("Verification complete"),
		llm.NewAssistantMessage("완료했습니다! 구현과 검증을 마쳤습니다."),
	}}
	input := autopilotLearningInput("ship the patch", provider, store)
	input.Learning = &LearningDeps{
		Store:          store,
		LLMExtractor:   extractor,
		ComplexityGate: learning.ComplexityGate{MinMessages: 1, RequireToolCall: true},
	}

	if _, err := NewAutopilotWorkflow().Run(context.Background(), input); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if extractor.calls != 1 {
		t.Fatalf("extractor calls = %d, want 1", extractor.calls)
	}
	if extractor.reqs[0].Workflow != "autopilot" {
		t.Fatalf("workflow = %q, want autopilot", extractor.reqs[0].Workflow)
	}
}

func TestAutopilotWorkflow_LearningMidStageFailTriggersLesson(t *testing.T) {
	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	provider := &failingScriptedProvider{
		failOn: 5,
		messages: []llm.Message{
			assistantStep("", llm.CompletedToolCall{ID: "plan-bash", Name: "bash", Input: `{}`}),
			assistantStep("", llm.CompletedToolCall{ID: "plan-bash-2", Name: "bash", Input: `{}`}),
			assistantStep("", llm.CompletedToolCall{ID: "plan-bash-3", Name: "bash", Input: `{}`}),
			assistantStep("Plan complete"),
		},
	}

	result, err := NewAutopilotWorkflow().Run(context.Background(), autopilotLearningInputWithBash("ship the patch", provider, store, func(context.Context, json.RawMessage) (*tools.Result, error) {
		return nil, errors.New("boom")
	}))
	if err == nil {
		t.Fatal("expected stage failure")
	}
	if result == nil {
		t.Fatal("expected partial result")
	}

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(lessons) != 1 {
		t.Fatalf("len(lessons) = %d, want 1", len(lessons))
	}
	if lessons[0].Source != "agent:autopilot" {
		t.Fatalf("source = %q, want agent:autopilot", lessons[0].Source)
	}
	if !strings.Contains(lessons[0].Text, "bash") {
		t.Fatalf("lesson text = %q, want tool-failure lesson", lessons[0].Text)
	}
}

func TestAutopilotWorkflow_NoPerStageLearning(t *testing.T) {
	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	provider := &scriptedSingleProvider{messages: []llm.Message{
		assistantStep("", llm.CompletedToolCall{ID: "plan-bash", Name: "bash", Input: `{}`}),
		assistantStep("Plan complete"),
		assistantStep("", llm.CompletedToolCall{ID: "code-bash", Name: "bash", Input: `{}`}),
		assistantStep("Code complete"),
		assistantStep("", llm.CompletedToolCall{ID: "test-bash", Name: "bash", Input: `{}`}),
		assistantStep("Tests complete"),
		assistantStep("", llm.CompletedToolCall{ID: "verify-bash", Name: "bash", Input: `{}`}),
		assistantStep("Verification complete"),
		llm.NewAssistantMessage("완료했습니다! 구현과 검증을 마쳤습니다."),
	}}

	_, err := NewAutopilotWorkflow().Run(context.Background(), autopilotLearningInput("ship the patch", provider, store))
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
}

type failingScriptedProvider struct {
	messages []llm.Message
	failOn   int
	callNum  int
}

func (p *failingScriptedProvider) Stream(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.callNum++
	if p.callNum == p.failOn {
		return errors.New("provider unavailable")
	}
	idx := p.callNum - 1
	if idx >= len(p.messages) {
		return errors.New("unexpected stream call")
	}
	msg := p.messages[idx]
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
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
	return nil
}

func (p *failingScriptedProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (p *failingScriptedProvider) Name() string            { return "test" }
func (p *failingScriptedProvider) Models() []llm.ModelInfo { return nil }

func autopilotLearningInput(msg string, provider llm.Provider, store *learning.Store) WorkflowInput {
	return autopilotLearningInputWithBash(msg, provider, store, nil)
}

func autopilotLearningInputWithBash(msg string, provider llm.Provider, store *learning.Store, bashFn func(context.Context, json.RawMessage) (*tools.Result, error)) WorkflowInput {
	input := testInput(msg, provider)
	reg := tools.NewRegistry()
	reg.Register(&testTool{name: "bash", executeFn: bashFn})
	input.Tools = reg
	input.Learning = &LearningDeps{Store: store}
	return input
}
