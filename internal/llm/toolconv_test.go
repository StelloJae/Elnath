package llm

import (
	"encoding/json"
	"testing"
)

// --- AppendToolResult ---

func TestAppendToolResult(t *testing.T) {
	// Append to empty messages creates a new user message.
	msgs := AppendToolResult(nil, "id1", "output", false)
	if len(msgs) != 1 {
		t.Fatalf("len = %d, want 1", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("role = %q, want user", msgs[0].Role)
	}
	if len(msgs[0].Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(msgs[0].Content))
	}
	tr, ok := msgs[0].Content[0].(ToolResultBlock)
	if !ok {
		t.Fatalf("content[0] type = %T, want ToolResultBlock", msgs[0].Content[0])
	}
	if tr.ToolUseID != "id1" || tr.Content != "output" || tr.IsError {
		t.Errorf("got {%q %q %v}, want {id1 output false}", tr.ToolUseID, tr.Content, tr.IsError)
	}

	// Append a second result to the same user message (merge).
	msgs = AppendToolResult(msgs, "id2", "output2", true)
	if len(msgs) != 1 {
		t.Fatalf("after merge: len = %d, want 1", len(msgs))
	}
	if len(msgs[0].Content) != 2 {
		t.Fatalf("after merge: content len = %d, want 2", len(msgs[0].Content))
	}
	tr2, ok := msgs[0].Content[1].(ToolResultBlock)
	if !ok {
		t.Fatalf("content[1] type = %T, want ToolResultBlock", msgs[0].Content[1])
	}
	if tr2.ToolUseID != "id2" || !tr2.IsError {
		t.Errorf("got {%q %v}, want {id2 true}", tr2.ToolUseID, tr2.IsError)
	}
}

func TestAppendToolResultNewMessage(t *testing.T) {
	// When last message is an assistant message, a new user message is appended.
	existing := []Message{NewAssistantMessage("thinking")}
	msgs := AppendToolResult(existing, "id1", "result", false)
	if len(msgs) != 2 {
		t.Fatalf("len = %d, want 2", len(msgs))
	}
	if msgs[1].Role != "user" {
		t.Errorf("role = %q, want user", msgs[1].Role)
	}
	if len(msgs[1].Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(msgs[1].Content))
	}
	if _, ok := msgs[1].Content[0].(ToolResultBlock); !ok {
		t.Errorf("content[0] type = %T, want ToolResultBlock", msgs[1].Content[0])
	}
}

// --- ExtractToolUseBlocks ---

func TestExtractToolUseBlocks(t *testing.T) {
	m := Message{
		Role: "assistant",
		Content: []ContentBlock{
			TextBlock{Text: "here"},
			ToolUseBlock{ID: "t1", Name: "bash", Input: json.RawMessage(`{"cmd":"ls"}`)},
			ToolUseBlock{ID: "t2", Name: "read", Input: json.RawMessage(`{"path":"/"}`)},
		},
	}
	blocks := ExtractToolUseBlocks(m)
	if len(blocks) != 2 {
		t.Fatalf("len = %d, want 2", len(blocks))
	}
	if blocks[0].ID != "t1" || blocks[0].Name != "bash" {
		t.Errorf("blocks[0] = {%q %q}, want {t1 bash}", blocks[0].ID, blocks[0].Name)
	}
	if blocks[1].ID != "t2" || blocks[1].Name != "read" {
		t.Errorf("blocks[1] = {%q %q}, want {t2 read}", blocks[1].ID, blocks[1].Name)
	}
}

func TestExtractToolUseBlocksEmpty(t *testing.T) {
	m := Message{
		Role:    "assistant",
		Content: []ContentBlock{TextBlock{Text: "no tools here"}},
	}
	blocks := ExtractToolUseBlocks(m)
	if blocks != nil {
		t.Errorf("got %v, want nil", blocks)
	}
}

// --- BuildAssistantMessage ---

