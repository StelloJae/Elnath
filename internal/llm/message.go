package llm

import "encoding/json"

// Role constants for Message.Role.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

// NewTextMessage is an alias for NewUserMessage / NewAssistantMessage
// that accepts a role string, used by agent and session code.
func NewTextMessage(role, text string) Message {
	return Message{Role: role, Content: []ContentBlock{TextBlock{Text: text}}}
}

// TextContent returns the concatenated text from all TextBlocks (alias for Text).
func (m Message) TextContent() string { return m.Text() }

// ContentBlock is a discriminated union for message content.
type ContentBlock interface {
	BlockType() string
}

// TextBlock holds plain text content.
type TextBlock struct {
	Text string `json:"text"`
}

func (b TextBlock) BlockType() string { return "text" }

// ToolUseBlock represents a model-initiated tool call.
type ToolUseBlock struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func (b ToolUseBlock) BlockType() string { return "tool_use" }

// ToolResultBlock carries the output of a tool execution.
type ToolResultBlock struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

func (b ToolResultBlock) BlockType() string { return "tool_result" }

// Message is a single turn in a conversation.
// The message array is the ONLY state — no hidden state machines.
type Message struct {
	Role    string         `json:"role"` // "user" or "assistant"
	Content []ContentBlock `json:"content"`
}

// NewUserMessage constructs a user message with a single text block.
func NewUserMessage(text string) Message {
	return Message{Role: "user", Content: []ContentBlock{TextBlock{Text: text}}}
}

// NewAssistantMessage constructs an assistant message with a single text block.
func NewAssistantMessage(text string) Message {
	return Message{Role: "assistant", Content: []ContentBlock{TextBlock{Text: text}}}
}

// NewToolResultMessage constructs a user-role message carrying a tool_result block.
// Anthropic requires tool results to be sent as a user turn.
func NewToolResultMessage(toolUseID, content string, isError bool) Message {
	return Message{
		Role: "user",
		Content: []ContentBlock{ToolResultBlock{
			ToolUseID: toolUseID,
			Content:   content,
			IsError:   isError,
		}},
	}
}

// Text returns the concatenated text from all TextBlocks in the message.
func (m Message) Text() string {
	var out string
	for _, b := range m.Content {
		if tb, ok := b.(TextBlock); ok {
			out += tb.Text
		}
	}
	return out
}

// MarshalJSON serialises Message, tagging each block with its type field.
func (m Message) MarshalJSON() ([]byte, error) {
	type wire struct {
		Role    string            `json:"role"`
		Content []json.RawMessage `json:"content"`
	}
	w := wire{Role: m.Role}
	for _, b := range m.Content {
		tagged, err := marshalBlock(b)
		if err != nil {
			return nil, err
		}
		w.Content = append(w.Content, tagged)
	}
	return json.Marshal(w)
}

func marshalBlock(b ContentBlock) (json.RawMessage, error) {
	inner, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(inner, &m); err != nil {
		return nil, err
	}
	typeBytes, _ := json.Marshal(b.BlockType())
	m["type"] = typeBytes
	return json.Marshal(m)
}

// UnmarshalJSON deserialises Message, reconstructing typed ContentBlocks.
func (m *Message) UnmarshalJSON(data []byte) error {
	type wire struct {
		Role    string            `json:"role"`
		Content []json.RawMessage `json:"content"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	m.Role = w.Role
	for _, raw := range w.Content {
		var peek struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &peek); err != nil {
			return err
		}
		block, err := unmarshalBlock(peek.Type, raw)
		if err != nil {
			return err
		}
		m.Content = append(m.Content, block)
	}
	return nil
}

func unmarshalBlock(blockType string, raw json.RawMessage) (ContentBlock, error) {
	switch blockType {
	case "text":
		var b TextBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return b, nil
	case "tool_use":
		var b ToolUseBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return b, nil
	case "tool_result":
		var b ToolResultBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return b, nil
	default:
		return TextBlock{Text: string(raw)}, nil
	}
}
