package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
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
		"Incomplete answer",                          // attempt 1
		"NEEDS_REVISION: missing error handling",     // verify 1 → needs revision
		"Complete answer with error handling",        // attempt 2
		"PASS",                                       // verify 2 → pass
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

	// All attempts get NEEDS_REVISION verdict.
	provider := newTestProvider(
		"Bad answer 1", "NEEDS_REVISION: wrong",
		"Bad answer 2", "NEEDS_REVISION: still wrong",
		"Bad answer 3", "NEEDS_REVISION: nope",
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

func TestRalphWorkflow_FailExitsImmediately(t *testing.T) {
	ctx := context.Background()

	provider := newTestProvider(
		"Wrong approach entirely",
		"FAIL: fundamentally wrong algorithm",
	)

	wf := NewRalphWorkflow()
	wf.MaxAttempts = 5
	input := testInput("Implement sorting", provider)

	_, err := wf.Run(ctx, input)
	if err == nil {
		t.Fatal("expected error on FAIL verdict")
	}
	if !strings.Contains(err.Error(), "fundamentally incorrect") {
		t.Fatalf("error = %q, want 'fundamentally incorrect'", err.Error())
	}

	// 1 attempt + 1 verify = 2 calls (no retries)
	if provider.CallCount() != 2 {
		t.Errorf("provider calls = %d, want 2 (should not retry after FAIL)", provider.CallCount())
	}
}

func TestRalphWorkflow_NeedsRevisionRetries(t *testing.T) {
	ctx := context.Background()

	provider := newTestProvider(
		"Partial answer",
		"NEEDS_REVISION: missing edge cases",
		"Better answer with edge cases",
		"PASS",
	)

	wf := NewRalphWorkflow()
	input := testInput("Handle all edge cases", provider)

	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("RalphWorkflow.Run: %v", err)
	}

	// 2 attempts × (1 execution + 1 verify) = 4 calls
	if provider.CallCount() != 4 {
		t.Errorf("provider calls = %d, want 4", provider.CallCount())
	}

	if !strings.Contains(result.Summary, "edge cases") {
		t.Errorf("summary = %q, want to contain passing attempt answer", result.Summary)
	}
}

func TestRalphWorkflow_LearningVerifiedFirstAttempt(t *testing.T) {
	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	provider := &scriptedSingleProvider{messages: []llm.Message{
		assistantStep("", llm.CompletedToolCall{ID: "bash-1", Name: "bash", Input: `{}`}),
		assistantStep("Complete answer with verification"),
		llm.NewAssistantMessage("PASS"),
	}}

	result, err := NewRalphWorkflow().Run(context.Background(), ralphLearningInput("verify this change", provider, store))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Workflow != "ralph" {
		t.Fatalf("workflow = %q, want ralph", result.Workflow)
	}

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(lessons) != 1 {
		t.Fatalf("len(lessons) = %d, want 1", len(lessons))
	}
	if lessons[0].Source != "agent:ralph" {
		t.Fatalf("source = %q, want agent:ralph", lessons[0].Source)
	}
	if strings.Contains(lessons[0].Text, "retried") {
		t.Fatalf("lesson text = %q, want no retry lesson", lessons[0].Text)
	}
}

func TestRalphWorkflow_LLMExtractionUsesMergedRunContext(t *testing.T) {
	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	extractor := &countingExtractor{}
	provider := &scriptedSingleProvider{messages: []llm.Message{
		assistantStep("", llm.CompletedToolCall{ID: "bash-1", Name: "bash", Input: `{}`}),
		assistantStep("Attempt one"),
		llm.NewAssistantMessage("NEEDS_REVISION: add verification"),
		assistantStep("", llm.CompletedToolCall{ID: "bash-2", Name: "bash", Input: `{}`}),
		assistantStep("Attempt two with verification"),
		llm.NewAssistantMessage("PASS"),
	}}
	input := ralphLearningInput("verify this change", provider, store)
	input.Learning = &LearningDeps{
		Store:          store,
		LLMExtractor:   extractor,
		ComplexityGate: learning.ComplexityGate{MinMessages: 1, RequireToolCall: true},
	}

	if _, err := NewRalphWorkflow().Run(context.Background(), input); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if extractor.calls != 1 {
		t.Fatalf("extractor calls = %d, want 1", extractor.calls)
	}
	if extractor.reqs[0].Workflow != "ralph" {
		t.Fatalf("workflow = %q, want ralph", extractor.reqs[0].Workflow)
	}
}

