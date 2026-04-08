package orchestrator

import (
	"context"
	"strings"
	"testing"
)

func TestAutopilotWorkflow_E2E(t *testing.T) {
	ctx := context.Background()

	provider := newTestProvider(
		"Plan: 1. Create endpoint 2. Add validation 3. Write tests", // plan
		"func handleCreate(w http.ResponseWriter, r *http.Request) { }", // code
		"Tests: 3/3 passed. Coverage: 85%",                              // test
		"Verification: COMPLETE. All requirements met.",                   // verify
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

	// 4 stages = 4 provider calls
	if provider.CallCount() != 4 {
		t.Errorf("provider calls = %d, want 4", provider.CallCount())
	}

	// Messages accumulate: initial user + 4 stages * (instruction + response) = 9
	if len(result.Messages) < 9 {
		t.Errorf("messages = %d, want >= 9", len(result.Messages))
	}

	// Summary comes from last assistant message (verify stage)
	if !strings.Contains(result.Summary, "COMPLETE") {
		t.Errorf("summary %q should contain COMPLETE from verify stage", result.Summary)
	}

	// Usage accumulates across all stages
	wantTokens := 4 * 10 // 4 calls × 10 input tokens each
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