func TestBuildAssistantMessage(t *testing.T) {
	t.Run("text and tool calls", func(t *testing.T) {
		msg := BuildAssistantMessage(
			[]string{"Hello ", "world"},
			[]CompletedToolCall{{ID: "c1", Name: "bash", Input: `{"cmd":"ls"}`}},
		)
		if msg.Role != "assistant" {
			t.Errorf("role = %q, want assistant", msg.Role)
		}
		if len(msg.Content) != 2 {
			t.Fatalf("content len = %d, want 2", len(msg.Content))
		}
		tb, ok := msg.Content[0].(TextBlock)
		if !ok {
			t.Fatalf("content[0] type = %T, want TextBlock", msg.Content[0])
		}
		if tb.Text != "Hello world" {
			t.Errorf("text = %q, want %q", tb.Text, "Hello world")
		}
		tu, ok := msg.Content[1].(ToolUseBlock)
		if !ok {
			t.Fatalf("content[1] type = %T, want ToolUseBlock", msg.Content[1])
		}
		if tu.ID != "c1" || tu.Name != "bash" {
			t.Errorf("tool_use = {%q %q}, want {c1 bash}", tu.ID, tu.Name)
		}
	})

	t.Run("text only", func(t *testing.T) {
		msg := BuildAssistantMessage([]string{"hi"}, nil)
		if len(msg.Content) != 1 {
			t.Fatalf("content len = %d, want 1", len(msg.Content))
		}
		if _, ok := msg.Content[0].(TextBlock); !ok {
			t.Errorf("content[0] type = %T, want TextBlock", msg.Content[0])
		}
	})

	t.Run("tool calls only", func(t *testing.T) {
		msg := BuildAssistantMessage(nil, []CompletedToolCall{{ID: "c2", Name: "read", Input: ""}})
		if len(msg.Content) != 1 {
			t.Fatalf("content len = %d, want 1", len(msg.Content))
		}
		tu, ok := msg.Content[0].(ToolUseBlock)
		if !ok {
			t.Fatalf("content[0] type = %T, want ToolUseBlock", msg.Content[0])
		}
		// Empty input should default to {}
		if string(tu.Input) != "{}" {
			t.Errorf("input = %q, want {}", string(tu.Input))
		}
	})

	t.Run("empty", func(t *testing.T) {
		msg := BuildAssistantMessage(nil, nil)
		if msg.Role != "assistant" {
			t.Errorf("role = %q, want assistant", msg.Role)
		}
		if len(msg.Content) != 0 {
			t.Errorf("content len = %d, want 0", len(msg.Content))
		}
	})
}

// --- ToAnthropicTools ---

