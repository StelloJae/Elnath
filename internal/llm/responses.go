package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ResponsesProvider uses OpenAI's Responses API (/v1/responses)
// which works with ChatGPT OAuth tokens (Codex CLI auth_mode: "chatgpt").
type ResponsesProvider struct {
	accessToken string
	accountID   string
	baseURL     string
	client      *http.Client
	model       string
}

func NewResponsesProvider(accessToken, model, accountID string, opts ...ResponsesOption) *ResponsesProvider {
	p := &ResponsesProvider{
		accessToken: accessToken,
		baseURL:     "https://chatgpt.com/backend-api/codex",
		client:      &http.Client{Timeout: 300 * time.Second},
		model:       model,
		accountID:   accountID,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

type ResponsesOption func(*ResponsesProvider)

func WithResponsesBaseURL(url string) ResponsesOption {
	return func(p *ResponsesProvider) { p.baseURL = strings.TrimRight(url, "/") }
}

func (p *ResponsesProvider) Name() string { return "openai-responses" }

func (p *ResponsesProvider) Models() []ModelInfo {
	return []ModelInfo{{ID: p.model, Name: p.model, MaxTokens: 32768}}
}

func (p *ResponsesProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Codex Responses API requires stream=true. Collect streamed chunks into a single response.
	var textParts []string
	var toolCalls []CompletedToolCall
	var usageStats UsageStats

	pending := map[string]*CompletedToolCall{}

	err := p.Stream(ctx, req, func(ev StreamEvent) {
		switch ev.Type {
		case EventTextDelta:
			if ev.Content != "" {
				textParts = append(textParts, ev.Content)
			}
		case EventToolUseStart:
			if ev.ToolCall != nil {
				pending[ev.ToolCall.ID] = &CompletedToolCall{
					ID:   ev.ToolCall.ID,
					Name: ev.ToolCall.Name,
				}
			}
		case EventToolUseDelta:
			if ev.ToolCall != nil {
				if tc, ok := pending[ev.ToolCall.ID]; ok {
					tc.Input += ev.ToolCall.Input
				}
			}
		case EventToolUseDone:
			if ev.ToolCall != nil {
				if tc, ok := pending[ev.ToolCall.ID]; ok {
					tc.Input = ev.ToolCall.Input
					toolCalls = append(toolCalls, *tc)
					delete(pending, ev.ToolCall.ID)
				}
			}
		case EventDone:
			if ev.Usage != nil {
				usageStats = UsageStats{
					InputTokens:  ev.Usage.InputTokens,
					OutputTokens: ev.Usage.OutputTokens,
				}
			}
		}
	})
	if err != nil {
		return nil, err
	}

	resp := &ChatResponse{
		Content:    strings.Join(textParts, ""),
		StopReason: "end_turn",
		Usage: Usage{
			InputTokens:  usageStats.InputTokens,
			OutputTokens: usageStats.OutputTokens,
		},
	}
	for _, tc := range toolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID:    tc.ID,
			Name:  tc.Name,
			Input: tc.Input,
		})
	}
	if len(resp.ToolCalls) > 0 {
		resp.StopReason = "tool_use"
	}
	return resp, nil
}

func (p *ResponsesProvider) Stream(ctx context.Context, req ChatRequest, cb func(StreamEvent)) error {
	body := p.buildRequest(req, true)
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("responses: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/responses", bytes.NewReader(data))
	if err != nil {
		return err
	}
	p.setHeaders(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("responses: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("responses: http %d: %s", resp.StatusCode, string(respBody))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)

	// Track item_id → call_id mapping for function calls.
	// The Responses API uses item_id in delta/done events but call_id is
	// what we need for tool result matching.
	itemToCallID := map[string]string{}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[6:]
		if payload == "[DONE]" {
			break
		}

		var event responsesStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}

		switch event.Type {
		case "response.output_text.delta":
			cb(StreamEvent{Type: EventTextDelta, Content: event.Delta})
		case "response.output_item.added":
			if event.Item != nil && event.Item.Type == "function_call" {
				itemToCallID[event.Item.ID] = event.Item.CallID
				cb(StreamEvent{
					Type: EventToolUseStart,
					ToolCall: &ToolUseEvent{
						ID:   event.Item.CallID,
						Name: event.Item.Name,
					},
				})
			}
		case "response.function_call_arguments.delta":
			callID := itemToCallID[event.ItemID]
			cb(StreamEvent{
				Type: EventToolUseDelta,
				ToolCall: &ToolUseEvent{
					ID:    callID,
					Input: event.Delta,
				},
			})
		case "response.function_call_arguments.done":
			callID := itemToCallID[event.ItemID]
			cb(StreamEvent{
				Type: EventToolUseDone,
				ToolCall: &ToolUseEvent{
					ID:    callID,
					Input: event.Arguments,
				},
			})
		case "response.completed":
			var usage *UsageStats
			if event.Response != nil && event.Response.Usage != nil {
				usage = &UsageStats{
					InputTokens:  event.Response.Usage.InputTokens,
					OutputTokens: event.Response.Usage.OutputTokens,
				}
			}
			cb(StreamEvent{Type: EventDone, Usage: usage})
			return nil
		}
	}

	return scanner.Err()
}

