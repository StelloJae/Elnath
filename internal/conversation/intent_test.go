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

// TestLLMClassifierDemotesShortResearchToQuestion guards Phase 7.5 (3a):
// Hermes-style depth signal. Classifier may promote lightweight queries to
// "research" based on phrasing (e.g., "웹에서 찾아서 정리해줘"), but a short
// message without investigation keywords should not trigger the full
// research workflow. Post-classifier gate demotes those to "question".
func TestLLMClassifierDemotesShortResearchToQuestion(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    Intent
	}{
		{
			name:    "short_lookup_demoted",
			message: "오늘 인기 미국 주식 TOP 5를 웹에서 찾아서 정리해줘",
			want:    IntentQuestion,
		},
		{
			name:    "long_with_analyze_keyword_keeps_research",
			message: "Compare the pricing model, security posture, and uptime of AWS RDS and GCP Cloud SQL over the last year and analyze the tradeoffs.",
			want:    IntentResearch,
		},
		{
			name:    "korean_investigation_keyword_keeps_research",
			message: "회사별 OAuth 구현을 비교 분석해서 어떤 게 가장 안전한지 평가해줘",
			want:    IntentResearch,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			classifier := NewLLMClassifier()
			provider := &mockProvider{
				chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
					return &llm.ChatResponse{Content: `{"intent":"research","confidence":0.9}`}, nil
				},
			}
			got, err := classifier.Classify(context.Background(), provider, tc.message, nil)
			if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			if got != tc.want {
				t.Errorf("intent for %q = %q, want %q", tc.message, got, tc.want)
			}
		})
	}
}

func TestLLMClassifierClassify_AllIntents(t *testing.T) {
	cases := []struct {
		response string
		message  string
		want     Intent
	}{
		{`{"intent":"question","confidence":0.9}`, "msg", IntentQuestion},
		{`{"intent":"simple_task","confidence":0.8}`, "msg", IntentSimpleTask},
		{`{"intent":"complex_task","confidence":0.7}`, "msg", IntentComplexTask},
		// Project must carry an imperative verb to pass the FU-RouterImperativeGate
		// and avoid demotion to chat.
		{`{"intent":"project","confidence":0.6}`, "Build me a habit tracker app.", IntentProject},
		// Research must carry an investigation keyword to pass the depth
		// gate introduced in Phase 7.5 (3a) and avoid demotion to question.
		{`{"intent":"research","confidence":0.95}`, "Compare and analyze the tradeoffs between PostgreSQL and MySQL for our specific workload pattern.", IntentResearch},
		{`{"intent":"wiki_query","confidence":0.92}`, "msg", IntentWikiQuery},
		{`{"intent":"unclear","confidence":0.3}`, "msg", IntentUnclear},
		{`{"intent":"chat","confidence":1.0}`, "msg", IntentChat},
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
			got, err := classifier.Classify(context.Background(), provider, tc.message, nil)
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

// TestLLMClassifierDemotesProjectWithoutImperative guards FU-RouterImperativeGate:
// the LLM (primed by ongoing session history about a project) cheerfully labels
// declarative briefing statements as "project", which routes them to the
// autopilot workflow and triggers unwanted code generation. Mirrors the Phase
// 7.5 depth gate on research intent — declarative project claims without a
// creation imperative verb must be demoted to chat so a briefing message does
// not silently fire autopilot.
//
// Dogfood repro (2026-04-20, session 11, tasks #297-#299):
//   - "Core features: daily habit check-in, weekly review, streak tracking."
//   - "The name of the app is HabitForge."
//   - "Primary platform: iOS, with a web companion later."
// All three classified intent=project → autopilot → Ruby codegen → failure.
func TestLLMClassifierDemotesProjectWithoutImperative(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    Intent
	}{
		{
			name:    "declarative_features_list_demoted",
			message: "Core features: daily habit check-in, weekly review, streak tracking.",
			want:    IntentChat,
		},
		{
			name:    "declarative_app_name_demoted",
			message: "The name of the app is HabitForge.",
			want:    IntentChat,
		},
		{
			name:    "declarative_platform_demoted",
			message: "Primary platform: iOS, with a web companion later.",
			want:    IntentChat,
		},
		{
			name:    "english_imperative_build_keeps_project",
			message: "Build me a habit tracker app.",
			want:    IntentProject,
		},
		{
			name:    "korean_imperative_keeps_project",
			message: "습관 추적 앱 만들어줘",
			want:    IntentProject,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			classifier := NewLLMClassifier()
			provider := &mockProvider{
				chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
					return &llm.ChatResponse{Content: `{"intent":"project","confidence":0.9}`}, nil
				},
			}
			got, err := classifier.Classify(context.Background(), provider, tc.message, nil)
			if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			if got != tc.want {
				t.Errorf("intent for %q = %q, want %q", tc.message, got, tc.want)
			}
		})
	}
}

