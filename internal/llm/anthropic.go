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
	"sync"
	"time"

	"github.com/stello/elnath/internal/llm/promptcache"
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

	// anthropicContext1MBeta unlocks the 1M-token context window on Anthropic
	// Messages API. audit.txt §01: "1M enabled by beta header context-1m-2025-08-07".
	anthropicContext1MBeta = "context-1m-2025-08-07"
	// anthropicModel1MSuffix marks a model ID that opts into the 1M context
	// beta. The suffix is a Claude Code convention (see migrateOpusToOpus1m);
	// the API itself receives the base model ID plus the beta header.
	anthropicModel1MSuffix = "[1m]"
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

// opts1MContext reports whether the caller's model ID opts into Anthropic's
// 1M-context beta, per the "[1m]" suffix convention (audit.txt §01).
// Suppressed when CLAUDE_CODE_DISABLE_1M_CONTEXT=1 (HIPAA parity mirror).
func opts1MContext(model string) bool {
	if os.Getenv("CLAUDE_CODE_DISABLE_1M_CONTEXT") == "1" {
		return false
	}
	return strings.HasSuffix(model, anthropicModel1MSuffix)
}

// apiModelID strips Elnath-internal beta suffixes (e.g. "[1m]") so the
// Messages API sees only the base model string. The 1M window is enabled
// via the anthropic-beta header, not the model ID itself.
func apiModelID(model string) string {
	return strings.TrimSuffix(model, anthropicModel1MSuffix)
}

// appendAnthropicBeta merges a beta flag into the anthropic-beta header
// without clobbering any flags previously set (e.g. OAuth betas).
func appendAnthropicBeta(h http.Header, beta string) {
	if beta == "" {
		return
	}
	existing := h.Get("anthropic-beta")
	switch {
	case existing == "":
		h.Set("anthropic-beta", beta)
	case strings.Contains(existing, beta):
		return
	default:
		h.Set("anthropic-beta", existing+","+beta)
	}
}

// AnthropicProvider implements Provider for Anthropic's Messages API.
type AnthropicProvider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client

	// Prompt-cache telemetry: the sink is optional. When set alongside a
	// non-empty ChatRequest.SessionID, Stream captures a pre-call
	// PromptState and, after a successful call, records a break report
	// event via the sink. lastStates remembers the previous turn's
	// pre-state per session so GapSince attribution measures
	// turn-to-turn wall-clock (audit.txt §03).
	promptCacheSink promptcache.EventSink
	promptCacheMu   sync.Mutex
	lastStates      map[string]*promptcache.PromptState
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

// WithAnthropicPromptCacheSink installs a prompt-cache event sink. When
// set, every successful Stream call that carries a non-empty
// ChatRequest.SessionID records a BreakReport to the sink. The zero
// value (no option) leaves telemetry disabled so callers opt in.
func WithAnthropicPromptCacheSink(sink promptcache.EventSink) AnthropicOption {
	return func(p *AnthropicProvider) { p.promptCacheSink = sink }
}

// NewAnthropicProvider constructs an Anthropic provider.
func NewAnthropicProvider(apiKey, model string, opts ...AnthropicOption) *AnthropicProvider {
	p := &AnthropicProvider{
		apiKey:     apiKey,
		baseURL:    anthropicDefaultBaseURL,
		model:      model,
		client:     &http.Client{Timeout: anthropicDefaultTimeout},
		lastStates: make(map[string]*promptcache.PromptState),
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// effectiveBetas lists the anthropic-beta flags that actually go on the
// wire for this (apiKey, model) pair. Order matters for sorted-diff
// attribution; callers feed this into promptcache.RecordPromptState
// which re-sorts + dedupes.
func effectiveBetas(apiKey, model string) []string {
	var betas []string
	if isAnthropicOAuthToken(apiKey) {
		betas = append(betas, "claude-code-20250219", "oauth-2025-04-20")
	}
	if opts1MContext(model) {
		betas = append(betas, anthropicContext1MBeta)
	}
	return betas
}

// effortFromThinking projects the ChatRequest.ThinkingBudget integer
// onto the closed-enum string PromptState.Effort expects. Non-positive
// budgets map to "off"; positive budgets keep their numeric value so
// break attribution shows the exact tier flip.
func effortFromThinking(budget int) string {
	if budget <= 0 {
		return "off"
	}
	return fmt.Sprintf("%d", budget)
}

// cacheScopeLabel projects the EnableCache boolean onto the scope
// string PromptState carries. "ephemeral" matches the cache_control
// type set in buildAnthropicRequest; empty string denotes no caching.
func cacheScopeLabel(enableCache bool) string {
	if enableCache {
		return "ephemeral"
	}
	return ""
}

// toPromptCacheTools projects []ToolDef onto the []promptcache.Tool
// shape expected by RecordPromptState. Schema bytes are shared (not
// copied) because PromptState does not retain the slice beyond hash
// computation.
func toPromptCacheTools(tools []ToolDef) []promptcache.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]promptcache.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, promptcache.Tool{Name: t.Name, Schema: t.InputSchema})
	}
	return out
}

