package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/llm"
)

func TestRalphWorkflow_PassOnFirst(t *testing.T) {
	ctx := context.Background()

	provider := newTestProvider(
		"Complete answer with all details", // attempt 1
		"PASS",                             // verification → pass
	)

	wf := NewRalphWorkflow()
	input := testInput("Explain goroutines", provider)

	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("RalphWorkflow.Run: %v", err)
	}

	if result.Workflow != "ralph" {
		t.Errorf("workflow = %q, want %q", result.Workflow, "ralph")
	}

	// 1 attempt + 1 verify = 2 calls
	if provider.CallCount() != 2 {
		t.Errorf("provider calls = %d, want 2", provider.CallCount())
	}

	if !strings.Contains(result.Summary, "Complete answer") {
		t.Errorf("summary = %q, want to contain attempt answer", result.Summary)
	}
}

func TestRalphWorkflow_RetryThenPass(t *testing.T) {
	ctx := context.Background()

	provider := newTestProvider(
		"Incomplete answer",                   // attempt 1
		"FAIL: missing error handling",        // verify 1 → fail
		"Complete answer with error handling", // attempt 2
		"PASS",                                // verify 2 → pass
	)

	wf := NewRalphWorkflow()
	input := testInput("Write a robust HTTP handler", provider)

	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("RalphWorkflow.Run: %v", err)
	}

	// 2 attempts × (1 execution + 1 verify) = 4 calls
	if provider.CallCount() != 4 {
		t.Errorf("provider calls = %d, want 4", provider.CallCount())
	}

	// Summary should be from the passing attempt
	if !strings.Contains(result.Summary, "error handling") {
		t.Errorf("summary %q should be from passing attempt", result.Summary)
	}

	// Usage should accumulate from all attempts
	wantTokens := 4 * 10
	if result.Usage.InputTokens != wantTokens {
		t.Errorf("input tokens = %d, want %d", result.Usage.InputTokens, wantTokens)
	}
}

func TestRalphWorkflow_ExhaustedAttempts(t *testing.T) {
	ctx := context.Background()

	// All attempts fail verification.
	provider := newTestProvider(
		"Bad answer 1", "FAIL: wrong",
		"Bad answer 2", "FAIL: still wrong",
		"Bad answer 3", "FAIL: nope",
	)

	wf := NewRalphWorkflow()
	wf.MaxAttempts = 3
	input := testInput("Impossible task", provider)

	_, err := wf.Run(ctx, input)
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if !strings.Contains(err.Error(), "not verified after 3 attempts") {
		t.Errorf("error = %q, want 'not verified after 3 attempts'", err.Error())
	}

	// 3 attempts × 2 calls = 6
	if provider.CallCount() != 6 {
		t.Errorf("provider calls = %d, want 6", provider.CallCount())
	}
}

func TestRalphWorkflow_VerifyParsing(t *testing.T) {
	tests := []struct {
		name     string
		verdict  string
		wantOK   bool
		wantFeed string
	}{
		{"plain pass", "PASS", true, ""},
		{"pass with detail", "PASS — excellent work", true, ""},
		{"lowercase pass", "pass", true, ""},
		{"fail with reason", "FAIL: needs more tests", false, "needs more tests"},
		{"fail no colon", "FAIL needs more tests", false, "FAIL needs more tests"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := newTestProvider(
				"some answer",
				tt.verdict,
			)

			wf := NewRalphWorkflow()
			wf.MaxAttempts = 1
			input := testInput("test", provider)

			result, err := wf.Run(ctx(), input)

			if tt.wantOK {
				if err != nil {
					t.Fatalf("expected pass, got error: %v", err)
				}
				if result == nil {
					t.Fatal("expected non-nil result on PASS")
				}
			} else {
				// With MaxAttempts=1, a FAIL means exhausted → error returned
				if err == nil {
					t.Fatal("expected error for FAIL verdict with 1 attempt")
				}
			}
		})
	}
}

func ctx() context.Context { return context.Background() }

func TestBuildRecoveryPrompt(t *testing.T) {
	prompt := buildRecoveryPrompt("Fix the HTTP handler", "missing verification")
	for _, needle := range []string{
		"Original task:",
		"Fix the HTTP handler",
		"Verifier feedback:",
		"missing verification",
		"prefer the smallest correct change",
		"use repo-native tests or verification commands",
		"modified files",
		"verification command/result",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("prompt missing %q:\n%s", needle, prompt)
		}
	}
}

func TestSanitizeRetryMessages(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, Content: nil},
		llm.NewAssistantMessage(""),
		llm.NewUserMessage("keep me"),
		llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.ToolUseBlock{ID: "t1", Name: "bash", Input: []byte(`{}`)}}},
	}
	got := sanitizeRetryMessages(msgs)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Role != llm.RoleUser {
		t.Fatalf("expected first kept message to be user, got %q", got[0].Role)
	}
	if got[1].Role != llm.RoleAssistant || len(llm.ExtractToolUseBlocks(got[1])) != 1 {
		t.Fatalf("expected tool-use assistant to be kept, got %+v", got[1])
	}
}

type verificationPromptCaptureProvider struct {
	prompt string
}

func (p *verificationPromptCaptureProvider) Name() string            { return "test" }
func (p *verificationPromptCaptureProvider) Models() []llm.ModelInfo { return nil }
func (p *verificationPromptCaptureProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: "PASS"}, nil
}
func (p *verificationPromptCaptureProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	if len(req.Messages) > 0 {
		p.prompt = req.Messages[len(req.Messages)-1].Text()
	}
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "PASS"})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
	return nil
}

func TestBuildVerificationEvidenceIncludesAssistantAndToolResults(t *testing.T) {
	longAnswer := strings.Repeat("details-", 40)
	msgs := []llm.Message{
		llm.NewUserMessage("original task"),
		{
			Role: llm.RoleUser,
			Content: []llm.ContentBlock{
				llm.ToolResultBlock{ToolUseID: "bash-1", Content: "go test ./...\nok\n", IsError: false},
			},
		},
		llm.NewAssistantMessage(longAnswer),
	}

	evidence := buildVerificationEvidence(msgs)
	for _, needle := range []string{
		"Final assistant answer:",
		longAnswer,
		"Recent tool evidence:",
		"go test ./...",
		"bash-1",
	} {
		if !strings.Contains(evidence, needle) {
			t.Fatalf("verification evidence missing %q:\n%s", needle, evidence)
		}
	}
}

func TestRalphVerifyPromptUsesExecutionEvidence(t *testing.T) {
	provider := &verificationPromptCaptureProvider{}
	wf := NewRalphWorkflow()
	input := testInput("add request-id middleware", provider)
	result := &WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("task"),
			{
				Role: llm.RoleUser,
				Content: []llm.ContentBlock{
					llm.ToolResultBlock{ToolUseID: "bash-verify", Content: "go test ./...\nok\n", IsError: false},
				},
			},
			llm.NewAssistantMessage(strings.Repeat("implemented patch with verification evidence ", 8)),
		},
	}

	ok, _, _, err := wf.verify(context.Background(), input, result)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("expected PASS verdict")
	}
	for _, needle := range []string{
		"Execution evidence:",
		"Recent tool evidence:",
		"go test ./...",
		"implemented patch with verification evidence",
	} {
		if !strings.Contains(provider.prompt, needle) {
			t.Fatalf("verification prompt missing %q:\n%s", needle, provider.prompt)
		}
	}
	if strings.Contains(provider.prompt, "Answer to evaluate:") {
		t.Fatalf("verification prompt should use execution evidence wording:\n%s", provider.prompt)
	}
}
