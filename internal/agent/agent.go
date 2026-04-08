package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/stello/elnath/internal/core"
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
		messages, err = a.executeTools(ctx, messages, toolCalls)
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

	for attempt := 0; attempt < retryMaxAttempts; attempt++ {
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

		msg, usage, err := a.stream(ctx, req, onText)
		if err == nil {
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

// executeTools runs each tool call, checks permissions, and appends results.
func (a *Agent) executeTools(ctx context.Context, messages []llm.Message, calls []llm.ToolUseBlock) ([]llm.Message, error) {
	for _, call := range calls {
		a.logger.Debug("executing tool", "name", call.Name, "id", call.ID)

		// Permission check.
		allowed, err := a.permission.Check(ctx, call.Name, call.Input)
		if err != nil {
			return nil, fmt.Errorf("permission check: %w", err)
		}
		if !allowed {
			messages = llm.AppendToolResult(messages, call.ID,
				fmt.Sprintf("permission denied for tool %q", call.Name), true)
			continue
		}

		// Pre-tool-use hooks.
		if a.hooks != nil {
			hookResult, hookErr := a.hooks.RunPreToolUse(ctx, call.Name, call.Input)
			if hookErr != nil {
				return nil, fmt.Errorf("pre-tool hook: %w", hookErr)
			}
			if hookResult.Action == HookDeny {
				messages = llm.AppendToolResult(messages, call.ID,
					fmt.Sprintf("hook denied tool %q: %s", call.Name, hookResult.Message), true)
				continue
			}
		}

		result, err := a.tools.Execute(ctx, call.Name, call.Input)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", core.ErrToolExecution, call.Name, err)
		}

		// Post-tool-use hooks.
		if a.hooks != nil {
			if hookErr := a.hooks.RunPostToolUse(ctx, call.Name, call.Input, result); hookErr != nil {
				a.logger.Warn("post-tool hook error", "tool", call.Name, "error", hookErr)
			}
		}

		a.logger.Debug("tool result",
			"name", call.Name,
			"id", call.ID,
			"is_error", result.IsError,
		)

		messages = llm.AppendToolResult(messages, call.ID, result.Output, result.IsError)
	}
	return messages, nil
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
