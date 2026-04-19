package conversation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/llm"
)

func TestEstimateTokens(t *testing.T) {
	cw := NewContextWindow()

	cases := []struct {
		name     string
		messages []llm.Message
		// wantMin / wantMax allow for the chars/4 heuristic variance.
		wantMin int
		wantMax int
	}{
		{
			name:     "empty slice",
			messages: nil,
			wantMin:  0,
			wantMax:  0,
		},
		{
			name:     "single empty message",
			messages: []llm.Message{{Role: "user", Content: nil}},
			wantMin:  4,
			wantMax:  4,
		},
		{
			name: "single text message 40 chars",
			// 40 chars / 4 = 10 tokens + 4 overhead = 14
			messages: []llm.Message{llm.NewUserMessage(strings.Repeat("a", 40))},
			wantMin:  14,
			wantMax:  14,
		},
		{
			name: "two messages",
			// msg1: 4 + 40/4=10 = 14; msg2: 4 + 20/4=5 = 9; total=23
			messages: []llm.Message{
				llm.NewUserMessage(strings.Repeat("a", 40)),
				llm.NewAssistantMessage(strings.Repeat("b", 20)),
			},
			wantMin: 23,
			wantMax: 23,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cw.EstimateTokens(tc.messages)
			if got < tc.wantMin || got > tc.wantMax {
				t.Errorf("EstimateTokens = %d, want [%d, %d]", got, tc.wantMin, tc.wantMax)
			}
		})
	}
}

func TestContextWindowFit_UnderBudget(t *testing.T) {
	cw := NewContextWindow()
	ctx := context.Background()

	// 3 short messages — well under any reasonable budget.
	messages := []llm.Message{
		llm.NewUserMessage("hello"),
		llm.NewAssistantMessage("hi there"),
		llm.NewUserMessage("how are you?"),
	}

	result, err := cw.Fit(ctx, messages, 100_000)
	if err != nil {
		t.Fatalf("Fit returned error: %v", err)
	}

	// microCompress may drop empty blocks but these messages all have content.
	if len(result) != len(messages) {
		t.Errorf("expected %d messages returned unchanged, got %d", len(messages), len(result))
	}
}

func TestContextWindowFit_EmptyBlocksRemoved(t *testing.T) {
	cw := NewContextWindow()
	ctx := context.Background()

	// A message with a whitespace-only text block should be dropped by microCompress.
	whitespaceMsg := llm.Message{
		Role:    "assistant",
		Content: []llm.ContentBlock{llm.TextBlock{Text: "   "}},
	}
	messages := []llm.Message{
		llm.NewUserMessage("hello"),
		whitespaceMsg,
		llm.NewAssistantMessage("response"),
	}

	result, err := cw.Fit(ctx, messages, 100_000)
	if err != nil {
		t.Fatalf("Fit returned error: %v", err)
	}

	// The whitespace message should have been removed.
	if len(result) != 2 {
		t.Errorf("expected 2 messages after micro-compress, got %d", len(result))
	}
}

func TestContextWindowFit_Trim(t *testing.T) {
	cw := NewContextWindow()
	ctx := context.Background()

	// snipSafetyMarginTokens = 3000, so effective snip budget = maxTokens - 3000.
	// We need: estimated(messages) > maxTokens AND maxTokens > 3000.
	//
	// Choose maxTokens = 3100 → effective budget = 100 tokens.
	// Each message: 4 overhead + len(text)/4.
	// Use 80-char body → 4 + 20 = 24 tokens per message.
	// 130 messages × 24 = 3120 > 3100 (triggers snip).
	// Within the 100-token budget: 4 messages × 24 = 96 ≤ 100, 5 × 24 = 120 > 100.
	// So snip keeps the 4 most-recent messages.
	const (
		maxTokens = 3100
		msgCount  = 130
		bodyLen   = 80
	)

	body := strings.Repeat("x", bodyLen)
	messages := make([]llm.Message, msgCount)
	for i := range messages {
		if i%2 == 0 {
			messages[i] = llm.NewUserMessage(body)
		} else {
			messages[i] = llm.NewAssistantMessage(body)
		}
	}

	result, err := cw.Fit(ctx, messages, maxTokens)
	if err != nil {
		t.Fatalf("Fit returned error: %v", err)
	}

	if len(result) >= len(messages) {
		t.Errorf("expected trimming to reduce message count from %d, got %d", len(messages), len(result))
	}

	// The last message must always be preserved.
	last := result[len(result)-1]
	if last.Text() != body {
		t.Errorf("last message text not preserved after trim")
	}
}

