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
