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
// It is chat-only — tool_use is not forwarded (AD-3).
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
// Tool definitions are NOT included in the request (chat-only per AD-3).
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
	var inputTokens int

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
					InputTokens: inputTokens,
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
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				cb(StreamEvent{
					Type:    EventTextDelta,
					Content: choice.Delta.Content,
				})
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("openai: read stream: %w", err)
	}

	// Stream ended without [DONE] — emit done anyway.
	cb(StreamEvent{
		Type:  EventDone,
		Usage: &UsageStats{InputTokens: inputTokens},
	})
	return nil
}

// toOpenAIMessages converts []Message to OpenAI wire format.
// ToolUseBlock and ToolResultBlock are converted to text so the chat
// context is preserved without requiring tool_use support (AD-3).
func toOpenAIMessages(msgs []Message) ([]openAIMessage, error) {
	out := make([]openAIMessage, 0, len(msgs))
	for _, m := range msgs {
		role := string(m.Role)
		if role == "system" {
			// System messages handled separately via req.System.
			continue
		}

		var sb strings.Builder
		for _, b := range m.Content {
			switch blk := b.(type) {
			case TextBlock:
				sb.WriteString(blk.Text)
			case ToolUseBlock:
				input, _ := json.Marshal(blk.Input)
				fmt.Fprintf(&sb, "[tool_use: %s(%s)]", blk.Name, string(input))
			case ToolResultBlock:
				if blk.IsError {
					fmt.Fprintf(&sb, "[tool_error(%s): %s]", blk.ToolUseID, blk.Content)
				} else {
					fmt.Fprintf(&sb, "[tool_result(%s): %s]", blk.ToolUseID, blk.Content)
				}
			default:
				// Unknown block: skip.
			}
		}

		out = append(out, openAIMessage{
			Role:    role,
			Content: sb.String(),
		})
	}
	return out, nil
}

// Chat sends a non-streaming chat completion and returns the complete response.
func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	var textParts []string
	var usageStats UsageStats

	err := p.Stream(ctx, req, func(ev StreamEvent) {
		switch ev.Type {
		case EventTextDelta:
			if ev.Content != "" {
				textParts = append(textParts, ev.Content)
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

	combined := ""
	for _, part := range textParts {
		combined += part
	}
	return &ChatResponse{
		Content:    combined,
		StopReason: "end_turn",
		Usage: Usage{
			InputTokens:  usageStats.InputTokens,
			OutputTokens: usageStats.OutputTokens,
		},
	}, nil
}

var openAIModels = []ModelInfo{
	{
		ID:              "gpt-4o",
		Name:            "GPT-4o",
		MaxTokens:       128000,
		InputPricePerM:  2.5,
		OutputPricePerM: 10.0,
	},
	{
		ID:              "gpt-4o-mini",
		Name:            "GPT-4o mini",
		MaxTokens:       128000,
		InputPricePerM:  0.15,
		OutputPricePerM: 0.60,
	},
}

// Models returns the list of OpenAI models known to this provider.
func (p *OpenAIProvider) Models() []ModelInfo {
	return openAIModels
}

// ---- wire types ----

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatRequest struct {
	Model     string          `json:"model"`
	Messages  []openAIMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens"`
	Stream    bool            `json:"stream"`
}

type openAIStreamChunk struct {
	Choices []openAIStreamChoice `json:"choices"`
	Usage   *openAIUsage         `json:"usage,omitempty"`
}

type openAIStreamChoice struct {
	Delta openAIStreamDelta `json:"delta"`
}

type openAIStreamDelta struct {
	Content string `json:"content"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
