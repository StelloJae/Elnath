package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

const (
	defaultMaxIterations = 50
	defaultMaxTokens     = 8192

	retryMaxAttempts = 3
	retryBaseDelay   = time.Second
)

// Agent runs the message→LLM→tools→repeat loop.
// The message array is the ONLY state — no hidden state machines.
type Agent struct {
	provider      llm.Provider
	tools         *tools.Registry
	permission    *Permission
	hooks         *HookRegistry
	model         string
	systemPrompt  string
	maxIterations int
	logger        *slog.Logger
}

// Option configures an Agent.
type Option func(*Agent)

// WithModel overrides the model string sent in each request.
func WithModel(model string) Option {
	return func(a *Agent) { a.model = model }
}

// WithSystemPrompt sets the system prompt for every request.
func WithSystemPrompt(prompt string) Option {
	return func(a *Agent) { a.systemPrompt = prompt }
}

// WithMaxIterations overrides the default iteration cap.
func WithMaxIterations(n int) Option {
	return func(a *Agent) { a.maxIterations = n }
}

// WithLogger sets a custom structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(a *Agent) { a.logger = l }
}

// WithPermission attaches a permission engine.
func WithPermission(p *Permission) Option {
	return func(a *Agent) { a.permission = p }
}

// WithHooks attaches a hook registry for tool execution lifecycle events.
func WithHooks(h *HookRegistry) Option {
	return func(a *Agent) { a.hooks = h }
}

// New creates an Agent with the given provider and tool registry.
func New(provider llm.Provider, reg *tools.Registry, opts ...Option) *Agent {
	a := &Agent{
		provider:      provider,
		tools:         reg,
		maxIterations: defaultMaxIterations,
		logger:        slog.Default(),
	}
	for _, o := range opts {
		o(a)
	}
	if a.permission == nil {
		a.permission = NewPermission()
	}
	return a
}

// RunResult is returned after the agent loop completes.
type RunResult struct {
	Messages []llm.Message
	Usage    llm.UsageStats
}

// Run executes the agent loop starting with the provided messages.
// It streams output via the onText callback and returns when the model
// stops requesting tool calls or maxIterations is reached.
func (a *Agent) Run(ctx context.Context, messages []llm.Message, onText func(string)) (*RunResult, error) {
	// Build tool definitions from the registry.
	toolDefs := buildToolDefs(a.tools)

	totalUsage := llm.UsageStats{}

	for iter := 0; iter < a.maxIterations; iter++ {
		req := llm.Request{
			Model:       a.model,
			Messages:    messages,
			Tools:       toolDefs,
			System:      a.systemPrompt,
			MaxTokens:   defaultMaxTokens,
			EnableCache: a.provider.Name() == "anthropic",
		}

		assistantMsg, usage, err := a.streamWithRetry(ctx, req, onText)
		if err != nil {
			return nil, err
		}

		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		totalUsage.CacheRead += usage.CacheRead
		totalUsage.CacheWrite += usage.CacheWrite

		messages = append(messages, assistantMsg)

		// Collect tool calls from the assistant message.
		toolCalls := llm.ExtractToolUseBlocks(assistantMsg)
		if len(toolCalls) == 0 {
			// No tool calls — the model is done.
			break
		}

		// Execute each tool call and collect results.
		messages, err = a.executeTools(ctx, messages, toolCalls, onText)
		if err != nil {
			return nil, err
		}
	}

	if len(messages) > 0 {
		last := messages[len(messages)-1]
		if last.Role == llm.RoleAssistant && len(llm.ExtractToolUseBlocks(last)) > 0 {
			return nil, fmt.Errorf("%w: model kept requesting tools after %d iterations",
				core.ErrMaxIterations, a.maxIterations)
		}
	}

	// Run on-stop hooks.
	if a.hooks != nil {
		if err := a.hooks.RunOnStop(ctx); err != nil {
			a.logger.Warn("on-stop hook error", "error", err)
		}
	}

	return &RunResult{Messages: messages, Usage: totalUsage}, nil
}