func TestRalphWorkflow_LearningBelowThreshold(t *testing.T) {
	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	provider := &scriptedSingleProvider{messages: []llm.Message{
		assistantStep("", llm.CompletedToolCall{ID: "bash-1", Name: "bash", Input: `{}`}),
		assistantStep("Attempt one"),
		llm.NewAssistantMessage("NEEDS_REVISION: missing verification"),
		assistantStep("", llm.CompletedToolCall{ID: "bash-2", Name: "bash", Input: `{}`}),
		assistantStep("Attempt two"),
		llm.NewAssistantMessage("NEEDS_REVISION: still incomplete"),
		assistantStep("", llm.CompletedToolCall{ID: "bash-3", Name: "bash", Input: `{}`}),
		assistantStep("Attempt three"),
		llm.NewAssistantMessage("PASS"),
	}}

	_, err := NewRalphWorkflow().Run(context.Background(), ralphLearningInput("verify this change", provider, store))
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
	if lessons[0].Source != "agent:ralph" {
		t.Fatalf("source = %q, want agent:ralph", lessons[0].Source)
	}
	if strings.Contains(lessons[0].Text, "retried") {
		t.Fatalf("lesson text = %q, want no retry lesson", lessons[0].Text)
	}
}

func TestRalphWorkflow_LearningRetryTriggersRuleE(t *testing.T) {
	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	provider := &scriptedSingleProvider{messages: []llm.Message{
		assistantStep("", llm.CompletedToolCall{ID: "bash-1", Name: "bash", Input: `{}`}),
		assistantStep("Attempt one"),
		llm.NewAssistantMessage("NEEDS_REVISION: missing verification"),
		assistantStep("", llm.CompletedToolCall{ID: "bash-2", Name: "bash", Input: `{}`}),
		assistantStep("Attempt two"),
		llm.NewAssistantMessage("NEEDS_REVISION: still incomplete"),
		assistantStep("", llm.CompletedToolCall{ID: "bash-3", Name: "bash", Input: `{}`}),
		assistantStep("Attempt three"),
		llm.NewAssistantMessage("NEEDS_REVISION: one more issue"),
		assistantStep("", llm.CompletedToolCall{ID: "bash-4", Name: "bash", Input: `{}`}),
		assistantStep("Attempt four"),
		llm.NewAssistantMessage("PASS"),
	}}

	_, err := NewRalphWorkflow().Run(context.Background(), ralphLearningInput("verify this change", provider, store))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertRetryLesson(t, store, "retried 3 times", true)
}

func TestRalphWorkflow_LearningCapExceededFinishReason(t *testing.T) {
	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	provider := &scriptedSingleProvider{messages: []llm.Message{
		assistantStep("", llm.CompletedToolCall{ID: "bash-1", Name: "bash", Input: `{}`}),
		assistantStep("Attempt one"),
		llm.NewAssistantMessage("NEEDS_REVISION: missing verification"),
		assistantStep("", llm.CompletedToolCall{ID: "bash-2", Name: "bash", Input: `{}`}),
		assistantStep("Attempt two"),
		llm.NewAssistantMessage("NEEDS_REVISION: still incomplete"),
		assistantStep("", llm.CompletedToolCall{ID: "bash-3", Name: "bash", Input: `{}`}),
		assistantStep("Attempt three"),
		llm.NewAssistantMessage("NEEDS_REVISION: one more issue"),
		assistantStep("", llm.CompletedToolCall{ID: "bash-4", Name: "bash", Input: `{}`}),
		assistantStep("Attempt four"),
		llm.NewAssistantMessage("NEEDS_REVISION: not done"),
		assistantStep("", llm.CompletedToolCall{ID: "bash-5", Name: "bash", Input: `{}`}),
		assistantStep("Attempt five"),
		llm.NewAssistantMessage("NEEDS_REVISION: still broken"),
	}}

	wf := NewRalphWorkflow()
	wf.MaxAttempts = 5
	_, err := wf.Run(context.Background(), ralphLearningInput("verify this change", provider, store))
	if err == nil {
		t.Fatal("expected exhausted-attempts error")
	}

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(lessons) != 1 {
		t.Fatalf("len(lessons) = %d, want 1", len(lessons))
	}
	if lessons[0].Source != "agent:ralph" {
		t.Fatalf("source = %q, want agent:ralph", lessons[0].Source)
	}
	if !strings.Contains(lessons[0].Text, "retried 4 times") {
		t.Fatalf("lesson text = %q, want cap-exceeded retry lesson", lessons[0].Text)
	}
	if strings.Contains(lessons[0].Text, "Efficient completion") {
		t.Fatalf("lesson text = %q, want no efficient-completion lesson after cap exceeded", lessons[0].Text)
	}
}

