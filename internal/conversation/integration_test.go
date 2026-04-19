package conversation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/llm"
)

// TestIntegration_LargeSession_CompressesBeforeOOM reproduces the original
// FU-SessionCompaction bug scenario end-to-end through the Manager: 30
// substantial messages (~45K estimated tokens) in a real session file, a mock
// provider that returns a valid 9-section structured summary, and a 50K
// provider context window. The manager must auto-compact before returning,
// shrinking the history well below the original count and keeping the total
// within the budget.
func TestIntegration_LargeSession_CompressesBeforeOOM(t *testing.T) {
	dir := t.TempDir()

	sess, err := agent.NewSession(dir)
	if err != nil {
		t.Fatalf("agent.NewSession: %v", err)
	}

	// Seed 30 messages, each ~2250 estimated tokens, for ~67K total — above
	// the 50K context window so CompressMessages enters the compaction loop.
	filler := strings.Repeat("word ", 1800) // ~9000 chars → ~2250 prose tokens
	msgs := make([]llm.Message, 0, 30)
	for i := 0; i < 30; i++ {
		body := fmt.Sprintf("entry-%03d %s", i, filler)
		if i%2 == 0 {
			msgs = append(msgs, llm.NewUserMessage(body))
		} else {
			msgs = append(msgs, llm.NewAssistantMessage(body))
		}
	}
	if err := sess.AppendMessages(msgs); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}

	provider := &mockProvider{
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{Content: buildStructuredSummaryFixture()}, nil
		},
	}

	cw := NewContextWindow()
	mgr := NewManager(nil, dir).
		WithProvider(provider).
		WithContextWindow(cw).
		WithProviderContextWindow(50_000).
		WithMemoryLimitMB(1024) // high enough that memory pressure does not fire

	result, _, err := mgr.SendMessage(context.Background(), sess.ID, "what next?")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// 30 seeded + 1 new user message = 31 pre-compaction; post-compaction
	// shrinks to summary + recent turn-pairs.
	if len(result) >= 30 {
		t.Errorf("post-compaction count = %d, want < 30", len(result))
	}
	if len(result) == 0 {
		t.Fatal("result is empty")
	}
	if !isStructuredSummaryMessage(result[0]) {
		t.Errorf("first message is not a structured summary: role=%q", result[0].Role)
	}

	estimated := cw.EstimateTokens(result)
	if estimated > 50_000 {
		t.Errorf("post-compaction tokens = %d, want <= 50000", estimated)
	}
	if estimated <= 0 {
		t.Fatalf("unexpected estimated tokens = %d", estimated)
	}
}
