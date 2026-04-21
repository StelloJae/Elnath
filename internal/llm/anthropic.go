package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/stello/elnath/internal/userfacingerr"
)

const (
	anthropicDefaultBaseURL = "https://api.anthropic.com"
	anthropicAPIVersion     = "2023-06-01"
	anthropicDefaultTimeout = 120 * time.Second

	// anthropicOAuthTokenPrefix identifies a Claude Code OAuth access token.
	// Such tokens authenticate via Authorization: Bearer + the claude-code
	// anthropic-beta feature flag, not the standard x-api-key header.
	anthropicOAuthTokenPrefix = "sk-ant-oat01-"
	anthropicOAuthBeta        = "claude-code-20250219,oauth-2025-04-20"
	anthropicOAuthUserAgent   = "claude-cli/2.1.2 (external, cli)"
)

// isAnthropicOAuthToken reports whether key is a Claude Code OAuth access token.
func isAnthropicOAuthToken(key string) bool {
	return strings.HasPrefix(key, anthropicOAuthTokenPrefix)
}

// setAnthropicAuthHeaders applies the correct auth headers based on token type.
// OAuth tokens use Authorization: Bearer plus the claude-code beta feature flag;
// standard API keys use x-api-key.
func setAnthropicAuthHeaders(h http.Header, apiKey string) {
	if isAnthropicOAuthToken(apiKey) {
		h.Set("Authorization", "Bearer "+apiKey)
		h.Set("anthropic-beta", anthropicOAuthBeta)
		h.Set("user-agent", anthropicOAuthUserAgent)
		h.Set("x-app", "cli")
		return
	}
	h.Set("x-api-key", apiKey)
}

// AnthropicProvider implements Provider for Anthropic's Messages API.
type AnthropicProvider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// AnthropicOption configures an AnthropicProvider.
type AnthropicOption func(*AnthropicProvider)

// WithAnthropicBaseURL overrides the default API base URL.
func WithAnthropicBaseURL(u string) AnthropicOption {
	return func(p *AnthropicProvider) { p.baseURL = u }
}

// WithAnthropicHTTPClient replaces the default HTTP client.
func WithAnthropicHTTPClient(c *http.Client) AnthropicOption {
	return func(p *AnthropicProvider) { p.client = c }
}

// WithAnthropicTimeout sets the HTTP client timeout.
func WithAnthropicTimeout(d time.Duration) AnthropicOption {
	return func(p *AnthropicProvider) { p.client = &http.Client{Timeout: d} }
}

