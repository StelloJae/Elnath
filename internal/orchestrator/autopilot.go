package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/stello/elnath/internal/llm"
)

// AutopilotWorkflow executes a four-stage pipeline: plan → code → test → verify.
// Each stage appends its results to the shared message history so that later
// stages have full context. If any stage fails the workflow returns partial
// results with an error summary rather than halting immediately, so callers
// receive as much progress as possible.
type AutopilotWorkflow struct {
	logger *slog.Logger
}

// NewAutopilotWorkflow returns an AutopilotWorkflow ready to use.
func NewAutopilotWorkflow() *AutopilotWorkflow {
	return &AutopilotWorkflow{logger: slog.Default()}
}

// Name implements Workflow.
func (w *AutopilotWorkflow) Name() string { return "autopilot" }

// stage is one step in the autopilot pipeline.
type stage struct {
	name        string
	instruction func(originalTask string) string
}

var autopilotStages = []stage{
	{
		name: "plan",
		instruction: func(task string) string {
			return fmt.Sprintf(`You are a senior software engineer. Create a detailed implementation plan for the following task.

Task: %s

Produce:
1. A brief analysis of the requirements.
2. A step-by-step implementation plan with clearly numbered steps.
3. Identification of any risks or edge cases.

Do NOT write any code yet — only the plan.`, task)
		},
	},
	{
		name: "code",
		instruction: func(task string) string {
			return fmt.Sprintf(`You are a senior software engineer. Using the implementation plan above, write the code to complete the following task.

Task: %s

Requirements:
- Follow the plan exactly.
- Write complete, working code.
- Include all necessary imports and dependencies.
- Handle errors appropriately.`, task)
		},
	},
	{
		name: "test",
		instruction: func(task string) string {
			return `Using the implementation above, run the tests or write and execute test code to verify correctness.

If the project has an existing test suite, run it now. Otherwise write minimal tests that cover the critical paths and run them.

Report: what was tested, what passed, and what (if anything) failed.`
		},
	},
	{
		name: "verify",
		instruction: func(task string) string {
			return fmt.Sprintf(`Review the complete implementation and test results above for the following task.

Original task: %s

Verify:
1. Does the implementation fully address the original task?
2. Are there any bugs, edge cases, or omissions?
3. Are the tests adequate?

Provide a final summary stating whether the task is COMPLETE or INCOMPLETE, and why.`, task)
		},
	},
}

// Run implements Workflow.
// It executes each stage sequentially, threading the message history through
// all stages. A failed stage is captured as an error summary appended to the
// messages; subsequent stages still run so partial results are available.
func (w *AutopilotWorkflow) Run(ctx context.Context, input WorkflowInput) (*WorkflowResult, error) {
	single := NewSingleWorkflow()
	totalUsage := llm.UsageStats{}

	// Seed the history with the original user request.
	messages := append(input.Messages, llm.NewUserMessage(input.Message))

	for _, s := range autopilotStages {
		w.logger.Info("autopilot: running stage", "stage", s.name)
		if input.OnText != nil {
			input.OnText(fmt.Sprintf("[autopilot] stage: %s\n", s.name))
		}

		instruction := s.instruction(input.Message)
		stageInput := WorkflowInput{
			Message:  instruction,
			Messages: messages,
			Session:  input.Session,
			Tools:    input.Tools,
			Provider: input.Provider,
			Config:   input.Config,
			OnText:   input.OnText,
		}

		result, err := single.Run(ctx, stageInput)
		if err != nil {
			w.logger.Warn("autopilot: stage failed", "stage", s.name, "error", err)
			errSummary := fmt.Sprintf("Autopilot stage %q failed: %v", s.name, err)
			if input.OnText != nil {
				input.OnText(fmt.Sprintf("[autopilot] %s\n", errSummary))
			}
			messages = append(messages, llm.NewAssistantMessage(errSummary))
			return &WorkflowResult{
				Messages: messages,
				Summary:  errSummary,
				Usage:    totalUsage,
				Workflow: w.Name(),
			}, fmt.Errorf("autopilot stage %q failed: %w", s.name, err)
		}

		totalUsage.InputTokens += result.Usage.InputTokens
		totalUsage.OutputTokens += result.Usage.OutputTokens
		totalUsage.CacheRead += result.Usage.CacheRead
		totalUsage.CacheWrite += result.Usage.CacheWrite

		messages = result.Messages
	}

	summary, summaryUsage := synthesizeAssistantSummary(ctx, input.Provider, input.Message, messages, input.OnText)
	totalUsage.InputTokens += summaryUsage.InputTokens
	totalUsage.OutputTokens += summaryUsage.OutputTokens

	return &WorkflowResult{
		Messages: messages,
		Summary:  summary,
		Usage:    totalUsage,
		Workflow: w.Name(),
	}, nil
}

// synthesizeAssistantSummary streams a user-friendly completion message in
// assistant tone. Tokens are flushed through onText as [summary] events so
// the Telegram sink can display them progressively.
func synthesizeAssistantSummary(ctx context.Context, provider llm.Provider, originalTask string, messages []llm.Message, onText func(string)) (string, llm.UsageStats) {
	fallback := extractSummary(messages)

	taskSnippet := originalTask
	if len(taskSnippet) > 300 {
		taskSnippet = taskSnippet[:300] + "..."
	}

	verifySnippet := fallback
	if len(verifySnippet) > 500 {
		verifySnippet = verifySnippet[:500] + "..."
	}

	prompt := fmt.Sprintf(`Task completed. Write a brief completion message as a personal AI assistant.

Original request: %s

Result: %s

Rules:
- First-person assistant tone ("완료했습니다!", "~를 만들었습니다", "~를 수정했어요")
- Focus on concrete deliverables (files created, features added, bugs fixed)
- No review language ("검토 결과", "확인 완료", "문제 없음", "COMPLETE")
- 1-3 sentences, under 80 words`, taskSnippet, verifySnippet)

	if onText != nil {
		onText("[autopilot] stage: summary\n")
	}

	slog.Info("autopilot: summary synthesis starting")
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Messages:  []llm.Message{llm.NewUserMessage(prompt)},
		MaxTokens: 200,
	})
	if err != nil {
		slog.Warn("autopilot: summary Chat failed", "error", err)
		return fallback, llm.UsageStats{}
	}
	if resp.Content == "" {
		slog.Warn("autopilot: summary Chat returned empty")
		return fallback, llm.UsageStats{}
	}
	slog.Info("autopilot: summary synthesized", "len", len(resp.Content))

	content := dedupSummary(strings.TrimSpace(resp.Content))
	if content == "" {
		return fallback, llm.UsageStats{}
	}

	return content, llm.UsageStats{
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
	}
}

// dedupSummary removes exact duplication where the LLM repeats the same text.
func dedupSummary(s string) string {
	s = strings.TrimSpace(s)
	n := len(s)
	if n < 20 {
		return s
	}
	for i := n * 4 / 10; i <= n*6/10; i++ {
		first := strings.TrimSpace(s[:i])
		second := strings.TrimSpace(s[i:])
		if first == second {
			return first
		}
	}
	return s
}