func TestMessageImportance(t *testing.T) {
	tests := []struct {
		name    string
		msg     llm.Message
		wantMin int
	}{
		{
			name:    "plain text",
			msg:     llm.NewUserMessage("hello"),
			wantMin: 1,
		},
		{
			name: "tool use block",
			msg: llm.Message{
				Role: llm.RoleAssistant,
				Content: []llm.ContentBlock{
					llm.ToolUseBlock{ID: "t1", Name: "bash", Input: []byte(`{}`)},
				},
			},
			wantMin: 4, // 1 base + 3 tool
		},
		{
			name: "error result",
			msg: llm.Message{
				Role: llm.RoleUser,
				Content: []llm.ContentBlock{
					llm.ToolResultBlock{ToolUseID: "t1", Content: "failed", IsError: true},
				},
			},
			wantMin: 6, // 1 base + 2 tool_result + 3 error
		},
		{
			name:    "text with decision marker",
			msg:     llm.NewAssistantMessage("Decision: we will use PostgreSQL"),
			wantMin: 3, // 1 base + 2 marker
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := messageImportance(tt.msg)
			if got < tt.wantMin {
				t.Errorf("messageImportance = %d, want >= %d", got, tt.wantMin)
			}
		})
	}
}

func TestCompressMessages_UnderBudget(t *testing.T) {
	cw := NewContextWindow()
	msgs := []llm.Message{
		llm.NewUserMessage("hi"),
		llm.NewAssistantMessage("hello"),
	}
	// Provider is never called since messages fit within budget.
	provider := &mockProvider{}
	result, err := cw.CompressMessages(context.Background(), provider, msgs, 100_000)
	if err != nil {
		t.Fatalf("CompressMessages: %v", err)
	}
	if len(result) != len(msgs) {
		t.Errorf("message count = %d, want %d", len(result), len(msgs))
	}
}

func TestCompressMessages_LLMSummary(t *testing.T) {
	cw := NewContextWindow()

	// Construct a message list that triggers flatSummarize (guaranteeing a provider call).
	// flatSummarize is used when toCompress produces only 1 topic segment.
	// One user message followed by many assistant messages = 1 segment.
	// keepCount = recentTurnsToKeep*2 = 8; we need len(msgs) > 8 so toCompress is non-empty.
	// Each message: 4 overhead + bodyLen/4 tokens. bodyLen=400 → 104 tokens each.
	// 12 messages → 1248 tokens; maxTokens=200 → threshold=160 → triggers LLM summary.
	const bodyLen = 400
	body := strings.Repeat("a", bodyLen)
	msgs := make([]llm.Message, 12)
	// First message is user (starts the single topic segment in toCompress).
	msgs[0] = llm.NewUserMessage(body)
	// Remaining toCompress messages are all assistant — no new segments start.
	for i := 1; i < 4; i++ {
		msgs[i] = llm.NewAssistantMessage(body)
	}
	// Last 8 messages are the "recent" portion kept as-is.
	for i := 4; i < 12; i++ {
		if i%2 == 0 {
			msgs[i] = llm.NewUserMessage(body)
		} else {
			msgs[i] = llm.NewAssistantMessage(body)
		}
	}

	summaryCalled := false
	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			summaryCalled = true
			return &llm.ChatResponse{Content: "Summary of earlier conversation"}, nil
		},
	}

	result, err := cw.CompressMessages(context.Background(), provider, msgs, 200)
	if err != nil {
		t.Fatalf("CompressMessages: %v", err)
	}
	if !summaryCalled {
		t.Error("expected LLM summarization to be called")
	}
	if len(result) >= len(msgs) {
		t.Errorf("expected compression to reduce count from %d, got %d", len(msgs), len(result))
	}
}

func TestCompressMessages_OnAutoCompressFires(t *testing.T) {
	cw := NewContextWindow()

	const bodyLen = 400
	body := strings.Repeat("a", bodyLen)
	msgs := make([]llm.Message, 12)
	msgs[0] = llm.NewUserMessage(body)
	for i := 1; i < 4; i++ {
		msgs[i] = llm.NewAssistantMessage(body)
	}
	for i := 4; i < 12; i++ {
		if i%2 == 0 {
			msgs[i] = llm.NewUserMessage(body)
		} else {
			msgs[i] = llm.NewAssistantMessage(body)
		}
	}

	callbackCount := 0
	cw.OnAutoCompress(func() { callbackCount++ })

	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{Content: "Summary"}, nil
		},
	}

	if _, err := cw.CompressMessages(context.Background(), provider, msgs, 200); err != nil {
		t.Fatalf("CompressMessages: %v", err)
	}
	if callbackCount == 0 {
		t.Fatalf("expected OnAutoCompress to fire at least once after Stage 2 LLM summary, got 0")
	}
}

