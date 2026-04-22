package llm

import "encoding/json"

// Role constants for Message.Role.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

// Source constants for Message.Source (Phase L1.1 — universal message schema).
// Source records where the message originated so load-side sanitisers,
// auditors, and future team workflows can tell chat vs task vs team (vs
// pre-L1 legacy) messages apart without inferring from surrounding
// structure. The empty string is preserved as "unknown / legacy" —
// every JSONL record written before L1.1 reads back with Source == ""
// and load-side callers should treat that as the conservative task
// default.
const (
	SourceChat = "chat"
	SourceTask = "task"
	SourceTeam = "team"
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

// ThinkingBlock holds extended thinking content from the model.
type ThinkingBlock struct {
	Thinking string `json:"thinking"`
}

func (b ThinkingBlock) BlockType() string { return "thinking" }

// ImageBlock holds an image for multimodal messages.
type ImageBlock struct {
	MediaType string `json:"media_type"` // e.g. "image/jpeg", "image/png"
	Data      string `json:"data"`       // base64-encoded image data
}

func (b ImageBlock) BlockType() string { return "image" }

// Message is a single turn in a conversation.
// The message array is the ONLY state — no hidden state machines.
//
// Source (Phase L1.1) tags the origin of the message (chat/task/"").
// It is intentionally omitted from the LLM-wire MarshalJSON output below
// — Anthropic / OpenAI APIs reject unknown top-level fields — and only
// surfaces on the persistence path via MarshalPersist so session JSONL
// records keep the provenance that load-side sanitisers need.
type Message struct {
	Role    string         `json:"role"`
	Source  string         `json:"source,omitempty"`
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

// MarshalJSON serialises Message for the LLM-wire path (Anthropic, OpenAI,
// Responses). Source is intentionally dropped here — upstream provider
// APIs reject unknown top-level fields. Persistence callers should use
// MarshalPersist, which keeps Source in the payload.
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

// MarshalPersist serialises Message for session JSONL storage and any
// other Elnath-internal persistence surface. It mirrors MarshalJSON's
// content tagging but additionally emits `"source"` when Source is set,
// so load-side readers (sanitisers, auditors, future team-aware
// consumers) can tell chat / task / legacy records apart. Empty Source
// stays omitted via `omitempty`, so pre-L1 callers that haven't yet set
// the field keep producing byte-for-byte identical output to the
// legacy MarshalJSON shape.
//
// Keeping persistence on its own method (rather than a flag on
// MarshalJSON) gives the write-side an intent-explicit API and leaves
// room for future persistence-only fields (schema version, write
// timestamp, hash chain) without touching the LLM-wire path.
func (m Message) MarshalPersist() ([]byte, error) {
	type wire struct {
		Role    string            `json:"role"`
		Source  string            `json:"source,omitempty"`
		Content []json.RawMessage `json:"content"`
	}
	w := wire{Role: m.Role, Source: m.Source}
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
	switch blk := b.(type) {
	case ImageBlock:
		// Anthropic image blocks use a nested source structure.
		return json.Marshal(map[string]interface{}{
			"type": "image",
			"source": map[string]string{
				"type":       "base64",
				"media_type": blk.MediaType,
				"data":       blk.Data,
			},
		})
	default:
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
}

// UnmarshalJSON deserialises Message, reconstructing typed ContentBlocks.
// Accepts both the LLM-wire payload (no source field) and persisted
// records from MarshalPersist (with source). A missing source field
// decodes to Source == "" so pre-L1 JSONL records continue to round-trip
// without surprise, matching the Phase L1.1 backward-compat contract.
func (m *Message) UnmarshalJSON(data []byte) error {
	type wire struct {
		Role    string            `json:"role"`
		Source  string            `json:"source,omitempty"`
		Content []json.RawMessage `json:"content"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	m.Role = w.Role
	m.Source = w.Source
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
	case "thinking":
		var b ThinkingBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return b, nil
	case "image":
		var b struct {
			Source struct {
				Type      string `json:"type"`
				MediaType string `json:"media_type"`
				Data      string `json:"data"`
			} `json:"source"`
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return ImageBlock{MediaType: b.Source.MediaType, Data: b.Source.Data}, nil
	default:
		return TextBlock{Text: string(raw)}, nil
	}
}
