package conversation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/llm"
)

func TestParseIntentResponse(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantIntent Intent
		wantErr    bool
	}{
		{
			name:       "plain JSON question",
			input:      `{"intent": "question", "confidence": 0.9}`,
			wantIntent: IntentQuestion,
		},
		{
			name:       "simple_task",
			input:      `{"intent": "simple_task", "confidence": 0.8}`,
			wantIntent: IntentSimpleTask,
		},
		{
			name:       "complex_task",
			input:      `{"intent": "complex_task", "confidence": 0.75}`,
			wantIntent: IntentComplexTask,
		},
		{
			name:       "project",
			input:      `{"intent": "project", "confidence": 0.6}`,
			wantIntent: IntentProject,
		},
		{
			name:       "research",
			input:      `{"intent": "research", "confidence": 0.95}`,
			wantIntent: IntentResearch,
		},
		{
			name:       "unclear",
			input:      `{"intent": "unclear", "confidence": 0.3}`,
			wantIntent: IntentUnclear,
		},
		{
			name:       "chat",
			input:      `{"intent": "chat", "confidence": 1.0}`,
			wantIntent: IntentChat,
		},
		{
			name:       "leading whitespace and trailing newline",
			input:      "  \n{\"intent\": \"question\", \"confidence\": 0.7}\n  ",
			wantIntent: IntentQuestion,
		},
		{
			name:       "markdown code fence json",
			input:      "```json\n{\"intent\": \"chat\", \"confidence\": 0.5}\n```",
			wantIntent: IntentChat,
		},
		{
			name:       "markdown code fence plain",
			input:      "```\n{\"intent\": \"research\", \"confidence\": 0.8}\n```",
			wantIntent: IntentResearch,
		},
		{
			name:    "invalid JSON no braces",
			input:   "intent: question",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "unknown intent category",
			input:   `{"intent": "coding", "confidence": 0.9}`,
			wantErr: true,
		},
		{
			name:    "missing intent field",
			input:   `{"confidence": 0.9}`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseIntentResponse(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got result=%+v", result)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Intent != tc.wantIntent {
				t.Errorf("Intent = %q, want %q", result.Intent, tc.wantIntent)
			}
		})
	}
}

func TestIntentConstants(t *testing.T) {
	all := []Intent{
		IntentQuestion,
		IntentSimpleTask,
		IntentComplexTask,
		IntentProject,
		IntentResearch,
		IntentWikiQuery,
		IntentUnclear,
		IntentChat,
	}

	if len(all) != 8 {
		t.Errorf("expected 8 intent constants, got %d", len(all))
	}

	seen := make(map[Intent]bool, len(all))
	for _, intent := range all {
		if intent == "" {
			t.Errorf("intent constant must not be empty string")
		}
		if seen[intent] {
			t.Errorf("duplicate intent constant: %q", intent)
		}
		seen[intent] = true
	}
}

func TestLLMClassifierClassify(t *testing.T) {
	classifier := NewLLMClassifier()
	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{Content: `{"intent":"question","confidence":0.9}`}, nil
		},
	}

	intent, err := classifier.Classify(context.Background(), provider, "what is Go?", nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if intent != IntentQuestion {
		t.Errorf("intent = %q, want %q", intent, IntentQuestion)
	}
}

func TestLLMClassifierClassify_ProviderError(t *testing.T) {
	classifier := NewLLMClassifier()
	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			return nil, fmt.Errorf("network error")
		},
	}

	intent, err := classifier.Classify(context.Background(), provider, "test", nil)
	if err == nil {
		t.Fatal("expected error from provider, got nil")
	}
	if intent != IntentUnclear {
		t.Errorf("intent = %q, want %q on provider error", intent, IntentUnclear)
	}
}

func TestLLMClassifierClassify_UnparseableResponse(t *testing.T) {
	// Non-JSON response → IntentUnclear, no error.
	classifier := NewLLMClassifier()
	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{Content: "I think it's a question"}, nil
		},
	}

	intent, err := classifier.Classify(context.Background(), provider, "test", nil)
	if err != nil {
		t.Fatalf("Classify: unexpected error: %v", err)
	}
	if intent != IntentUnclear {
		t.Errorf("intent = %q, want %q for unparseable response", intent, IntentUnclear)
	}
}

func TestLLMClassifierClassify_HistoryTruncation(t *testing.T) {
	// History longer than 8 messages is truncated to the last 8.
	var capturedReq llm.ChatRequest
	provider := &mockProvider{
		chatFn: func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			capturedReq = req
			return &llm.ChatResponse{Content: `{"intent":"chat","confidence":0.8}`}, nil
		},
	}

	history := make([]llm.Message, 12)
	for i := range history {
		history[i] = llm.NewUserMessage(fmt.Sprintf("msg %d", i))
	}

	classifier := NewLLMClassifier()
	_, err := classifier.Classify(context.Background(), provider, "hello", history)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	// 8 history messages + 1 classification request = 9 total.
	want := 9
	if len(capturedReq.Messages) != want {
		t.Errorf("captured %d messages, want %d (8 history + 1 classify)", len(capturedReq.Messages), want)
	}
	if capturedReq.System != classificationPrompt {
		t.Errorf("system prompt mismatch")
	}
	if capturedReq.MaxTokens != 64 {
		t.Errorf("MaxTokens = %d, want 64", capturedReq.MaxTokens)
	}
	if capturedReq.Temperature != 0.0 {
		t.Errorf("Temperature = %v, want 0.0", capturedReq.Temperature)
	}
}