func (p *ResponsesProvider) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.accessToken)
	req.Header.Set("Content-Type", "application/json")
	if p.accountID != "" {
		req.Header.Set("chatgpt-account-id", p.accountID)
	}
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("Accept", "text/event-stream")
}

func (p *ResponsesProvider) buildRequest(req ChatRequest, stream bool) map[string]interface{} {
	input := make([]interface{}, 0, len(req.Messages)*2)

	// System prompt handled via "instructions" field, not in input

	for _, msg := range req.Messages {
		var textParts []map[string]interface{}
		for _, block := range msg.Content {
			switch b := block.(type) {
			case TextBlock:
				textParts = append(textParts, map[string]interface{}{
					"type": "input_text",
					"text": b.Text,
				})
			case ToolUseBlock:
				// Flush accumulated text parts as a role message first.
				if len(textParts) > 0 {
					input = append(input, buildRoleMessage(msg.Role, textParts))
					textParts = nil
				}
				input = append(input, map[string]interface{}{
					"type":      "function_call",
					"name":      b.Name,
					"call_id":   b.ID,
					"arguments": string(b.Input),
				})
			case ToolResultBlock:
				// Flush accumulated text parts as a role message first.
				if len(textParts) > 0 {
					input = append(input, buildRoleMessage(msg.Role, textParts))
					textParts = nil
				}
				input = append(input, map[string]interface{}{
					"type":    "function_call_output",
					"call_id": b.ToolUseID,
					"output":  b.Content,
				})
			}
		}
		if len(textParts) > 0 {
			input = append(input, buildRoleMessage(msg.Role, textParts))
		}
	}

	instructions := req.System
	if instructions == "" {
		instructions = "You are a helpful assistant."
	}
	model := req.Model
	if model == "" {
		model = p.model
	}
	body := map[string]interface{}{
		"model":        model,
		"input":        input,
		"instructions": instructions,
		"store":        false,
	}
	if stream {
		body["stream"] = true
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]interface{}, 0, len(req.Tools))
		for _, t := range req.Tools {
			tool := map[string]interface{}{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
			}
			if len(t.InputSchema) > 0 {
				var schema interface{}
				if err := json.Unmarshal(t.InputSchema, &schema); err == nil {
					tool["parameters"] = schema
				}
			}
			tools = append(tools, tool)
		}
		body["tools"] = tools
	}
	return body
}

func buildRoleMessage(role string, parts []map[string]interface{}) map[string]interface{} {
	if len(parts) == 1 {
		return map[string]interface{}{
			"role":    role,
			"content": parts[0]["text"],
		}
	}
	return map[string]interface{}{
		"role":    role,
		"content": parts,
	}
}

func (p *ResponsesProvider) parseResult(r *responsesResult) *ChatResponse {
	var text strings.Builder
	var toolCalls []ToolCall
	for _, item := range r.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, ToolCall{
				ID:    item.CallID,
				Name:  item.Name,
				Input: item.Arguments,
			})
		}
	}

	stopReason := "end_turn"
	if len(toolCalls) > 0 {
		stopReason = "tool_use"
	}
	resp := &ChatResponse{
		Content:    text.String(),
		ToolCalls:  toolCalls,
		StopReason: stopReason,
	}
	if r.Usage != nil {
		resp.Usage = Usage{
			InputTokens:  r.Usage.InputTokens,
			OutputTokens: r.Usage.OutputTokens,
		}
	}
	return resp
}

// --- wire types ---

type responsesResult struct {
	ID     string            `json:"id"`
	Output []responsesOutput `json:"output"`
	Usage  *responsesUsage   `json:"usage"`
}

type responsesOutput struct {
	Type      string             `json:"type"`
	Content   []responsesContent `json:"content"`
	Name      string             `json:"name,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
}

type responsesContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type responsesStreamEvent struct {
	Type      string           `json:"type"`
	Delta     string           `json:"delta"`
	Response  *responsesResult `json:"response"`
	Item      *responsesItem   `json:"item,omitempty"`
	ItemID    string           `json:"item_id,omitempty"`
	Arguments string           `json:"arguments,omitempty"`
}

type responsesItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	CallID    string `json:"call_id"`
	Arguments string `json:"arguments"`
}