func TestCompressMessages_OnAutoCompressNotFiredUnderBudget(t *testing.T) {
	cw := NewContextWindow()

	callbackCount := 0
	cw.OnAutoCompress(func() { callbackCount++ })

	msgs := []llm.Message{llm.NewUserMessage("short"), llm.NewAssistantMessage("ok")}
	if _, err := cw.CompressMessages(context.Background(), &mockProvider{}, msgs, 100_000); err != nil {
		t.Fatalf("CompressMessages: %v", err)
	}
	if callbackCount != 0 {
		t.Fatalf("expected OnAutoCompress not to fire when under budget, got %d", callbackCount)
	}
}

func TestCompressMessages_FallbackToSnip(t *testing.T) {
	cw := NewContextWindow()

	const bodyLen = 400
	body := strings.Repeat("b", bodyLen)
	msgs := make([]llm.Message, 20)
	for i := range msgs {
		if i%2 == 0 {
			msgs[i] = llm.NewUserMessage(body)
		} else {
			msgs[i] = llm.NewAssistantMessage(body)
		}
	}

	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			return nil, fmt.Errorf("provider unavailable")
		},
	}

	result, err := cw.CompressMessages(context.Background(), provider, msgs, 200)
	if err != nil {
		t.Fatalf("CompressMessages with provider error: %v", err)
	}
	// Should fall back to snip — result must be smaller than original.
	if len(result) >= len(msgs) {
		t.Errorf("expected snip to reduce count from %d, got %d", len(msgs), len(result))
	}
}

func TestCompressMessages_UsesNewSessionPromptWhenNoPriorSummary(t *testing.T) {
	cw := NewContextWindow()
	msgs := newLargeCompressionMessages("new-session")

	var capturedReq llm.ChatRequest
	provider := &mockProvider{
		chatFn: func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			capturedReq = req
			return &llm.ChatResponse{Content: testStructuredSummary("Continue updating the summary.")}, nil
		},
	}

	if _, err := cw.CompressMessages(context.Background(), provider, msgs, structuredCompressionMaxTokens); err != nil {
		t.Fatalf("CompressMessages: %v", err)
	}
	if !strings.Contains(capturedReq.System, "You are compressing a conversation.") {
		t.Fatalf("expected new-session prompt, got %q", capturedReq.System)
	}
	if strings.Contains(capturedReq.System, "You are updating a structured conversation summary.") {
		t.Fatalf("expected new-session prompt only, got %q", capturedReq.System)
	}
	if !strings.Contains(capturedReq.System, "new-session-0") {
		t.Fatalf("expected prompt to contain injected conversation transcript, got %q", capturedReq.System)
	}
}

func TestCompressMessages_UsesIterativePromptWhenPriorSummaryPresent(t *testing.T) {
	cw := NewContextWindow()
	priorSummary := testStructuredSummary("Audit the compression pipeline.")
	msgs := newLargeCompressionMessagesWithPriorSummary(priorSummary, "iterative")

	var capturedReq llm.ChatRequest
	provider := &mockProvider{
		chatFn: func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			capturedReq = req
			return &llm.ChatResponse{Content: testStructuredSummary("Merge the new messages into the existing summary.")}, nil
		},
	}

	if _, err := cw.CompressMessages(context.Background(), provider, msgs, structuredCompressionMaxTokens); err != nil {
		t.Fatalf("CompressMessages: %v", err)
	}
	if !strings.Contains(capturedReq.System, "You are updating a structured conversation summary.") {
		t.Fatalf("expected iterative prompt, got %q", capturedReq.System)
	}
	if !strings.Contains(capturedReq.System, "## 9. Next action\nAudit the compression pipeline.") {
		t.Fatalf("expected prompt to inject existing summary, got %q", capturedReq.System)
	}
	if !strings.Contains(capturedReq.System, "iterative-1") {
		t.Fatalf("expected prompt to inject new messages, got %q", capturedReq.System)
	}
}

