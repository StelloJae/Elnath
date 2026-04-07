package llm

import (
	"encoding/json"
	"testing"
)

func TestNewUserMessage(t *testing.T) {
	m := NewUserMessage("hello")
	if m.Role != RoleUser {
		t.Errorf("role = %q, want %q", m.Role, RoleUser)
	}
	if len(m.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(m.Content))
	}
	tb, ok := m.Content[0].(TextBlock)
	if !ok {
		t.Fatal("content[0] is not TextBlock")
	}
	if tb.Text != "hello" {
		t.Errorf("text = %q, want %q", tb.Text, "hello")
	}
}

func TestNewAssistantMessage(t *testing.T) {
	m := NewAssistantMessage("world")
	if m.Role != RoleAssistant {
		t.Errorf("role = %q, want %q", m.Role, RoleAssistant)
	}
	if len(m.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(m.Content))
	}
	tb, ok := m.Content[0].(TextBlock)
	if !ok {
		t.Fatal("content[0] is not TextBlock")
	}
	if tb.Text != "world" {
		t.Errorf("text = %q, want %q", tb.Text, "world")
	}
}

func TestMessageText(t *testing.T) {
	m := Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			TextBlock{Text: "hello "},
			ToolUseBlock{ID: "1", Name: "bash", Input: json.RawMessage(`{}`)},
			TextBlock{Text: "world"},
		},
	}
	got := m.Text()
	want := "hello world"
	if got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
}

func TestMessageJSON(t *testing.T) {
	original := Message{
		Role: RoleUser,
		Content: []ContentBlock{
			TextBlock{Text: "run this"},
			ToolUseBlock{ID: "abc", Name: "bash", Input: json.RawMessage(`{"cmd":"ls"}`)},
			ToolResultBlock{ToolUseID: "abc", Content: "file.go", IsError: false},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Role != original.Role {
		t.Errorf("role = %q, want %q", decoded.Role, original.Role)
	}
	if len(decoded.Content) != len(original.Content) {
		t.Fatalf("content len = %d, want %d", len(decoded.Content), len(original.Content))
	}

	if _, ok := decoded.Content[0].(TextBlock); !ok {
		t.Errorf("content[0] type = %T, want TextBlock", decoded.Content[0])
	}
	tu, ok := decoded.Content[1].(ToolUseBlock)
	if !ok {
		t.Fatalf("content[1] type = %T, want ToolUseBlock", decoded.Content[1])
	}
	if tu.ID != "abc" || tu.Name != "bash" {
		t.Errorf("ToolUseBlock = {%q %q}, want {abc bash}", tu.ID, tu.Name)
	}
	tr, ok := decoded.Content[2].(ToolResultBlock)
	if !ok {
		t.Fatalf("content[2] type = %T, want ToolResultBlock", decoded.Content[2])
	}
	if tr.ToolUseID != "abc" || tr.Content != "file.go" {
		t.Errorf("ToolResultBlock = {%q %q}, want {abc file.go}", tr.ToolUseID, tr.Content)
	}
}

func TestToolUseBlockType(t *testing.T) {
	b := ToolUseBlock{ID: "1", Name: "bash", Input: json.RawMessage(`{}`)}
	if b.BlockType() != "tool_use" {
		t.Errorf("BlockType() = %q, want %q", b.BlockType(), "tool_use")
	}
}
