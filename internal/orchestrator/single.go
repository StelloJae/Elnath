package orchestrator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/stello/elnath/internal/agent"
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
	messages := append(input.Messages, llm.NewUserMessage(input.Message))

	opts := agentOptions(input.Config)
	a := agent.New(input.Provider, input.Tools, opts...)

	result, err := a.Run(ctx, messages, input.OnText)
	if err != nil {
		return nil, fmt.Errorf("single workflow: %w", err)
	}

	summary := extractSummary(result.Messages)

	return &WorkflowResult{
		Messages: result.Messages,
		Summary:  summary,
		Usage:    result.Usage,
		Workflow: w.Name(),
	}, nil
}

// agentOptions converts a WorkflowConfig into agent.Option slice.
func agentOptions(cfg WorkflowConfig) []agent.Option {
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