func TestCompressMessages_OutputIsStructuredSummary(t *testing.T) {
	cw := NewContextWindow()
	msgs := newLargeCompressionMessages("structured-output")
	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{Content: testStructuredSummary("Run the remaining regression tests.")}, nil
		},
	}

	result, err := cw.CompressMessages(context.Background(), provider, msgs, structuredCompressionMaxTokens)
	if err != nil {
		t.Fatalf("CompressMessages: %v", err)
	}
	if len(result) != 9 {
		t.Fatalf("message count = %d, want 9 (1 summary + 8 recent messages)", len(result))
	}
	if result[0].Role != llm.RoleAssistant {
		t.Fatalf("summary role = %q, want assistant", result[0].Role)
	}
	if _, ok := parseStructuredSummary(result[0].Text()); !ok {
		t.Fatalf("expected structured summary output, got %q", result[0].Text())
	}
	structuredCount := 0
	for _, msg := range result {
		if isStructuredSummaryMessage(msg) {
			structuredCount++
		}
	}
	if structuredCount != 1 {
		t.Fatalf("structured summary count = %d, want 1", structuredCount)
	}
}

func TestCompressMessages_MalformedOutputFallsBackToLegacy(t *testing.T) {
	cw := NewContextWindow()
	msgs := newLargeCompressionMessages("fallback")

	var requests []llm.ChatRequest
	provider := &mockProvider{
		chatFn: func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			requests = append(requests, req)
			if len(requests) == 1 {
				return &llm.ChatResponse{Content: "one-line malformed summary"}, nil
			}
			return &llm.ChatResponse{Content: "legacy fallback summary"}, nil
		},
	}

	result, err := cw.CompressMessages(context.Background(), provider, msgs, structuredCompressionMaxTokens)
	if err != nil {
		t.Fatalf("CompressMessages: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(requests))
	}
	if !strings.Contains(requests[0].System, "You are compressing a conversation.") {
		t.Fatalf("expected structured prompt first, got %q", requests[0].System)
	}
	if requests[1].System != legacyUnstructuredSummaryPrompt {
		t.Fatalf("legacy prompt = %q, want %q", requests[1].System, legacyUnstructuredSummaryPrompt)
	}
	if got := result[0].Text(); got != "legacy fallback summary" {
		t.Fatalf("summary text = %q, want legacy fallback output", got)
	}
	if isStructuredSummaryMessage(result[0]) {
		t.Fatal("expected fallback summary to remain unstructured")
	}
	if len(result) != 9 {
		t.Fatalf("message count = %d, want 9 (1 fallback summary + 8 recent messages)", len(result))
	}
	if result[0].Role != llm.RoleAssistant {
		t.Fatalf("summary role = %q, want assistant", result[0].Role)
	}
	if len(result[0].Content) != 1 || result[0].Text() == "" {
		t.Fatal("expected fallback output to be injected into a single assistant message")
	}
	if len(result[1:]) != 8 {
		t.Fatalf("recent tail length = %d, want 8", len(result[1:]))
	}
}

func TestCompressMessages_PreservesOnAutoCompressCallback(t *testing.T) {
	cw := NewContextWindow()
	msgs := newLargeCompressionMessages("callback")

	callbackCount := 0
	cw.OnAutoCompress(func() { callbackCount++ })

	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{Content: testStructuredSummary("Continue with verification.")}, nil
		},
	}

	if _, err := cw.CompressMessages(context.Background(), provider, msgs, structuredCompressionMaxTokens); err != nil {
		t.Fatalf("CompressMessages: %v", err)
	}
	if callbackCount == 0 {
		t.Fatal("expected OnAutoCompress to fire after successful structured compression")
	}
}

func TestCompressMessages_SnipsWhenOnlyStructuredSummaryRemains(t *testing.T) {
	cw := NewContextWindow()
	msgs := newLargeCompressionMessages("summary-loop")

	callCount := 0
	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			callCount++
			return &llm.ChatResponse{Content: testStructuredSummary("Keep working through the recent tail.")}, nil
		},
	}

	result, err := cw.CompressMessages(context.Background(), provider, msgs, 200)
	if err != nil {
		t.Fatalf("CompressMessages: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("provider calls = %d, want 1 before snip fallback", callCount)
	}
	if len(result) != 1 {
		t.Fatalf("message count = %d, want 1 after snip fallback", len(result))
	}
	if strings.Contains(result[0].Text(), "# Session Summary") {
		t.Fatalf("expected snip fallback to keep the newest message, got %q", result[0].Text())
	}
}