func TestLLMClassifierClassify_ShortHistory(t *testing.T) {
	// History shorter than 8 passes through unchanged.
	var capturedReq llm.ChatRequest
	provider := &mockProvider{
		chatFn: func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			capturedReq = req
			return &llm.ChatResponse{Content: `{"intent":"question","confidence":0.7}`}, nil
		},
	}

	history := []llm.Message{
		llm.NewUserMessage("one"),
		llm.NewAssistantMessage("two"),
	}

	classifier := NewLLMClassifier()
	_, err := classifier.Classify(context.Background(), provider, "hello", history)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	// 2 history + 1 classify request = 3.
	if len(capturedReq.Messages) != 3 {
		t.Errorf("captured %d messages, want 3", len(capturedReq.Messages))
	}
}

func TestLLMClassifierClassify_AllIntents(t *testing.T) {
	cases := []struct {
		response string
		want     Intent
	}{
		{`{"intent":"question","confidence":0.9}`, IntentQuestion},
		{`{"intent":"simple_task","confidence":0.8}`, IntentSimpleTask},
		{`{"intent":"complex_task","confidence":0.7}`, IntentComplexTask},
		{`{"intent":"project","confidence":0.6}`, IntentProject},
		{`{"intent":"research","confidence":0.95}`, IntentResearch},
		{`{"intent":"wiki_query","confidence":0.92}`, IntentWikiQuery},
		{`{"intent":"unclear","confidence":0.3}`, IntentUnclear},
		{`{"intent":"chat","confidence":1.0}`, IntentChat},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.want), func(t *testing.T) {
			classifier := NewLLMClassifier()
			provider := &mockProvider{
				chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
					return &llm.ChatResponse{Content: tc.response}, nil
				},
			}
			got, err := classifier.Classify(context.Background(), provider, "msg", nil)
			if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			if got != tc.want {
				t.Errorf("intent = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFilterTextMessages(t *testing.T) {
	msgs := []llm.Message{
		llm.NewUserMessage("hello"),
		{Role: "assistant", Content: []llm.ContentBlock{
			llm.TextBlock{Text: "I'll run bash"},
			llm.ToolUseBlock{ID: "t1", Name: "bash", Input: []byte(`{}`)},
		}},
		llm.NewToolResultMessage("t1", "ok", false),
		llm.NewAssistantMessage("done"),
		llm.NewUserMessage("thanks"),
	}

	got := filterTextMessages(msgs)
	if len(got) != 3 {
		t.Fatalf("filterTextMessages returned %d messages, want 3", len(got))
	}
	if got[0].Text() != "hello" {
		t.Errorf("msg[0] = %q, want %q", got[0].Text(), "hello")
	}
	if got[1].Text() != "done" {
		t.Errorf("msg[1] = %q, want %q", got[1].Text(), "done")
	}
	if got[2].Text() != "thanks" {
		t.Errorf("msg[2] = %q, want %q", got[2].Text(), "thanks")
	}
}

func TestLLMClassifierClassify_ToolMessagesFiltered(t *testing.T) {
	var capturedReq llm.ChatRequest
	provider := &mockProvider{
		chatFn: func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			capturedReq = req
			return &llm.ChatResponse{Content: `{"intent":"simple_task","confidence":0.9}`}, nil
		},
	}

	history := []llm.Message{
		llm.NewUserMessage("add email validation"),
		{Role: "assistant", Content: []llm.ContentBlock{
			llm.TextBlock{Text: "running bash"},
			llm.ToolUseBlock{ID: "t1", Name: "bash", Input: []byte(`{}`)},
		}},
		llm.NewToolResultMessage("t1", "file written", false),
		llm.NewAssistantMessage("done"),
	}

	classifier := NewLLMClassifier()
	_, err := classifier.Classify(context.Background(), provider, "add domain check", history)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	// Only 2 text messages from history + 1 classify request = 3.
	if len(capturedReq.Messages) != 3 {
		t.Errorf("captured %d messages, want 3 (tool messages should be filtered)", len(capturedReq.Messages))
	}
}

func TestClassificationPromptBoundaryGuidance(t *testing.T) {
	for _, needle := range []string{
		`Prefer "wiki_query" over "question"`,
		`Prefer "research" over "question"`,
		`Prefer "project" over "complex_task"`,
		`Prefer "simple_task" only for clearly bounded one-step edits or commands.`,
	} {
		if !strings.Contains(classificationPrompt, needle) {
			t.Fatalf("classificationPrompt missing %q", needle)
		}
	}
}
