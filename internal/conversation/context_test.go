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
		maxTokens  = 3100
		msgCount   = 130
		bodyLen    = 80
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

func TestSegmentByTopic(t *testing.T) {
	messages := []llm.Message{
		llm.NewUserMessage("topic 1"),
		llm.NewAssistantMessage("answer 1"),
		llm.NewUserMessage("topic 2"),
		llm.NewAssistantMessage("answer 2a"),
		llm.NewAssistantMessage("answer 2b"),
		llm.NewUserMessage("topic 3"),
		llm.NewAssistantMessage("answer 3"),
	}

	segments := segmentByTopic(messages)

	if len(segments) != 3 {
		t.Fatalf("got %d segments, want 3", len(segments))
	}
	if len(segments[0].messages) != 2 {
		t.Errorf("segment 0: got %d messages, want 2", len(segments[0].messages))
	}
	if len(segments[1].messages) != 3 {
		t.Errorf("segment 1: got %d messages, want 3", len(segments[1].messages))
	}
	if len(segments[2].messages) != 2 {
		t.Errorf("segment 2: got %d messages, want 2", len(segments[2].messages))
	}
}

func TestSegmentByTopic_Empty(t *testing.T) {
	segments := segmentByTopic(nil)
	if len(segments) != 0 {
		t.Errorf("got %d segments for nil input, want 0", len(segments))
	}
}

func TestSegmentByTopic_AssistantOnly(t *testing.T) {
	messages := []llm.Message{
		llm.NewAssistantMessage("only assistant messages"),
		llm.NewAssistantMessage("still going"),
	}
	segments := segmentByTopic(messages)
	if len(segments) != 1 {
		t.Errorf("got %d segments, want 1", len(segments))
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

func TestImportanceThreshold_Median(t *testing.T) {
	cw := NewContextWindow()
	segments := []topicSegment{
		{importance: 1},
		{importance: 5},
		{importance: 3},
	}
	// Sorted: [1, 3, 5] → median at index 1 = 3.
	got := cw.importanceThreshold(segments)
	if got != 3 {
		t.Errorf("importanceThreshold = %d, want 3", got)
	}
}

func TestImportanceThreshold_Single(t *testing.T) {
	cw := NewContextWindow()
	segments := []topicSegment{{importance: 7}}
	got := cw.importanceThreshold(segments)
	if got != 7 {
		t.Errorf("importanceThreshold = %d, want 7", got)
	}
}

func TestImportanceThreshold_Empty(t *testing.T) {
	cw := NewContextWindow()
	got := cw.importanceThreshold(nil)
	if got != 0 {
		t.Errorf("importanceThreshold(nil) = %d, want 0", got)
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