// streamWithRetry calls the provider with exponential backoff on 429/5xx errors.
func (a *Agent) streamWithRetry(ctx context.Context, req llm.Request, onText func(string)) (llm.Message, llm.UsageStats, error) {
	var lastErr error
	delay := retryBaseDelay
	currentReq := req

	for attempt := 0; attempt < retryMaxAttempts; attempt++ {
		reqForAttempt := currentReq
		if attempt > 0 {
			a.logger.Warn("retrying after provider error",
				"attempt", attempt,
				"delay", delay,
				"error", lastErr,
			)
			select {
			case <-ctx.Done():
				return llm.Message{}, llm.UsageStats{}, ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}

		msg, usage, err := a.stream(ctx, reqForAttempt, onText)
		if err == nil {
			if isEmptyAssistantMessage(msg) {
				fallbackMsg, fallbackUsage, fbErr := a.chatFallback(ctx, reqForAttempt, onText)
				if fbErr == nil && !isEmptyAssistantMessage(fallbackMsg) {
					return fallbackMsg, fallbackUsage, nil
				}
				if fbErr == nil {
					fbErr = fmt.Errorf("empty assistant response")
				}
				currentReq.Messages = append(reqForAttempt.Messages, llm.NewUserMessage(
					"The previous attempt returned an empty response. You must either provide a concrete answer or call tools. Do not return empty content.",
				))
				lastErr = fbErr
				continue
			}
			return msg, usage, nil
		}

		if isRetryable(err) {
			lastErr = err
			continue
		}
		return llm.Message{}, llm.UsageStats{}, err
	}

	return llm.Message{}, llm.UsageStats{}, fmt.Errorf("%w: %w", core.ErrProviderError, lastErr)
}

func (a *Agent) chatFallback(ctx context.Context, req llm.Request, onText func(string)) (llm.Message, llm.UsageStats, error) {
	resp, err := a.provider.Chat(ctx, llm.ChatRequest(req))
	if err != nil {
		return llm.Message{}, llm.UsageStats{}, fmt.Errorf("chat fallback: %w", err)
	}

	if onText != nil && resp.Content != "" {
		onText(resp.Content)
	}

	toolCalls := make([]llm.CompletedToolCall, 0, len(resp.ToolCalls))
	for _, tc := range resp.ToolCalls {
		toolCalls = append(toolCalls, llm.CompletedToolCall{
			ID:    tc.ID,
			Name:  tc.Name,
			Input: tc.Input,
		})
	}

	msg := llm.BuildAssistantMessage([]string{resp.Content}, toolCalls)
	return msg, llm.UsageStats{
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		CacheRead:    resp.Usage.CacheRead,
		CacheWrite:   resp.Usage.CacheWrite,
	}, nil
}

func isEmptyAssistantMessage(msg llm.Message) bool {
	return len(msg.Content) == 0 || (msg.Text() == "" && len(llm.ExtractToolUseBlocks(msg)) == 0)
}

// stream calls the provider once and accumulates the response into a Message.
func (a *Agent) stream(ctx context.Context, req llm.Request, onText func(string)) (llm.Message, llm.UsageStats, error) {
	var (
		textParts []string
		toolCalls []llm.CompletedToolCall
		usage     llm.UsageStats

		// in-progress tool call accumulation keyed by ID
		pendingTools = map[string]*llm.CompletedToolCall{}
	)

	err := a.provider.Stream(ctx, req, func(ev llm.StreamEvent) {
		switch ev.Type {
		case llm.EventTextDelta:
			if ev.Content != "" {
				textParts = append(textParts, ev.Content)
				if onText != nil {
					onText(ev.Content)
				}
			}

		case llm.EventToolUseStart:
			if ev.ToolCall != nil {
				pendingTools[ev.ToolCall.ID] = &llm.CompletedToolCall{
					ID:   ev.ToolCall.ID,
					Name: ev.ToolCall.Name,
				}
			}

		case llm.EventToolUseDelta:
			if ev.ToolCall != nil {
				if tc, ok := pendingTools[ev.ToolCall.ID]; ok {
					tc.Input += ev.ToolCall.Input
				}
			}

		case llm.EventToolUseDone:
			if ev.ToolCall != nil {
				if tc, ok := pendingTools[ev.ToolCall.ID]; ok {
					// Use the fully accumulated input from EventToolUseDone
					// if it carries the complete JSON (Anthropic sends the
					// final accumulated string here).
					if ev.ToolCall.Input != "" {
						tc.Input = ev.ToolCall.Input
					}
					toolCalls = append(toolCalls, *tc)
					delete(pendingTools, ev.ToolCall.ID)
				}
			}

		case llm.EventDone:
			if ev.Usage != nil {
				usage = *ev.Usage
			}
		}
	})

	if err != nil {
		return llm.Message{}, llm.UsageStats{}, err
	}

	msg := llm.BuildAssistantMessage(textParts, toolCalls)
	return msg, usage, nil
}

// toolExecResult holds the result of a single tool execution for ordered collection.
type toolExecResult struct {
	id      string
	output  string
	isError bool
}

func (a *Agent) executeTools(ctx context.Context, messages []llm.Message, calls []llm.ToolUseBlock, onText func(string)) ([]llm.Message, error) {
	results, approved, err := a.collectApprovedToolCalls(ctx, calls, onText)
	if err != nil {
		return nil, err
	}
	if err := a.executeApprovedToolCalls(ctx, approved, results); err != nil {
		return nil, err
	}
	return a.appendToolResults(messages, results), nil
}

func (a *Agent) collectApprovedToolCalls(ctx context.Context, calls []llm.ToolUseBlock, onText func(string)) ([]toolExecResult, []approvedToolCall, error) {
	results := make([]toolExecResult, len(calls))
	approved := make([]approvedToolCall, 0, len(calls))

	for i, call := range calls {
		a.logger.Debug("checking tool", "name", call.Name, "id", call.ID)
		if onText != nil {
			preview := extractToolPreview(call.Name, string(call.Input))
			ev := daemon.ToolProgressEvent(call.Name, preview)
			onText(daemon.EncodeProgressEvent(ev))
		}

		allowed, err := a.permission.Check(ctx, call.Name, call.Input)
		if err != nil {
			return nil, nil, fmt.Errorf("permission check: %w", err)
		}
		if !allowed {
			results[i] = toolExecResult{id: call.ID, output: fmt.Sprintf("permission denied for tool %q", call.Name), isError: true}
			continue
		}

		if a.hooks != nil {
			hookResult, hookErr := a.hooks.RunPreToolUse(ctx, call.Name, call.Input)
			if hookErr != nil {
				return nil, nil, fmt.Errorf("pre-tool hook: %w", hookErr)
			}
			if hookResult.Action == HookDeny {
				results[i] = toolExecResult{id: call.ID, output: fmt.Sprintf("hook denied tool %q: %s", call.Name, hookResult.Message), isError: true}
				continue
			}
		}

		approved = append(approved, approvedToolCall{call: call, index: i})
	}

	return results, approved, nil
}

func (a *Agent) appendToolResults(messages []llm.Message, results []toolExecResult) []llm.Message {
	for _, r := range results {
		if r.id == "" {
			continue
		}
		a.logger.Debug("tool result", "id", r.id, "is_error", r.isError)
		messages = llm.AppendToolResult(messages, r.id, r.output, r.isError)
	}
	return messages
}

// buildToolDefs converts the tools.Registry into []llm.ToolDef.
func buildToolDefs(reg *tools.Registry) []llm.ToolDef {
	all := reg.List()
	defs := make([]llm.ToolDef, 0, len(all))
	for _, t := range all {
		defs = append(defs, llm.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
		})
	}
	return defs
}

