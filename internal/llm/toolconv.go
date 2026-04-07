package llm

import "encoding/json"

// AppendToolResult appends a user-role message carrying tool_result blocks
// for each ToolUseBlock in the previous assistant message.
//
// Anthropic requires tool results to be sent as a user turn containing
// content blocks of type "tool_result", not as a top-level role.
func AppendToolResult(messages []Message, toolUseID, content string, isError bool) []Message {
	block := ToolResultBlock{
		ToolUseID: toolUseID,
		Content:   content,
		IsError:   isError,
	}

	// If the last message is already a user message with tool_result blocks,
	// append to it so multiple results stay in the same turn.
	if len(messages) > 0 {
		last := &messages[len(messages)-1]
		if last.Role == "user" && hasToolResults(last) {
			last.Content = append(last.Content, block)
			return messages
		}
	}

	return append(messages, Message{
		Role:    "user",
		Content: []ContentBlock{block},
	})
}

func hasToolResults(m *Message) bool {
	for _, b := range m.Content {
		if b.BlockType() == "tool_result" {
			return true
		}
	}
	return false
}

// ExtractToolUseBlocks returns all ToolUseBlocks from a message.
func ExtractToolUseBlocks(m Message) []ToolUseBlock {
	var out []ToolUseBlock
	for _, b := range m.Content {
		if tb, ok := b.(ToolUseBlock); ok {
			out = append(out, tb)
		}
	}
	return out
}

// BuildAssistantMessage constructs an assistant Message from accumulated
// stream events (text and tool use).
func BuildAssistantMessage(textParts []string, toolCalls []CompletedToolCall) Message {
	var content []ContentBlock

	// Combine all text parts into a single TextBlock if non-empty.
	combined := ""
	for _, p := range textParts {
		combined += p
	}
	if combined != "" {
		content = append(content, TextBlock{Text: combined})
	}

	for _, tc := range toolCalls {
		var inputRaw json.RawMessage
		if tc.Input == "" {
			inputRaw = json.RawMessage("{}")
		} else {
			inputRaw = json.RawMessage(tc.Input)
		}
		content = append(content, ToolUseBlock{
			ID:    tc.ID,
			Name:  tc.Name,
			Input: inputRaw,
		})
	}

	return Message{Role: "assistant", Content: content}
}

// CompletedToolCall holds the final state of a streamed tool use.
type CompletedToolCall struct {
	ID    string
	Name  string
	Input string // complete JSON string
}

// ToAnthropicTools converts internal ToolDef slice to Anthropic API tool format.
func ToAnthropicTools(tools []ToolDef) []map[string]interface{} {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		schema := interface{}(t.InputSchema)
		if len(t.InputSchema) == 0 {
			schema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		out = append(out, map[string]interface{}{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": schema,
		})
	}
	return out
}

// ToAnthropicMessages converts internal Message slice to Anthropic API message format.
// TextBlock → {type:"text", text:...}
// ToolUseBlock → {type:"tool_use", id:..., name:..., input:...}
// ToolResultBlock → user-role message with tool_result content block
func ToAnthropicMessages(messages []Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		var blocks []map[string]interface{}
		for _, b := range m.Content {
			switch blk := b.(type) {
			case TextBlock:
				blocks = append(blocks, map[string]interface{}{
					"type": "text",
					"text": blk.Text,
				})
			case ToolUseBlock:
				var input interface{}
				if len(blk.Input) > 0 {
					var v interface{}
					if err := json.Unmarshal(blk.Input, &v); err == nil {
						input = v
					} else {
						input = map[string]interface{}{}
					}
				} else {
					input = map[string]interface{}{}
				}
				blocks = append(blocks, map[string]interface{}{
					"type":  "tool_use",
					"id":    blk.ID,
					"name":  blk.Name,
					"input": input,
				})
			case ToolResultBlock:
				tr := map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": blk.ToolUseID,
					"content":     blk.Content,
				}
				if blk.IsError {
					tr["is_error"] = true
				}
				blocks = append(blocks, tr)
			}
		}
		if len(blocks) > 0 {
			out = append(out, map[string]interface{}{
				"role":    m.Role,
				"content": blocks,
			})
		}
	}
	return out
}

// ToOpenAITools converts internal ToolDef to OpenAI function calling format (v0.2).
func ToOpenAITools(tools []ToolDef) []map[string]interface{} {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		params := interface{}(t.InputSchema)
		if len(t.InputSchema) == 0 {
			params = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		out = append(out, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  params,
			},
		})
	}
	return out
}

// ToOpenAIMessages converts internal Messages to OpenAI chat format.
// ToolUseBlock is serialised as a function_call in the assistant message.
// ToolResultBlock becomes a message with role "tool".
func ToOpenAIMessages(messages []Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case "user", "system":
			text := m.Text()
			out = append(out, map[string]interface{}{
				"role":    m.Role,
				"content": text,
			})
		case "assistant":
			text := m.Text()
			var toolCalls []map[string]interface{}
			for _, b := range m.Content {
				if blk, ok := b.(ToolUseBlock); ok {
					var args interface{}
					if len(blk.Input) > 0 {
						var v interface{}
						if err := json.Unmarshal(blk.Input, &v); err == nil {
							args = v
						}
					}
					if args == nil {
						args = map[string]interface{}{}
					}
					argBytes, _ := json.Marshal(args)
					toolCalls = append(toolCalls, map[string]interface{}{
						"id":   blk.ID,
						"type": "function",
						"function": map[string]interface{}{
							"name":      blk.Name,
							"arguments": string(argBytes),
						},
					})
				}
			}
			msg := map[string]interface{}{
				"role":    "assistant",
				"content": text,
			}
			if len(toolCalls) > 0 {
				msg["tool_calls"] = toolCalls
			}
			out = append(out, msg)
		}

		// ToolResultBlock → role "tool" message
		for _, b := range m.Content {
			if blk, ok := b.(ToolResultBlock); ok {
				out = append(out, map[string]interface{}{
					"role":         "tool",
					"tool_call_id": blk.ToolUseID,
					"content":      blk.Content,
				})
			}
		}
	}
	return out
}