// recordPromptCacheEvent runs the two-phase break detector and hands the
// result to the configured sink. lastStates is updated in the same
// critical section so concurrent Stream calls on different sessions do
// not race on the map. Sink errors are logged to stderr but do not fail
// the API call — telemetry must never kill the happy path.
func (p *AnthropicProvider) recordPromptCacheEvent(ctx context.Context, sessionID string, current *promptcache.PromptState, usage promptcache.Usage, model string) {
	if p.promptCacheSink == nil || sessionID == "" || current == nil {
		return
	}
	p.promptCacheMu.Lock()
	prior := p.lastStates[sessionID]
	p.lastStates[sessionID] = current
	p.promptCacheMu.Unlock()

	resp := promptcache.RecordResponse(usage, time.Now().UTC())
	report := promptcache.CheckForCacheBreak(prior, current, resp)

	if err := p.promptCacheSink.Record(ctx, sessionID, promptcache.Event{
		Timestamp: resp.ReceivedAt,
		Model:     model,
		Report:    report,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "anthropic: prompt-cache sink record failed for session=%s: %v\n", sessionID, err)
	}
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

	effectiveModel := req.Model
	if effectiveModel == "" {
		effectiveModel = p.model
	}
	if opts1MContext(effectiveModel) {
		appendAnthropicBeta(httpReq.Header, anthropicContext1MBeta)
	}

	// Pre-call prompt-cache snapshot. Only captured when a sink is wired
	// AND the caller supplied a SessionID — otherwise telemetry is a
	// no-op and Stream pays none of the RecordPromptState cost.
	var preState *promptcache.PromptState
	if p.promptCacheSink != nil && req.SessionID != "" {
		preState = promptcache.RecordPromptState(promptcache.Input{
			Model:      effectiveModel,
			APIModel:   apiModelID(effectiveModel),
			System:     req.System,
			Tools:      toPromptCacheTools(req.Tools),
			Betas:      effectiveBetas(p.apiKey, effectiveModel),
			Effort:     effortFromThinking(req.ThinkingBudget),
			CacheScope: cacheScopeLabel(req.EnableCache),
		})
	}

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
			if opts1MContext(effectiveModel) {
				// Single-shot fallback: retry on base model when the 1M-context
				// beta is rate-limited. audit.txt §01 treats [1m] as opt-in;
				// graceful downgrade keeps the session running at 200K.
				fallbackReq := req
				fallbackReq.Model = apiModelID(effectiveModel)
				return p.Stream(ctx, fallbackReq, cb)
			}
			inner := fmt.Errorf("anthropic: rate limit (429): %s", errBody)
			return userfacingerr.Wrap(userfacingerr.ELN080, inner, "anthropic 429")
		case 529:
			return fmt.Errorf("anthropic: overloaded (529): %s", errBody)
		default:
			return fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, errBody)
		}
	}

	// Sniff usage from the stream so the post-call sink record carries
	// cache-attribution tokens. The wrapper is zero-cost when no sink
	// was wired (preState is nil → original cb forwarded unchanged).
	sseCb := cb
	var capturedUsage promptcache.Usage
	if preState != nil {
		sseCb = func(ev StreamEvent) {
			if ev.Type == EventDone && ev.Usage != nil {
				capturedUsage = promptcache.Usage{
					InputTokens:              ev.Usage.InputTokens,
					OutputTokens:             ev.Usage.OutputTokens,
					CacheReadInputTokens:     ev.Usage.CacheRead,
					CacheCreationInputTokens: ev.Usage.CacheWrite,
				}
			}
			cb(ev)
		}
	}

	if err := parseAnthropicSSE(resp.Body, sseCb); err != nil {
		return err
	}

	// On successful completion, record the prompt-cache event. 429
	// fallback paths return from the switch above and never reach here,
	// so we record exactly once per turn (the final successful attempt).
	if preState != nil {
		p.recordPromptCacheEvent(ctx, req.SessionID, preState, capturedUsage, effectiveModel)
	}
	return nil
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
		Model:     apiModelID(model),
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
		ID:              "claude-opus-4-7",
		Name:            "Claude Opus 4.7",
		MaxTokens:       32000,
		ContextWindow:   200_000,
		InputPricePerM:  15.0,
		OutputPricePerM: 75.0,
	},
	{
		ID:              "claude-opus-4-7" + anthropicModel1MSuffix,
		Name:            "Claude Opus 4.7 (1M context)",
		MaxTokens:       32000,
		ContextWindow:   1_000_000,
		InputPricePerM:  15.0,
		OutputPricePerM: 75.0,
	},
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
