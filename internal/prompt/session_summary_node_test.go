package prompt

import (
	"context"
	"strings"
	testing "testing"

	"github.com/stello/elnath/internal/llm"
)

func TestSessionSummaryNodeEmptyMessages(t *testing.T) {
	t.Parallel()

	got, err := NewSessionSummaryNode(10, 5, 200).Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestSessionSummaryNodeLastNUserMessages(t *testing.T) {
	t.Parallel()

	messages := []llm.Message{
		llm.NewUserMessage("u1"),
		llm.NewAssistantMessage("a1"),
		llm.NewUserMessage("u2"),
		llm.NewAssistantMessage("a2"),
		llm.NewUserMessage("u3"),
		llm.NewAssistantMessage("a3"),
		llm.NewUserMessage("u4"),
		llm.NewAssistantMessage("a4"),
		llm.NewUserMessage("u5"),
		llm.NewAssistantMessage("a5"),
		llm.NewUserMessage("u6"),
		llm.NewAssistantMessage("a6"),
	}

	got, err := NewSessionSummaryNode(10, 5, 200).Render(context.Background(), &RenderState{Messages: messages})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}

	for _, want := range []string{"u2", "u3", "u4", "u5", "u6"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render = %q, want substring %q", got, want)
		}
	}
	for _, unwanted := range []string{"u1", "a1", "a2", "a3", "a4", "a5", "a6"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("Render = %q, should not contain %q", got, unwanted)
		}
	}
}

func TestSessionSummaryNodeMaxCharsTruncation(t *testing.T) {
	t.Parallel()

	message := strings.Repeat("x", 1000)
	got, err := NewSessionSummaryNode(10, 5, 80).Render(context.Background(), &RenderState{
		Messages: []llm.Message{llm.NewUserMessage(message)},
	})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if len(got) > 80 {
		t.Fatalf("Render length = %d, want <= 80", len(got))
	}
	if got == "" {
		t.Fatal("Render returned empty string")
	}
}

func TestSessionSummaryNodeDefaultsWhenZero(t *testing.T) {
	t.Parallel()

	node := NewSessionSummaryNode(7, 0, 0)
	if node.maxMsgs != 5 {
		t.Fatalf("maxMsgs = %d, want 5", node.maxMsgs)
	}
	if node.maxChars != 800 {
		t.Fatalf("maxChars = %d, want 800", node.maxChars)
	}
}