// NewAnthropicProvider constructs an Anthropic provider.
func NewAnthropicProvider(apiKey, model string, opts ...AnthropicOption) *AnthropicProvider {
	p := &AnthropicProvider{
		apiKey:  apiKey,
		baseURL: anthropicDefaultBaseURL,
		model:   model,
		client:  &http.Client{Timeout: anthropicDefaultTimeout},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

// Stream sends a streaming request to Anthropic and calls cb for each event.
func (p *AnthropicProvider) Stream(ctx context.Context, req Request, cb func(StreamEvent)) error {
	body, err := buildAnthropicRequest(req, p.model)
	if err != nil {
		return fmt.Errorf("anthropic: build request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("anthropic: new http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	setAnthropicAuthHeaders(httpReq.Header, p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			inner := fmt.Errorf("anthropic: http: %w", err)
			return userfacingerr.Wrap(userfacingerr.ELN040, inner, "anthropic http")
		}
		return fmt.Errorf("anthropic: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if dumpPath := os.Getenv("ELNATH_ANTHROPIC_DUMP"); dumpPath != "" {
			_ = os.WriteFile(dumpPath, append([]byte("REQUEST BODY:\n"), append(body, append([]byte("\n\nRESPONSE:\n"), errBody...)...)...), 0o600)
		}
		switch resp.StatusCode {
		case 429:
			inner := fmt.Errorf("anthropic: rate limit (429): %s", errBody)
			return userfacingerr.Wrap(userfacingerr.ELN080, inner, "anthropic 429")
		case 529:
			return fmt.Errorf("anthropic: overloaded (529): %s", errBody)
		default:
			return fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, errBody)
		}
	}

	return parseAnthropicSSE(resp.Body, cb)
}

// --- request building ---

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    json.RawMessage    `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream"`
	Thinking  *anthropicThinking `json:"thinking,omitempty"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anthropicMessage struct {
	Role    string            `json:"role"`
	Content []json.RawMessage `json:"content"`
}

type anthropicTool struct {
	// Type labels server-side native tools (e.g. "web_search_20250305"); left
	// empty for the default function-style tool path, in which case the
	// Messages API infers a custom tool from {name, description, input_schema}.
	Type         string          `json:"type,omitempty"`
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	// MaxUses caps server-side tool invocations (currently only native
	// web_search, which Claude Code hardcodes to 8).
	MaxUses      *int          `json:"max_uses,omitempty"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

var defaultInputSchema = json.RawMessage(`{"type":"object","properties":{}}`)

func buildAnthropicRequest(req Request, defaultModel string) ([]byte, error) {
	model := req.Model
	if model == "" {
		model = defaultModel
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}

	ar := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Stream:    true,
	}

	// Build system prompt as structured block(s) with optional cache_control.
	if req.System != "" {
		sysBlock := map[string]interface{}{
			"type": "text",
			"text": req.System,
		}
		if req.EnableCache {
			sysBlock["cache_control"] = map[string]string{"type": "ephemeral"}
		}
		sysBlocks := []interface{}{sysBlock}
		sysJSON, err := json.Marshal(sysBlocks)
		if err != nil {
			return nil, fmt.Errorf("marshal system: %w", err)
		}
		ar.System = sysJSON
	}

	// Extended thinking support.
	if req.ThinkingBudget > 0 {
		ar.Thinking = &anthropicThinking{
			Type:         "enabled",
			BudgetTokens: req.ThinkingBudget,
		}
		// Thinking requires max_tokens to be at least budget + 1.
		if ar.MaxTokens <= req.ThinkingBudget {
			ar.MaxTokens = req.ThinkingBudget + 4096
		}
	}

	for i, t := range req.Tools {
		// Native server-side tools: the Messages API executes these itself
		// and injects results into the model's context, so the tool carries
		// only type + server-specific config — no description/input_schema.
		// Reference: /Users/stello/claude-code-src/src/tools/WebSearchTool/
		// WebSearchTool.ts:76-84 (makeToolSchema, max_uses=8 hardcoded).
		// Without this branch Claude routes queries through Elnath's own
		// DDG-scrape fallback instead of the hosted search primitive.
		if t.Name == "web_search" {
			maxUses := 8
			tool := anthropicTool{
				Type:    "web_search_20250305",
				Name:    "web_search",
				MaxUses: &maxUses,
			}
			if req.EnableCache && i == len(req.Tools)-1 {
				tool.CacheControl = &cacheControl{Type: "ephemeral"}
			}
			ar.Tools = append(ar.Tools, tool)
			continue
		}

		schema := t.InputSchema
		if len(schema) == 0 {
			schema = defaultInputSchema
		}
		tool := anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		}
		// Add cache_control to the last tool for prompt caching.
		if req.EnableCache && i == len(req.Tools)-1 {
			tool.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		ar.Tools = append(ar.Tools, tool)
	}

	for _, msg := range req.Messages {
		am, err := toAnthropicMessage(msg)
		if err != nil {
			return nil, err
		}
		ar.Messages = append(ar.Messages, am)
	}

	return json.Marshal(ar)
}

func toAnthropicMessage(msg Message) (anthropicMessage, error) {
	am := anthropicMessage{Role: string(msg.Role)}
	for _, block := range msg.Content {
		raw, err := marshalBlock(block)
		if err != nil {
			return anthropicMessage{}, err
		}
		am.Content = append(am.Content, raw)
	}
	return am, nil
}

// --- SSE parsing ---

func parseAnthropicSSE(r io.Reader, cb func(StreamEvent)) error {
	scanner := bufio.NewScanner(r)

	// Track in-progress tool use blocks by index.
	type toolState struct {
		id    string
		name  string
		input strings.Builder
	}
	tools := map[int]*toolState{}
	thinkingIndices := map[int]bool{}
	var currentToolIndex int

	var usage UsageStats

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event:") {
			// event type line — we handle via data parsing
			continue
		}

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}

		var ev map[string]json.RawMessage
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}

		evType := jsonString(ev["type"])

		switch evType {
		case "message_start":
			var ms struct {
				Message struct {
					Usage struct {
						InputTokens         int `json:"input_tokens"`
						CacheReadTokens     int `json:"cache_read_input_tokens"`
						CacheCreationTokens int `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(data), &ms); err == nil {
				usage.InputTokens = ms.Message.Usage.InputTokens
				usage.CacheRead = ms.Message.Usage.CacheReadTokens
				usage.CacheWrite = ms.Message.Usage.CacheCreationTokens
				cb(StreamEvent{InputTokens: usage.InputTokens})
			}

		case "content_block_start":
			var cbs struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(data), &cbs); err != nil {
				continue
			}
			switch cbs.ContentBlock.Type {
			case "tool_use":
				currentToolIndex = cbs.Index
				tools[cbs.Index] = &toolState{
					id:   cbs.ContentBlock.ID,
					name: cbs.ContentBlock.Name,
				}
				cb(StreamEvent{
					Type: EventToolUseStart,
					ToolCall: &ToolUseEvent{
						ID:   cbs.ContentBlock.ID,
						Name: cbs.ContentBlock.Name,
					},
				})
			case "thinking":
				// Extended thinking block started — tracked by index.
				thinkingIndices[cbs.Index] = true
			}

		case "content_block_delta":
			var cbd struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &cbd); err != nil {
				continue
			}
			switch cbd.Delta.Type {
			case "text_delta":
				if thinkingIndices[cbd.Index] {
					// Thinking delta — skip streaming to user (internal reasoning).
				} else {
					cb(StreamEvent{Type: EventTextDelta, Content: cbd.Delta.Text})
				}
			case "thinking_delta":
				// Extended thinking content — silently consumed.
			case "input_json_delta":
				if ts, ok := tools[cbd.Index]; ok {
					ts.input.WriteString(cbd.Delta.PartialJSON)
					cb(StreamEvent{
						Type: EventToolUseDelta,
						ToolCall: &ToolUseEvent{
							ID:    ts.id,
							Name:  ts.name,
							Input: cbd.Delta.PartialJSON,
						},
					})
				}
			}

		case "content_block_stop":
			var cbs struct {
				Index int `json:"index"`
			}
			if err := json.Unmarshal([]byte(data), &cbs); err != nil {
				continue
			}
			if ts, ok := tools[cbs.Index]; ok {
				cb(StreamEvent{
					Type: EventToolUseDone,
					ToolCall: &ToolUseEvent{
						ID:    ts.id,
						Name:  ts.name,
						Input: ts.input.String(),
					},
				})
				_ = currentToolIndex
			}

		case "message_delta":
			var md struct {
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &md); err == nil {
				usage.OutputTokens = md.Usage.OutputTokens
			}

		case "message_stop":
			cb(StreamEvent{Type: EventDone, Usage: &usage})

		case "error":
			var errEv struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(data), &errEv); err == nil {
				return fmt.Errorf("anthropic: stream error: %s", errEv.Error.Message)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("anthropic: scan: %w", err)
	}
	return nil
}

// Chat sends a non-streaming request and returns the complete response by
// accumulating the stream internally.
func (p *AnthropicProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	var textParts []string
	var toolCalls []CompletedToolCall
	var usageStats UsageStats

	// Track in-progress tool calls keyed by ID.
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
					toolCalls = append(toolCalls, *tc)
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

	combined := ""
	for _, p := range textParts {
		combined += p
	}

	resp := &ChatResponse{
		Content: combined,
		Usage: Usage{
			InputTokens:  usageStats.InputTokens,
			OutputTokens: usageStats.OutputTokens,
			CacheRead:    usageStats.CacheRead,
			CacheWrite:   usageStats.CacheWrite,
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
	} else {
		resp.StopReason = "end_turn"
	}
	return resp, nil
}

// anthropicModels lists the supported Anthropic models with approximate pricing.
var anthropicModels = []ModelInfo{
	{
		ID:              "claude-opus-4-6",
		Name:            "Claude Opus 4",
		MaxTokens:       32000,
		ContextWindow:   200_000,
		InputPricePerM:  15.0,
		OutputPricePerM: 75.0,
	},
	{
		ID:              "claude-sonnet-4-6",
		Name:            "Claude Sonnet 4",
		MaxTokens:       16000,
		ContextWindow:   200_000,
		InputPricePerM:  3.0,
		OutputPricePerM: 15.0,
	},
	{
		ID:              "claude-haiku-4-5",
		Name:            "Claude Haiku 4",
		MaxTokens:       8192,
		ContextWindow:   200_000,
		InputPricePerM:  0.8,
		OutputPricePerM: 4.0,
	},
}

// Models returns the list of Anthropic models known to this provider.
func (p *AnthropicProvider) Models() []ModelInfo {
	return anthropicModels
}

func jsonString(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}