func TestCompressMessages_LegacyFallbackFailureSnipsInsteadOfKeepingMalformedSummary(t *testing.T) {
	cw := NewContextWindow()
	msgs := newLargeCompressionMessages("legacy-fallback-error")

	callCount := 0
	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			callCount++
			if callCount == 1 {
				return &llm.ChatResponse{Content: "malformed structured output"}, nil
			}
			return nil, fmt.Errorf("legacy fallback unavailable")
		},
	}

	result, err := cw.CompressMessages(context.Background(), provider, msgs, 200)
	if err != nil {
		t.Fatalf("CompressMessages: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("provider calls = %d, want 2 (structured + legacy fallback)", callCount)
	}
	if len(result) != 1 {
		t.Fatalf("message count = %d, want 1 after snip fallback", len(result))
	}
	if got := result[0].Text(); strings.Contains(got, "malformed structured output") {
		t.Fatalf("expected malformed output to be discarded, got %q", got)
	}
}

func TestContextWindowFit_TrimKeepsRecent(t *testing.T) {
	cw := NewContextWindow()
	ctx := context.Background()

	// Build 130 messages that total > 3100 tokens (same budget as Trim test).
	// Label the last 10 distinctly so we can verify the tail is preserved.
	const (
		maxTokens = 3100
		totalMsgs = 130
		bodyLen   = 80
	)
	body := strings.Repeat("a", bodyLen)

	messages := make([]llm.Message, totalMsgs)
	for i := range messages {
		if i < totalMsgs-10 {
			if i%2 == 0 {
				messages[i] = llm.NewUserMessage(body)
			} else {
				messages[i] = llm.NewAssistantMessage(body)
			}
		} else {
			// Last 10 messages have a unique label.
			label := fmt.Sprintf("recent-%d-", i) + strings.Repeat("a", bodyLen-20)
			if i%2 == 0 {
				messages[i] = llm.NewUserMessage(label)
			} else {
				messages[i] = llm.NewAssistantMessage(label)
			}
		}
	}

	result, err := cw.Fit(ctx, messages, maxTokens)
	if err != nil {
		t.Fatalf("Fit error: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("result is empty")
	}

	// Result must be a contiguous suffix of the original slice.
	offset := len(messages) - len(result)
	for i, m := range result {
		orig := messages[offset+i]
		if m.Text() != orig.Text() {
			t.Errorf("result[%d] text mismatch: got %q, want %q", i, m.Text(), orig.Text())
		}
	}
}

const structuredCompressionMaxTokens = 1240

func newLargeCompressionMessages(prefix string) []llm.Message {
	const bodyLen = 400
	msgs := make([]llm.Message, 12)
	for i := range msgs {
		body := fmt.Sprintf("%s-%d-%s", prefix, i, strings.Repeat("x", bodyLen))
		if i == 0 {
			msgs[i] = llm.NewUserMessage(body)
			continue
		}
		if i < 4 {
			msgs[i] = llm.NewAssistantMessage(body)
			continue
		}
		if i%2 == 0 {
			msgs[i] = llm.NewUserMessage(body)
		} else {
			msgs[i] = llm.NewAssistantMessage(body)
		}
	}
	return msgs
}

func newLargeCompressionMessagesWithPriorSummary(summary, prefix string) []llm.Message {
	msgs := newLargeCompressionMessages(prefix)
	msgs[0] = llm.NewAssistantMessage(summary)
	return msgs
}

// TestContextWindowCompressMessages_PostCompressionValidation guards the P1
// validation invariant: when the LLM summarizer produces no usable output
// (empty content across both structured and legacy paths) CompressMessages
// must still return at least one message with a populated role and non-empty
// content — via the snip fallback. This prevents invalid compression from
// propagating an empty assistant shell into the next turn.
func TestContextWindowCompressMessages_PostCompressionValidation(t *testing.T) {
	cw := NewContextWindow()
	ctx := context.Background()

	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{Content: ""}, nil
		},
	}

	messages := make([]llm.Message, 0, 30)
	body := strings.Repeat("payload ", 25) // 200 chars → ~50 prose tokens per msg
	for i := 0; i < 30; i++ {
		if i%2 == 0 {
			messages = append(messages, llm.NewUserMessage(body))
		} else {
			messages = append(messages, llm.NewAssistantMessage(body))
		}
	}

	result, err := cw.CompressMessages(ctx, provider, messages, 500)
	if err != nil {
		t.Fatalf("CompressMessages: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("result is empty, want at least 1 message")
	}
	if result[0].Role == "" {
		t.Errorf("first message has empty role: %+v", result[0])
	}
	if strings.TrimSpace(result[0].Text()) == "" {
		t.Errorf("first message has empty content; expected snip fallback to preserve a real message. Got %+v", result[0])
	}
}
