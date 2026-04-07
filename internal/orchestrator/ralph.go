package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/llm"
)

const defaultMaxAttempts = 5

// RalphWorkflow runs the SingleWorkflow in a verify-and-retry loop.
// After each execution it asks the LLM whether the result is complete and
// correct. If the verifier says yes, it returns. Otherwise it appends the
// feedback to the message history and retries, up to MaxAttempts times.
type RalphWorkflow struct {
	MaxAttempts int
	logger      *slog.Logger
}

// NewRalphWorkflow returns a RalphWorkflow with the default attempt limit.
func NewRalphWorkflow() *RalphWorkflow {
	return &RalphWorkflow{
		MaxAttempts: defaultMaxAttempts,
		logger:      slog.Default(),
	}
}

// Name implements Workflow.
func (w *RalphWorkflow) Name() string { return "ralph" }

// Run implements Workflow.
// Each iteration:
//  1. Runs SingleWorkflow.Run with the current message history.
//  2. Calls verify() to ask the LLM whether the result is satisfactory.
//  3. If verified, returns the result.
//  4. If not, appends the verification feedback as a user message and retries.
func (w *RalphWorkflow) Run(ctx context.Context, input WorkflowInput) (*WorkflowResult, error) {
	single := NewSingleWorkflow()
	totalUsage := llm.UsageStats{}
	maxAttempts := w.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}

	current := input

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		w.logger.Info("ralph workflow: attempt", "attempt", attempt, "max", maxAttempts)

		result, err := single.Run(ctx, current)
		if err != nil {
			return nil, fmt.Errorf("ralph workflow attempt %d: %w", attempt, err)
		}

		totalUsage.InputTokens += result.Usage.InputTokens
		totalUsage.OutputTokens += result.Usage.OutputTokens
		totalUsage.CacheRead += result.Usage.CacheRead
		totalUsage.CacheWrite += result.Usage.CacheWrite

		ok, feedback, verifyUsage, err := w.verify(ctx, input, result)
		if err != nil {
			return nil, fmt.Errorf("ralph workflow verify attempt %d: %w", attempt, err)
		}

		totalUsage.InputTokens += verifyUsage.InputTokens
		totalUsage.OutputTokens += verifyUsage.OutputTokens
		totalUsage.CacheRead += verifyUsage.CacheRead
		totalUsage.CacheWrite += verifyUsage.CacheWrite

		if ok {
			w.logger.Info("ralph workflow: verified", "attempt", attempt)
			result.Usage = totalUsage
			result.Workflow = w.Name()
			return result, nil
		}

		w.logger.Info("ralph workflow: not verified, retrying", "attempt", attempt, "feedback", feedback)

		// Append verification feedback so the next attempt has full context.
		feedbackMsg := fmt.Sprintf("Your previous response was not satisfactory. Feedback: %s\n\nPlease try again.", feedback)
		current = WorkflowInput{
			Message:  feedbackMsg,
			Messages: result.Messages,
			Session:  input.Session,
			Tools:    input.Tools,
			Provider: input.Provider,
			Config:   input.Config,
		}
	}

	return nil, fmt.Errorf("ralph workflow: task not verified after %d attempts", maxAttempts)
}

// verify asks the LLM to evaluate whether the workflow result satisfactorily
// answers the original task. Returns (ok, feedback, usage, err).
func (w *RalphWorkflow) verify(ctx context.Context, input WorkflowInput, result *WorkflowResult) (bool, string, llm.UsageStats, error) {
	lastAnswer := extractSummary(result.Messages)

	verifyPrompt := fmt.Sprintf(`You are a strict quality reviewer. Evaluate whether the following answer completely and correctly addresses the original task.

Original task: %s

Answer to evaluate:
%s

Respond with exactly one of:
  PASS — if the answer is complete, correct, and addresses the task fully.
  FAIL: <brief reason> — if the answer is incomplete, incorrect, or off-topic.

Your response must start with either PASS or FAIL.`, input.Message, lastAnswer)

	verifyMessages := []llm.Message{llm.NewUserMessage(verifyPrompt)}

	opts := agentOptions(WorkflowConfig{
		Model:         input.Config.Model,
		MaxIterations: 3,
	})
	a := agent.New(input.Provider, input.Tools, opts...)

	verifyResult, err := a.Run(ctx, verifyMessages, nil)
	if err != nil {
		return false, "", llm.UsageStats{}, fmt.Errorf("verifier agent: %w", err)
	}

	verdict := ""
	for i := len(verifyResult.Messages) - 1; i >= 0; i-- {
		if verifyResult.Messages[i].Role == llm.RoleAssistant {
			verdict = strings.TrimSpace(verifyResult.Messages[i].Text())
			break
		}
	}

	upper := strings.ToUpper(verdict)
	if strings.HasPrefix(upper, "PASS") {
		return true, "", verifyResult.Usage, nil
	}

	feedback := verdict
	if idx := strings.Index(verdict, ":"); idx >= 0 && idx < len(verdict)-1 {
		feedback = strings.TrimSpace(verdict[idx+1:])
	}

	return false, feedback, verifyResult.Usage, nil
}
