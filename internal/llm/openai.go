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

const defaultOpenAIBaseURL = "https://api.openai.com/v1"

// OpenAIProvider implements Provider for the OpenAI chat completions API.
type OpenAIProvider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// OpenAIOption configures an OpenAIProvider.
type OpenAIOption func(*OpenAIProvider)

// WithOpenAIBaseURL overrides the default API base URL (useful for proxies / Azure).
func WithOpenAIBaseURL(u string) OpenAIOption {
	return func(p *OpenAIProvider) { p.baseURL = u }
}

// WithOpenAIHTTPClient replaces the default HTTP client.
func WithOpenAIHTTPClient(c *http.Client) OpenAIOption {
	return func(p *OpenAIProvider) { p.client = c }
}

// NewOpenAIProvider creates an OpenAIProvider.
func NewOpenAIProvider(apiKey, model string, opts ...OpenAIOption) *OpenAIProvider {
	p := &OpenAIProvider{
		apiKey:  apiKey,
		baseURL: defaultOpenAIBaseURL,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *OpenAIProvider) Name() string { return "openai" }

// Stream sends a chat completion request with streaming and calls cb for each event.
func (p *OpenAIProvider) Stream(ctx context.Context, req Request, cb func(StreamEvent)) error {
	msgs, err := toOpenAIMessages(req.Messages)
	if err != nil {
		return fmt.Errorf("openai: convert messages: %w", err)
	}

	// Prepend system message if provided.
	if req.System != "" {
		sys := openAIMessage{Role: "system", Content: req.System}
		msgs = append([]openAIMessage{sys}, msgs...)
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	body := openAIChatRequest{
		Model:     p.model,
		Messages:  msgs,
		MaxTokens: maxTokens,
		Stream:    true,
	}

	if len(req.Tools) > 0 {
		for _, t := range req.Tools {
			body.Tools = append(body.Tools, openAITool{
				Type: "function",
				Function: openAIFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			})
		}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("openai: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("openai: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("openai: http %d: %s", resp.StatusCode, string(body))
	}

	return p.parseSSE(resp.Body, cb)
}

// parseSSE reads the SSE stream and emits StreamEvents via cb.
func (p *OpenAIProvider) parseSSE(r io.Reader, cb func(StreamEvent)) error {
	scanner := bufio.NewScanner(r)
	var inputTokens, outputTokens int
	// index → tool call ID, used to correlate argument delta chunks.
	pendingToolIDs := map[int]string{}

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			cb(StreamEvent{
				Type: EventDone,
				Usage: &UsageStats{
					InputTokens:  inputTokens,
					OutputTokens: outputTokens,
				},
			})
			return nil
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Malformed chunk — skip silently to stay resilient.
			continue
		}

		// Capture usage if present (some endpoints send it in the final chunk).
		if chunk.Usage != nil {
			inputTokens = chunk.Usage.PromptTokens
			outputTokens = chunk.Usage.CompletionTokens
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				cb(StreamEvent{
					Type:    EventTextDelta,
					Content: choice.Delta.Content,
				})
			}

			for _, tc := range choice.Delta.ToolCalls {
				if tc.ID != "" {
					// First chunk for this tool call: has ID and function name.
					pendingToolIDs[tc.Index] = tc.ID
					cb(StreamEvent{
						Type: EventToolUseStart,
						ToolCall: &ToolUseEvent{
							ID:   tc.ID,
							Name: tc.Function.Name,
						},
					})
				}
				if tc.Function.Arguments != "" {
					id := pendingToolIDs[tc.Index]
					cb(StreamEvent{
						Type: EventToolUseDelta,
						ToolCall: &ToolUseEvent{
							ID:    id,
							Input: tc.Function.Arguments,
						},
					})
				}
			}

			if choice.FinishReason == "tool_calls" {
				for _, id := range pendingToolIDs {
					cb(StreamEvent{
						Type:     EventToolUseDone,
						ToolCall: &ToolUseEvent{ID: id},
					})
				}
				pendingToolIDs = map[int]string{}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("openai: read stream: %w", err)
	}

	// Stream ended without [DONE] — emit done anyway.
	cb(StreamEvent{
		Type:  EventDone,
		Usage: &UsageStats{InputTokens: inputTokens, OutputTokens: outputTokens},
	})
	return nil
}

// toOpenAIMessages converts []Message to OpenAI wire format.
// ToolUseBlock in assistant messages → tool_calls field.
// ToolResultBlock in user messages → separate "tool" role messages.
func toOpenAIMessages(msgs []Message) ([]openAIMessage, error) {
	out := make([]openAIMessage, 0, len(msgs))
	for _, m := range msgs {
		role := string(m.Role)
		if role == "system" {
			// System messages handled separately via req.System.
			continue
		}

		// Collect text blocks and tool use blocks separately for assistant messages.
		var textParts []string
		var toolCalls []openAIToolCall
		var toolResults []openAIMessage

		for _, b := range m.Content {
			switch blk := b.(type) {
			case TextBlock:
				textParts = append(textParts, blk.Text)
			case ToolUseBlock:
				args := string(blk.Input)
				if args == "" {
					args = "{}"
				}
				toolCalls = append(toolCalls, openAIToolCall{
					ID:   blk.ID,
					Type: "function",
					Function: openAIFunctionCall{
						Name:      blk.Name,
						Arguments: args,
					},
				})
			case ToolResultBlock:
				// Tool results become standalone "tool" role messages.
				toolResults = append(toolResults, openAIMessage{
					Role:       "tool",
					Content:    blk.Content,
					ToolCallID: blk.ToolUseID,
				})
			}
		}

		if len(toolResults) > 0 {
			// User messages containing tool results are emitted as individual tool messages.
			out = append(out, toolResults...)
			continue
		}

		msg := openAIMessage{Role: role}
		if len(textParts) > 0 {
			msg.Content = strings.Join(textParts, "")
		}
		if len(toolCalls) > 0 {
			msg.ToolCalls = toolCalls
		}
		out = append(out, msg)
	}
	return out, nil
}

// Chat sends a non-streaming chat completion and returns the complete response.
func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	var textParts []string
	var usageStats UsageStats
	pending := map[string]*CompletedToolCall{}
	var toolCalls []ToolCall

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
					toolCalls = append(toolCalls, ToolCall{
						ID:    tc.ID,
						Name:  tc.Name,
						Input: tc.Input,
					})
					delete(pending, ev.ToolCall.ID)
				}
			}
		case EventDone:
			if ev.Usage != nil {
				usageStats = *ev.Usage
			}
		}
	})
	if err != nil {
		return nil, err
	}

	stopReason := "end_turn"
	if len(toolCalls) > 0 {
		stopReason = "tool_use"
	}
	resp := &ChatResponse{
		Content:    strings.Join(textParts, ""),
		StopReason: stopReason,
		Usage: Usage{
			InputTokens:  usageStats.InputTokens,
			OutputTokens: usageStats.OutputTokens,
		},
	}
	resp.ToolCalls = toolCalls
	return resp, nil
}

