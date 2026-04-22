package orchestrator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
)

// SingleWorkflow is the simplest workflow: it creates one Agent and runs the
// message loop once. All tool calls are handled by that single agent.
type SingleWorkflow struct {
	logger *slog.Logger
}

// NewSingleWorkflow returns a SingleWorkflow ready to use.
func NewSingleWorkflow() *SingleWorkflow {
	return &SingleWorkflow{logger: slog.Default()}
}

// Name implements Workflow.
func (w *SingleWorkflow) Name() string { return "single" }

// Run implements Workflow.
// It appends the input message to the conversation history, creates an Agent
// with the provided config, runs the agent loop, and returns the updated
// message array together with usage stats.
func (w *SingleWorkflow) Run(ctx context.Context, input WorkflowInput) (*WorkflowResult, error) {
	if input.Sink == nil {
		input.Sink = event.NopSink{}
	}
	messages := append(input.Messages, llm.NewUserMessage(input.Message))

	opts := agentOptions(input.Config, input.Session)
	if input.Config.ContextWindow != nil && input.Config.CompressionMaxTokens > 0 {
		cw := input.Config.ContextWindow
		budget := input.Config.CompressionMaxTokens
		provider := input.Provider
		compressFn := func(ctx context.Context, msgs []llm.Message) ([]llm.Message, error) {
			return cw.CompressMessages(ctx, provider, msgs, budget)
		}
		opts = append(opts, agent.WithCompressFunc(compressFn))
	}
	a := agent.New(input.Provider, input.Tools, opts...)

	result, err := a.Run(ctx, messages, input.Sink)
	if err != nil {
		return nil, fmt.Errorf("single workflow: %w", err)
	}
	if input.Learning != nil {
		info := learning.AgentResultInfo{
			Topic:         firstMessageSnippet(input.Message, 80),
			FinishReason:  string(result.FinishReason),
			Iterations:    result.Iterations,
			MaxIterations: input.Config.MaxIterations,
			OutputTokens:  result.Usage.OutputTokens,
			InputTokens:   result.Usage.InputTokens,
			ToolStats:     toAgentToolStats(result.ToolStats),
			Workflow:      "single",
		}
		applyAgentLearning(prepareLearningDeps(input.Learning, input.Session, result.Messages, len(input.Messages), result.ToolStats), info)
	}

	summary := extractSummary(result.Messages)

	return &WorkflowResult{
		Messages:     result.Messages,
		Summary:      summary,
		Usage:        result.Usage,
		ToolStats:    result.ToolStats,
		Iterations:   result.Iterations,
		FinishReason: string(result.FinishReason),
		Workflow:     w.Name(),
	}, nil
}

// agentOptions converts a WorkflowConfig (+ optional session) into an
// agent.Option slice. When a session is supplied, its ID is threaded via
// agent.WithSessionID so provider telemetry can scope per-session
// (e.g. promptcache FileSink jsonl per session).
func agentOptions(cfg WorkflowConfig, session *agent.Session) []agent.Option {
	var opts []agent.Option
	if cfg.Model != "" {
		opts = append(opts, agent.WithModel(cfg.Model))
	}
	if cfg.SystemPrompt != "" {
		opts = append(opts, agent.WithSystemPrompt(cfg.SystemPrompt))
	}
	if cfg.MaxIterations > 0 {
		opts = append(opts, agent.WithMaxIterations(cfg.MaxIterations))
	}
	if cfg.Hooks != nil {
		opts = append(opts, agent.WithHooks(cfg.Hooks))
	}
	if cfg.Permission != nil {
		opts = append(opts, agent.WithPermission(cfg.Permission))
	}
	if cfg.ToolExecutor != nil {
		opts = append(opts, agent.WithToolExecutor(cfg.ToolExecutor))
	}
	if cfg.ReflectionEnqueuer != nil {
		opts = append(opts, agent.WithReflection(cfg.ReflectionEnqueuer))
	}
	if session != nil && session.ID != "" {
		opts = append(opts, agent.WithSessionID(session.ID))
	}
	return opts
}

// extractSummary returns the text of the last assistant message, truncated to
// 200 characters, for use as a human-readable summary.
func extractSummary(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleAssistant {
			text := messages[i].Text()
			if len(text) > 200 {
				text = text[:200] + "..."
			}
			return text
		}
	}
	return ""
}