// isRetryable returns true for errors that warrant an automatic retry.
// We treat provider errors conservatively: retry only when the error
// message suggests a rate-limit (429) or transient server error (5xx).
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{"429", "500", "502", "503", "504", "rate limit", "rate_limit"} {
		for i := 0; i <= len(msg)-len(marker); i++ {
			if msg[i:i+len(marker)] == marker {
				return true
			}
		}
	}
	return false
}

func extractToolPreview(toolName, input string) string {
	input = strings.TrimSpace(input)
	if input == "" || input == "{}" {
		return ""
	}
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(input), &fields); err != nil {
		return ""
	}
	var preview string
	switch toolName {
	case "bash":
		if v, ok := fields["command"].(string); ok {
			preview = v
		}
	case "file_read", "file_write", "file_edit":
		if v, ok := fields["path"].(string); ok {
			preview = v
		}
	case "web_search":
		if v, ok := fields["query"].(string); ok {
			preview = v
		}
	case "git":
		if v, ok := fields["args"].(string); ok {
			preview = v
		}
	default:
		for _, key := range []string{"path", "command", "query", "name", "url"} {
			if v, ok := fields[key].(string); ok && v != "" {
				preview = v
				break
			}
		}
	}
	if len([]rune(preview)) > 40 {
		preview = string([]rune(preview)[:37]) + "..."
	}
	return preview
}