var openAIModels = []ModelInfo{
	{
		ID:              "gpt-5.4",
		Name:            "GPT-5.4",
		MaxTokens:       128_000,
		ContextWindow:   1_050_000,
		InputPricePerM:  2.5,
		OutputPricePerM: 15.0,
	},
	{
		ID:              "gpt-5.4-mini",
		Name:            "GPT-5.4 Mini",
		MaxTokens:       128_000,
		ContextWindow:   400_000,
		InputPricePerM:  0.75,
		OutputPricePerM: 4.5,
	},
	{
		ID:              "gpt-5.4-nano",
		Name:            "GPT-5.4 Nano",
		MaxTokens:       128_000,
		ContextWindow:   400_000,
		InputPricePerM:  0.20,
		OutputPricePerM: 1.25,
	},
	{
		ID:              "gpt-5.2",
		Name:            "GPT-5.2",
		MaxTokens:       100_000,
		ContextWindow:   1_050_000,
		InputPricePerM:  1.75,
		OutputPricePerM: 14.0,
	},
}

// Models returns the list of OpenAI models known to this provider.
func (p *OpenAIProvider) Models() []ModelInfo {
	return openAIModels
}

// ---- wire types ----

type openAIMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"` // "function"
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string         `json:"type"` // always "function"
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type openAIChatRequest struct {
	Model     string          `json:"model"`
	Messages  []openAIMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens"`
	Stream    bool            `json:"stream"`
	Tools     []openAITool    `json:"tools,omitempty"`
}

type openAIStreamChunk struct {
	Choices []openAIStreamChoice `json:"choices"`
	Usage   *openAIUsage         `json:"usage,omitempty"`
}

type openAIStreamChoice struct {
	Delta        openAIStreamDelta `json:"delta"`
	FinishReason string            `json:"finish_reason,omitempty"`
}

type openAIStreamDelta struct {
	Content   string                 `json:"content"`
	ToolCalls []openAIStreamToolCall `json:"tool_calls,omitempty"`
}

type openAIStreamToolCall struct {
	Index    int                `json:"index"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function openAIFunctionCall `json:"function"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
