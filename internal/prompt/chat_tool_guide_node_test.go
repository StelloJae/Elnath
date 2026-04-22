package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestChatToolGuideNode_IsChatTrueWithToolsIncludesGuide(t *testing.T) {
	t.Parallel()

	state := &RenderState{
		IsChat:         true,
		AvailableTools: []string{"web_search", "web_fetch", "read_file", "glob", "grep"},
	}
	got, err := NewChatToolGuideNode(78).Render(context.Background(), state)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}

	if !strings.Contains(got, "도구 사용 지침") {
		t.Errorf("Render missing '도구 사용 지침' heading:\n%s", got)
	}
	// Rule numbers 1..6 must all appear (chatToolGuideHeader canonical form).
	for i := 1; i <= 6; i++ {
		marker := []string{"1.", "2.", "3.", "4.", "5.", "6."}[i-1]
		if !strings.Contains(got, marker) {
			t.Errorf("Render missing rule marker %q:\n%s", marker, got)
		}
	}
	if !strings.Contains(got, "Sources:") {
		t.Errorf("Render missing Sources: citation marker:\n%s", got)
	}
}

func TestChatToolGuideNode_IsChatFalseReturnsEmpty(t *testing.T) {
	t.Parallel()

	got, err := NewChatToolGuideNode(78).Render(context.Background(), &RenderState{
		IsChat:         false,
		AvailableTools: []string{"web_search"},
	})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string for task path even when tools listed", got)
	}
}

func TestChatToolGuideNode_IsChatTrueEmptyToolsReturnsEmpty(t *testing.T) {
	t.Parallel()

	got, err := NewChatToolGuideNode(78).Render(context.Background(), &RenderState{
		IsChat:         true,
		AvailableTools: nil,
	})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string when chat has no tools wired (legacy stream)", got)
	}
}

func TestChatToolGuideNode_NilStateReturnsEmpty(t *testing.T) {
	t.Parallel()

	got, err := NewChatToolGuideNode(78).Render(context.Background(), nil)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string on nil state", got)
	}
}

func TestChatToolGuideNode_PriorityAndName(t *testing.T) {
	t.Parallel()

	n := NewChatToolGuideNode(78)
	if n.Name() != "chat_tool_guide" {
		t.Errorf("Name = %q, want %q", n.Name(), "chat_tool_guide")
	}
	if n.Priority() != 78 {
		t.Errorf("Priority = %d, want 78", n.Priority())
	}
}
