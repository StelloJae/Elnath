package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestChatSystemPromptNode_IsChatTrueIncludesIdentity(t *testing.T) {
	t.Parallel()

	got, err := NewChatSystemPromptNode(96).Render(context.Background(), &RenderState{IsChat: true})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "Elnath") {
		t.Errorf("Render = %q, want to contain %q", got, "Elnath")
	}
	if !strings.Contains(got, "한국어") {
		t.Errorf("Render = %q, want Korean default guidance", got)
	}
	if !strings.Contains(got, "상세") {
		t.Errorf("Render = %q, want detailed-answer guidance", got)
	}
}

func TestChatSystemPromptNode_IsChatFalseReturnsEmpty(t *testing.T) {
	t.Parallel()

	got, err := NewChatSystemPromptNode(96).Render(context.Background(), &RenderState{IsChat: false})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string for task path (IsChat=false)", got)
	}
}

func TestChatSystemPromptNode_NilStateReturnsEmpty(t *testing.T) {
	t.Parallel()

	got, err := NewChatSystemPromptNode(96).Render(context.Background(), nil)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string on nil state", got)
	}
}

func TestChatSystemPromptNode_PriorityAndName(t *testing.T) {
	t.Parallel()

	n := NewChatSystemPromptNode(96)
	if n.Name() != "chat_system" {
		t.Errorf("Name = %q, want %q", n.Name(), "chat_system")
	}
	if n.Priority() != 96 {
		t.Errorf("Priority = %d, want 96", n.Priority())
	}
}