// TestIsComplexEnoughForResearchQAPhraseBlocklist guards Phase 8.2 Fix 6:
// single-step diagnostic questions carrying phrases like "walk me through"
// or "how to fix" must demote out of the research workflow even when
// length/URL/keyword thresholds would otherwise promote them. P01
// (v36 triage, 2026-04-23): 374 chars + URL → research → hypothesis
// round 2 empty-response parse fatal. Length alone is not evidence of
// multi-round investigation.
func TestIsComplexEnoughForResearchQAPhraseBlocklist(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    bool
	}{
		{
			name:    "p01_verbatim_374_chars_with_url_walk_me_through",
			message: "I have an API endpoint that works when I call it from my browser but returns 403 when I hit it with `curl -X POST https://api.example.com/v1/items -d '{\"name\":\"x\"}'`. The browser request carries the same session cookie my curl has via `-b cookies.txt`. Walk me through what's different and how to fix the curl invocation. I'll paste the verbose `curl -v` output on request.",
			want:    false,
		},
		{"short_walk_me_through", "Walk me through what's different here.", false},
		{"how_to_fix_with_long_stack", "My service returns 500 on /login with this stack trace: panic at auth.go:42 null dereference when the upstream session token is nil after the CSRF rotation we shipped last week. How to fix this properly?", false},
		{"how_do_i_fix_case_insensitive", "HOW DO I FIX this null pointer that keeps crashing the admin panel in production every time the cron task misses a retry?", false},
		{"what_is_different_variant", "What is different between my curl and the browser when both carry the same cookie via -b cookies.txt?", false},
		{"research_keyword_keeps_true", "Compare and analyze the tradeoffs between PostgreSQL and MySQL for our workload.", true},
		{"long_message_without_qa_phrase_keeps_true", strings.Repeat("pipeline throughput optimization candidate ", 8), true},
		{"empty_stays_false", "", false},
		{"whitespace_only_stays_false", "   \t\n ", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := isComplexEnoughForResearch(tc.message); got != tc.want {
				t.Errorf("isComplexEnoughForResearch(%q) = %v, want %v", tc.message, got, tc.want)
			}
		})
	}
}

// TestLLMClassifierDemotesQADiagnosticToQuestion guards Phase 8.2 Fix 6
// end-to-end: even when the LLM classifier labels a single-step
// diagnostic Q&A as research, the post-classifier depth gate demotes it
// to question so the research workflow never fires. Mirrors P01 failure
// mode in the v36 triage report.
func TestLLMClassifierDemotesQADiagnosticToQuestion(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    Intent
	}{
		{
			name:    "walk_me_through_demoted_to_question",
			message: "I have an API endpoint that works when I call it from my browser but returns 403 when I hit it with curl. Walk me through what's different and how to fix the curl invocation.",
			want:    IntentQuestion,
		},
		{
			name:    "how_to_fix_demoted_to_question",
			message: "My service returns 500 on /login with this stack trace: panic at auth.go:42 null dereference when session is nil. How to fix this properly?",
			want:    IntentQuestion,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			classifier := NewLLMClassifier()
			provider := &mockProvider{
				chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
					return &llm.ChatResponse{Content: `{"intent":"research","confidence":0.9}`}, nil
				},
			}
			got, err := classifier.Classify(context.Background(), provider, tc.message, nil)
			if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			if got != tc.want {
				t.Errorf("intent for %q = %q, want %q", tc.message, got, tc.want)
			}
		})
	}
}

func TestHasProjectImperative(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    bool
	}{
		{"english_build", "Build me a habit tracker app.", true},
		{"english_create", "Let's create a new onboarding flow.", true},
		{"english_implement", "Implement the payment retry queue.", true},
		{"english_make_with_space", "Make a REST API skeleton.", true},
		{"english_start_with_space", "Start a new feature branch tooling.", true},
		{"korean_mandel", "습관 추적 앱 만들어줘", true},
		{"korean_implement", "결제 재시도 큐 구현해", true},
		{"declarative_features", "Core features: daily habit check-in, weekly review, streak tracking.", false},
		{"declarative_name", "The name of the app is HabitForge.", false},
		{"declarative_platform", "Primary platform: iOS, with a web companion later.", false},
		{"question_without_verb", "What's the app name?", false},
		{"empty", "", false},
		{"whitespace_only", "   \t\n  ", false},
		{"makefile_noun_not_imperative", "The makefile is broken.", false},
		{"starter_noun_not_imperative", "This starter template is great.", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := hasProjectImperative(tc.message); got != tc.want {
				t.Errorf("hasProjectImperative(%q) = %v, want %v", tc.message, got, tc.want)
			}
		})
	}
}
