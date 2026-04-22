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

// TestMessage_SourceConstants pins the Phase L1.1 source enum. Future
// consumers (sanitisers, observability, team workflows) rely on these
// exact strings to classify message origin, so changing them is a
// breaking change to the persisted JSONL schema.
func TestMessage_SourceConstants(t *testing.T) {
	if SourceChat != "chat" {
		t.Errorf("SourceChat = %q, want %q", SourceChat, "chat")
	}
	if SourceTask != "task" {
		t.Errorf("SourceTask = %q, want %q", SourceTask, "task")
	}
}

// TestMessage_MarshalPersistIncludesSource is the core Phase L1.1
// contract: MarshalPersist must emit `"source":"<value>"` whenever the
// field is set, so session JSONL records carry provenance that load-side
// code can read back.
func TestMessage_MarshalPersistIncludesSource(t *testing.T) {
	m := Message{
		Role:    RoleUser,
		Source:  SourceChat,
		Content: []ContentBlock{TextBlock{Text: "hi"}},
	}
	data, err := m.MarshalPersist()
	if err != nil {
		t.Fatalf("MarshalPersist error: %v", err)
	}
	if !contains(data, `"source":"chat"`) {
		t.Errorf("MarshalPersist output missing source field: %s", data)
	}
}

// TestMessage_MarshalPersistOmitsEmptySource pins the `omitempty`
// contract: pre-L1 callers that never set Source must produce
// byte-for-byte identical persistence output to the legacy
// MarshalJSON shape so old JSONL files keep their format.
func TestMessage_MarshalPersistOmitsEmptySource(t *testing.T) {
	m := Message{
		Role:    RoleUser,
		Content: []ContentBlock{TextBlock{Text: "hi"}},
	}
	data, err := m.MarshalPersist()
	if err != nil {
		t.Fatalf("MarshalPersist error: %v", err)
	}
	if contains(data, `"source"`) {
		t.Errorf("MarshalPersist should omit empty source; got: %s", data)
	}
}

// TestMessage_MarshalJSONOmitsSource guards the LLM-wire boundary:
// Anthropic / OpenAI / Responses reject unknown top-level fields, so
// Source must never leak into the standard MarshalJSON output even
// when set on the Message value.
func TestMessage_MarshalJSONOmitsSource(t *testing.T) {
	m := Message{
		Role:    RoleUser,
		Source:  SourceChat,
		Content: []ContentBlock{TextBlock{Text: "hi"}},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	if contains(data, `"source"`) {
		t.Errorf("standard MarshalJSON must not carry Source (LLM wire pollution): %s", data)
	}
}

// TestMessage_SourceRoundTripThroughPersist pins the persistence
// round-trip: MarshalPersist → Unmarshal must preserve Source exactly.
func TestMessage_SourceRoundTripThroughPersist(t *testing.T) {
	original := Message{
		Role:    RoleAssistant,
		Source:  SourceTask,
		Content: []ContentBlock{TextBlock{Text: "done"}},
	}
	data, err := original.MarshalPersist()
	if err != nil {
		t.Fatalf("MarshalPersist error: %v", err)
	}
	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if decoded.Source != SourceTask {
		t.Errorf("round-trip Source = %q, want %q", decoded.Source, SourceTask)
	}
}

// TestMessage_UnmarshalLegacyJSONL guards pre-L1 backward compatibility:
// JSONL records written before L1 land (no source field) must decode
// with Source == "" so existing session histories continue to load.
func TestMessage_UnmarshalLegacyJSONL(t *testing.T) {
	legacy := []byte(`{"role":"user","content":[{"type":"text","text":"legacy"}]}`)
	var decoded Message
	if err := json.Unmarshal(legacy, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if decoded.Source != "" {
		t.Errorf("legacy record should decode to empty Source; got %q", decoded.Source)
	}
	if decoded.Role != RoleUser {
		t.Errorf("legacy record Role = %q, want %q", decoded.Role, RoleUser)
	}
	if len(decoded.Content) != 1 {
		t.Fatalf("legacy record Content len = %d, want 1", len(decoded.Content))
	}
}

func contains(data []byte, needle string) bool {
	return len(data) > 0 && len(needle) > 0 && indexOf(data, needle) >= 0
}

func indexOf(data []byte, needle string) int {
	n := []byte(needle)
	for i := 0; i+len(n) <= len(data); i++ {
		match := true
		for j := 0; j < len(n); j++ {
			if data[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
