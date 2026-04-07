package orchestrator

import (
	"context"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

// Workflow is the interface all execution strategies must implement.
type Workflow interface {
	Name() string
	Run(ctx context.Context, input WorkflowInput) (*WorkflowResult, error)
}

// WorkflowInput carries everything a workflow needs to execute a task.
type WorkflowInput struct {
	Message  string
	Messages []llm.Message // conversation history
	Session  *agent.Session
	Tools    *tools.Registry
	Provider llm.Provider
	Config   WorkflowConfig
	Extra    interface{} // workflow-specific dependencies (e.g. *ResearchDeps)
}

// WorkflowResult is the output of a completed workflow execution.
type WorkflowResult struct {
	Messages []llm.Message // updated message array
	Summary  string        // human-readable summary of what was done
	Usage    llm.UsageStats
	Workflow string // which workflow was used
}

// WorkflowConfig holds tuning parameters passed to every workflow.
type WorkflowConfig struct {
	MaxIterations int
	MaxTokens     int
	Model         string
	SystemPrompt  string
}