func TestRalphWorkflow_NoPerIterLearning(t *testing.T) {
	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	provider := &scriptedSingleProvider{messages: []llm.Message{
		assistantStep("", llm.CompletedToolCall{ID: "bash-1", Name: "bash", Input: `{}`}),
		assistantStep("Attempt one"),
		llm.NewAssistantMessage("NEEDS_REVISION: missing verification"),
		assistantStep("", llm.CompletedToolCall{ID: "bash-2", Name: "bash", Input: `{}`}),
		assistantStep("Attempt two"),
		llm.NewAssistantMessage("NEEDS_REVISION: still incomplete"),
		assistantStep("", llm.CompletedToolCall{ID: "bash-3", Name: "bash", Input: `{}`}),
		assistantStep("Attempt three"),
		llm.NewAssistantMessage("PASS"),
	}}

	_, err := NewRalphWorkflow().Run(context.Background(), ralphLearningInput("verify this change", provider, store))
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

func TestRalphWorkflow_VerifyParsing(t *testing.T) {
	tests := []struct {
		name        string
		verdict     string
		wantVerdict VerifyVerdict
		wantFeed    string
	}{
		{"plain pass", "PASS", VerdictPass, ""},
		{"pass with detail", "PASS — excellent work", VerdictPass, ""},
		{"lowercase pass", "pass", VerdictPass, ""},
		{"fail with reason", "FAIL: needs more tests", VerdictFail, "needs more tests"},
		{"fail no colon", "FAIL needs more tests", VerdictFail, "FAIL needs more tests"},
		{"needs revision with feedback", "NEEDS_REVISION: add error handling", VerdictNeedsRevision, "add error handling"},
		{"needs revision no colon", "NEEDS_REVISION add tests", VerdictNeedsRevision, "NEEDS_REVISION add tests"},
		{"unknown defaults to needs revision", "MAYBE", VerdictNeedsRevision, "MAYBE"},
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

			switch tt.wantVerdict {
			case VerdictPass:
				if err != nil {
					t.Fatalf("expected pass, got error: %v", err)
				}
				if result == nil {
					t.Fatal("expected non-nil result on PASS")
				}
			case VerdictFail:
				if err == nil {
					t.Fatal("expected error for FAIL verdict")
				}
				if !strings.Contains(err.Error(), "fundamentally incorrect") {
					t.Fatalf("error = %q, want 'fundamentally incorrect'", err.Error())
				}
			case VerdictNeedsRevision:
				if err == nil {
					t.Fatal("expected error for NEEDS_REVISION verdict with 1 attempt")
				}
				if !strings.Contains(err.Error(), "not verified after 1 attempts") {
					t.Fatalf("error = %q, want 'not verified after 1 attempts'", err.Error())
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

func ralphLearningInput(msg string, provider llm.Provider, store *learning.Store) WorkflowInput {
	input := testInput(msg, provider)
	reg := tools.NewRegistry()
	reg.Register(&testTool{name: "bash"})
	input.Tools = reg
	input.Learning = &LearningDeps{Store: store}
	return input
}

func assertRetryLesson(t *testing.T, store *learning.Store, wantSubstring string, wantRetry bool) {
	t.Helper()
	lessons, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	foundRetry := false
	for _, lesson := range lessons {
		if strings.Contains(lesson.Text, wantSubstring) {
			foundRetry = true
			if lesson.Source != "agent:ralph" {
				t.Fatalf("source = %q, want agent:ralph", lesson.Source)
			}
		}
	}
	if foundRetry != wantRetry {
		t.Fatalf("found retry lesson = %v, want %v; lessons=%+v", foundRetry, wantRetry, lessons)
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

	verdict, _, _, err := wf.verify(context.Background(), input, result)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verdict != VerdictPass {
		t.Fatalf("expected VerdictPass, got %d", verdict)
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
	if !strings.Contains(provider.prompt, "NEEDS_REVISION") {
		t.Fatalf("verification prompt should include NEEDS_REVISION option:\n%s", provider.prompt)
	}
}
