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
	current.Learning = nil

	var accToolStatSlices [][]learning.AgentToolStat
	var lastFinishReason string
	totalIter := 0
	attemptsRun := 0
	verified := false
	var finalResult *WorkflowResult

	for a := 1; a <= maxAttempts; a++ {
		attemptsRun = a
		w.logger.Info("ralph workflow: attempt", "attempt", a, "max", maxAttempts)

		result, err := single.Run(ctx, current)
		if err != nil {
			return nil, fmt.Errorf("ralph workflow attempt %d: %w", a, err)
		}

		totalUsage.InputTokens += result.Usage.InputTokens
		totalUsage.OutputTokens += result.Usage.OutputTokens
		totalUsage.CacheRead += result.Usage.CacheRead
		totalUsage.CacheWrite += result.Usage.CacheWrite
		accToolStatSlices = append(accToolStatSlices, toAgentToolStats(result.ToolStats))
		totalIter += result.Iterations
		lastFinishReason = result.FinishReason
		finalResult = result

		ok, feedback, verifyUsage, err := w.verify(ctx, input, result)
		if err != nil {
			return nil, fmt.Errorf("ralph workflow verify attempt %d: %w", a, err)
		}

		totalUsage.InputTokens += verifyUsage.InputTokens
		totalUsage.OutputTokens += verifyUsage.OutputTokens
		totalUsage.CacheRead += verifyUsage.CacheRead
		totalUsage.CacheWrite += verifyUsage.CacheWrite

		if ok {
			w.logger.Info("ralph workflow: verified", "attempt", a)
			verified = true
			break
		}

		w.logger.Info("ralph workflow: not verified, retrying", "attempt", a, "feedback", feedback)

		// Append verification feedback so the next attempt has full context.
		feedbackMsg := buildRecoveryPrompt(input.Message, feedback)
		current = WorkflowInput{
			Message:  feedbackMsg,
			Messages: sanitizeRetryMessages(result.Messages),
			Session:  input.Session,
			Tools:    input.Tools,
			Provider: input.Provider,
			Config:   input.Config,
			Learning: nil,
		}
	}

	mergedToolStats := toWorkflowToolStats(learning.MergeAgentToolStats(accToolStatSlices...))
	if finalResult != nil {
		finalResult.Usage = totalUsage
		finalResult.ToolStats = mergedToolStats
		finalResult.Iterations = totalIter
		finalResult.FinishReason = lastFinishReason
		finalResult.Workflow = w.Name()
	}

	if input.Learning != nil {
		finishReason := lastFinishReason
		if !verified {
			finishReason = "ralph_cap_exceeded"
		}
		info := learning.AgentResultInfo{
			Topic:         firstMessageSnippet(input.Message, 80),
			FinishReason:  finishReason,
			Iterations:    totalIter,
			MaxIterations: input.Config.MaxIterations * attemptsRun,
			OutputTokens:  totalUsage.OutputTokens,
			InputTokens:   totalUsage.InputTokens,
			ToolStats:     learning.MergeAgentToolStats(accToolStatSlices...),
			RetryCount:    attemptsRun - 1,
			Workflow:      "ralph",
		}
		applyAgentLearning(input.Learning, info)
	}

	if !verified {
		return nil, fmt.Errorf("ralph workflow: task not verified after %d attempts", maxAttempts)
	}
	return finalResult, nil
}

// verify asks the LLM to evaluate whether the workflow result satisfactorily
// answers the original task. Returns (ok, feedback, usage, err).
func (w *RalphWorkflow) verify(ctx context.Context, input WorkflowInput, result *WorkflowResult) (bool, string, llm.UsageStats, error) {
	evidence := buildVerificationEvidence(result.Messages)

	verifyPrompt := fmt.Sprintf(`You are a strict quality reviewer. Evaluate whether the following execution evidence completely and correctly addresses the original task.

Original task: %s

Execution evidence:
%s

Use the evidence above, including tool results and verification output when present. Do not fail merely because the final assistant summary is concise if the execution evidence shows the task was completed correctly.

Respond with exactly one of:
  PASS — if the answer is complete, correct, and addresses the task fully.
  FAIL: <brief reason> — if the answer is incomplete, incorrect, or off-topic.

Your response must start with either PASS or FAIL.`, input.Message, evidence)

	verifyMessages := []llm.Message{llm.NewUserMessage(verifyPrompt)}

	opts := agentOptions(WorkflowConfig{
		Model:         input.Config.Model,
		MaxIterations: 3,
		Hooks:         input.Config.Hooks,
		Permission:    input.Config.Permission,
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

func buildRecoveryPrompt(originalTask, feedback string) string {
	return fmt.Sprintf(`Your previous response was not satisfactory.

Original task:
%s

Verifier feedback:
%s

Recovery guidance:
- stay tightly scoped to the original task
- prefer the smallest correct change over broad rewrites
- use repo-native tests or verification commands when available
- end with a concise final answer that names the modified files and the verification command/result
- if missing information would materially change the outcome or is costly to reverse, ask instead of guessing

Please try again with a corrected answer.`, originalTask, feedback)
}

func sanitizeRetryMessages(messages []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		if len(msg.Content) == 0 {
			continue
		}
		if msg.Role == llm.RoleAssistant && msg.Text() == "" && len(llm.ExtractToolUseBlocks(msg)) == 0 {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func buildVerificationEvidence(messages []llm.Message) string {
	const (
		maxAssistantChars = 4000
		maxToolChars      = 1200
		maxToolResults    = 4
	)

	assistantText := "(no assistant text returned)"
	toolEvidence := make([]string, 0, maxToolResults)

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if assistantText == "(no assistant text returned)" && msg.Role == llm.RoleAssistant {
			if text := strings.TrimSpace(msg.Text()); text != "" {
				assistantText = trimForEvidence(text, maxAssistantChars)
			}
		}
		if len(toolEvidence) >= maxToolResults {
			continue
		}
		for _, block := range msg.Content {
			tr, ok := block.(llm.ToolResultBlock)
			if !ok {
				continue
			}
			content := strings.TrimSpace(tr.Content)
			if content == "" {
				continue
			}
			label := "tool_result"
			if tr.IsError {
				label += " (error)"
			}
			toolEvidence = append(toolEvidence, fmt.Sprintf("- %s %s:\n%s", label, tr.ToolUseID, trimForEvidence(content, maxToolChars)))
			if len(toolEvidence) >= maxToolResults {
				break
			}
		}
	}

	reverseStrings(toolEvidence)

	var sb strings.Builder
	sb.WriteString("Final assistant answer:\n")
	sb.WriteString(assistantText)
	if len(toolEvidence) > 0 {
		sb.WriteString("\n\nRecent tool evidence:\n")
		sb.WriteString(strings.Join(toolEvidence, "\n\n"))
	}
	return sb.String()
}

func trimForEvidence(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func reverseStrings(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}
