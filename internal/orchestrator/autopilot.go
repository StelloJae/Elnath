package orchestrator

import (
	"context"
	"fmt"
	"log/slog"

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

		instruction := s.instruction(input.Message)
		stageInput := WorkflowInput{
			Message:  instruction,
			Messages: messages,
			Session:  input.Session,
			Tools:    input.Tools,
			Provider: input.Provider,
			Config:   input.Config,
		}

		result, err := single.Run(ctx, stageInput)
		if err != nil {
			w.logger.Warn("autopilot: stage failed", "stage", s.name, "error", err)
			errSummary := fmt.Sprintf("Autopilot stopped at stage %q: %v", s.name, err)
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

	summary := extractSummary(messages)

	return &WorkflowResult{
		Messages: messages,
		Summary:  summary,
		Usage:    totalUsage,
		Workflow: w.Name(),
	}, nil
}
