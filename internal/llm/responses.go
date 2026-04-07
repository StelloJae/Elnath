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
	var usage *UsageStats

	err := p.Stream(ctx, req, func(ev StreamEvent) {
		switch ev.Type {
		case EventTextDelta:
			textParts = append(textParts, ev.Content)
		case EventDone:
			usage = ev.Usage
		}
	})
	if err != nil {
		return nil, err
	}

	resp := &ChatResponse{
		Content:    strings.Join(textParts, ""),
		StopReason: "end_turn",
	}
	if usage != nil {
		resp.Usage = Usage{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
		}
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
	input := make([]map[string]interface{}, 0, len(req.Messages))

	// System prompt handled via "instructions" field, not in input

	for _, msg := range req.Messages {
		m := map[string]interface{}{"role": msg.Role}
		parts := make([]map[string]interface{}, 0)
		for _, block := range msg.Content {
			switch b := block.(type) {
			case TextBlock:
				parts = append(parts, map[string]interface{}{
					"type": "input_text",
					"text": b.Text,
				})
			case ToolUseBlock:
				// skip tool_use in input (handled separately)
			case ToolResultBlock:
				// skip tool results for now (chat-only mode)
			}
		}
		if len(parts) == 1 {
			m["content"] = parts[0]["text"]
		} else if len(parts) > 1 {
			m["content"] = parts
		} else {
			m["content"] = msg.Text()
		}
		input = append(input, m)
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
	return body
}

func (p *ResponsesProvider) parseResult(r *responsesResult) *ChatResponse {
	var text strings.Builder
	for _, item := range r.Output {
		if item.Type == "message" {
			for _, c := range item.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				}
			}
		}
	}

	resp := &ChatResponse{
		Content:    text.String(),
		StopReason: "end_turn",
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
	Type    string             `json:"type"`
	Content []responsesContent `json:"content"`
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
	Type     string           `json:"type"`
	Delta    string           `json:"delta"`
	Response *responsesResult `json:"response"`
}