func TestToAnthropicTools(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`)
	tools := []ToolDef{
		{Name: "bash", Description: "run shell", InputSchema: schema},
		{Name: "read", Description: "read file", InputSchema: json.RawMessage(`{}`)},
	}
	out := ToAnthropicTools(tools)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0]["name"] != "bash" {
		t.Errorf("name = %v, want bash", out[0]["name"])
	}
	if out[0]["description"] != "run shell" {
		t.Errorf("description = %v, want run shell", out[0]["description"])
	}
	if out[0]["input_schema"] == nil {
		t.Error("input_schema is nil")
	}
}

func TestToAnthropicToolsEmptySchema(t *testing.T) {
	// nil InputSchema should produce a default empty object schema.
	tools := []ToolDef{{Name: "noop", Description: "does nothing", InputSchema: nil}}
	out := ToAnthropicTools(tools)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	schema, ok := out[0]["input_schema"].(map[string]interface{})
	if !ok {
		t.Fatalf("input_schema type = %T, want map", out[0]["input_schema"])
	}
	if schema["type"] != "object" {
		t.Errorf("type = %v, want object", schema["type"])
	}
}

func TestToAnthropicToolsEmpty(t *testing.T) {
	if out := ToAnthropicTools(nil); out != nil {
		t.Errorf("got %v, want nil", out)
	}
}

// --- ToAnthropicMessages ---

func TestToAnthropicMessages(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{TextBlock{Text: "hello"}}},
		{
			Role: "assistant",
			Content: []ContentBlock{
				TextBlock{Text: "I will run bash"},
				ToolUseBlock{ID: "t1", Name: "bash", Input: json.RawMessage(`{"cmd":"ls"}`)},
			},
		},
		{
			Role: "user",
			Content: []ContentBlock{
				ToolResultBlock{ToolUseID: "t1", Content: "file.go", IsError: false},
			},
		},
	}
	out := ToAnthropicMessages(msgs)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}

	// user text message
	userBlocks := out[0]["content"].([]map[string]interface{})
	if userBlocks[0]["type"] != "text" {
		t.Errorf("block type = %v, want text", userBlocks[0]["type"])
	}

	// assistant message with text + tool_use
	asstBlocks := out[1]["content"].([]map[string]interface{})
	if len(asstBlocks) != 2 {
		t.Fatalf("assistant blocks len = %d, want 2", len(asstBlocks))
	}
	if asstBlocks[1]["type"] != "tool_use" {
		t.Errorf("block type = %v, want tool_use", asstBlocks[1]["type"])
	}
	if asstBlocks[1]["id"] != "t1" {
		t.Errorf("id = %v, want t1", asstBlocks[1]["id"])
	}

	// tool_result message
	trBlocks := out[2]["content"].([]map[string]interface{})
	if trBlocks[0]["type"] != "tool_result" {
		t.Errorf("block type = %v, want tool_result", trBlocks[0]["type"])
	}
	if trBlocks[0]["tool_use_id"] != "t1" {
		t.Errorf("tool_use_id = %v, want t1", trBlocks[0]["tool_use_id"])
	}
	if _, hasErr := trBlocks[0]["is_error"]; hasErr {
		t.Error("is_error should be absent for non-error result")
	}
}

// --- ToOpenAITools ---

func TestToOpenAITools(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	tools := []ToolDef{{Name: "read", Description: "read a file", InputSchema: schema}}
	out := ToOpenAITools(tools)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if out[0]["type"] != "function" {
		t.Errorf("type = %v, want function", out[0]["type"])
	}
	fn, ok := out[0]["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("function type = %T, want map", out[0]["function"])
	}
	if fn["name"] != "read" {
		t.Errorf("name = %v, want read", fn["name"])
	}
	if fn["description"] != "read a file" {
		t.Errorf("description = %v, want read a file", fn["description"])
	}
	if fn["parameters"] == nil {
		t.Error("parameters is nil")
	}
}

func TestToOpenAIToolsEmpty(t *testing.T) {
	if out := ToOpenAITools(nil); out != nil {
		t.Errorf("got %v, want nil", out)
	}
}

// --- ToOpenAIMessages ---

func TestToOpenAIMessages(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{TextBlock{Text: "hi"}}},
		{Role: "assistant", Content: []ContentBlock{TextBlock{Text: "hello"}}},
		{
			Role: "assistant",
			Content: []ContentBlock{
				TextBlock{Text: "running"},
				ToolUseBlock{ID: "c1", Name: "bash", Input: json.RawMessage(`{"cmd":"pwd"}`)},
			},
		},
		{
			Role: "user",
			Content: []ContentBlock{
				ToolResultBlock{ToolUseID: "c1", Content: "/home", IsError: false},
			},
		},
	}
	out := ToOpenAIMessages(msgs)

	// user message
	if out[0]["role"] != "user" || out[0]["content"] != "hi" {
		t.Errorf("user msg = %v", out[0])
	}

	// plain assistant
	if out[1]["role"] != "assistant" || out[1]["content"] != "hello" {
		t.Errorf("assistant msg = %v", out[1])
	}

	// assistant with tool_calls
	if out[2]["role"] != "assistant" {
		t.Errorf("role = %v, want assistant", out[2]["role"])
	}
	tcs, ok := out[2]["tool_calls"].([]map[string]interface{})
	if !ok {
		t.Fatalf("tool_calls type = %T, want []map", out[2]["tool_calls"])
	}
	if len(tcs) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(tcs))
	}
	if tcs[0]["id"] != "c1" {
		t.Errorf("tool_call id = %v, want c1", tcs[0]["id"])
	}

	// tool result → role "tool"
	// ToOpenAIMessages emits a "tool" message for each ToolResultBlock in any message.
	// The user message with ToolResultBlock produces one "tool" entry.
	var toolMsg map[string]interface{}
	for _, m := range out {
		if m["role"] == "tool" {
			toolMsg = m
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("no tool role message found")
	}
	if toolMsg["tool_call_id"] != "c1" {
		t.Errorf("tool_call_id = %v, want c1", toolMsg["tool_call_id"])
	}
	if toolMsg["content"] != "/home" {
		t.Errorf("content = %v, want /home", toolMsg["content"])
	}
}
