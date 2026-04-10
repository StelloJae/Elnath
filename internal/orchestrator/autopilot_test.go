package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/llm"
)

func TestAutopilotWorkflow_E2E(t *testing.T) {
	ctx := context.Background()

	provider := newTestProvider(
		"Plan: 1. Create endpoint 2. Add validation 3. Write tests",     // plan
		"func handleCreate(w http.ResponseWriter, r *http.Request) { }", // code
		"Tests: 3/3 passed. Coverage: 85%",                              // test
		"Verification: COMPLETE. All requirements met.",                  // verify
		"완료했습니다! 사용자 등록 엔드포인트를 만들었습니다.",                              // summary synthesis
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
