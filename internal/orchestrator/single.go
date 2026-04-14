package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/stello/elnath/internal/agent"
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
	messages := append(input.Messages, llm.NewUserMessage(input.Message))

	opts := agentOptions(input.Config)
	a := agent.New(input.Provider, input.Tools, opts...)

	result, err := a.Run(ctx, messages, input.OnText)
	if err != nil {
		return nil, fmt.Errorf("single workflow: %w", err)
	}
	w.applyLearning(input, result, input.Config.MaxIterations)

	summary := extractSummary(result.Messages)

	return &WorkflowResult{
		Messages: result.Messages,
		Summary:  summary,
		Usage:    result.Usage,
		Workflow: w.Name(),
	}, nil
}

func (w *SingleWorkflow) applyLearning(input WorkflowInput, result *agent.RunResult, maxIter int) {
	deps := input.Learning
	if deps == nil || deps.Store == nil || result == nil {
		return
	}

	info := learning.AgentResultInfo{
		Topic:         firstMessageSnippet(input.Message, 80),
		FinishReason:  string(result.FinishReason),
		Iterations:    result.Iterations,
		MaxIterations: maxIter,
		OutputTokens:  result.Usage.OutputTokens,
		InputTokens:   result.Usage.InputTokens,
		ToolStats:     toAgentToolStats(result.ToolStats),
	}
	lessons := learning.ExtractAgent(info)
	if len(lessons) == 0 {
		return
	}

	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}

	personaChanged := false
	for _, lesson := range lessons {
		if err := deps.Store.Append(lesson); err != nil {
			log.Warn("agent learning: append failed", "error", err)
			continue
		}
		if deps.SelfState != nil && len(lesson.PersonaDelta) > 0 {
			deps.SelfState.ApplyLessons(lesson.PersonaDelta)
			personaChanged = true
		}
	}

	if personaChanged && deps.SelfState != nil {
		if err := deps.SelfState.Save(); err != nil {
			log.Warn("agent learning: selfState save failed", "error", err)
		}
	}
}

func firstMessageSnippet(msg string, n int) string {
	msg = strings.TrimSpace(msg)
	if msg == "" || n <= 0 {
		return ""
	}
	runes := []rune(msg)
	if len(runes) <= n {
		return msg
	}
	return strings.TrimSpace(string(runes[:n]))
}

func toAgentToolStats(src []agent.ToolStat) []learning.AgentToolStat {
	out := make([]learning.AgentToolStat, 0, len(src))
	for _, stat := range src {
		out = append(out, learning.AgentToolStat{
			Name:      stat.Name,
			Calls:     stat.Calls,
			Errors:    stat.Errors,
			TotalTime: stat.TotalTime,
		})
	}
	return out
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
	if cfg.Hooks != nil {
		opts = append(opts, agent.WithHooks(cfg.Hooks))
	}
	if cfg.Permission != nil {
		opts = append(opts, agent.WithPermission(cfg.Permission))
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
