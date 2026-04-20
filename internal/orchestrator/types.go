package orchestrator

import (
	"context"
	"log/slog"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/self"
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
	Sink     event.Sink   // typed event sink (use event.NopSink{} for silent)
	Extra    interface{}  // workflow-specific dependencies (e.g. *ResearchDeps)
	Learning *LearningDeps
}

type LearningDeps struct {
	Store          *learning.Store
	SelfState      *self.SelfState
	Logger         *slog.Logger
	LLMExtractor   learning.LLMExtractor
	CursorStore    *learning.CursorStore
	Breaker        *learning.Breaker
	ComplexityGate learning.ComplexityGate
	SessionID      string
	MessageCount   int
	ToolCallCount  int
	CompactSummary func() (text string, lastLine int)
	Redact         func(string) string
}

// WorkflowResult is the output of a completed workflow execution.
type WorkflowResult struct {
	Messages     []llm.Message // updated message array
	Summary      string        // human-readable summary of what was done
	Usage        llm.UsageStats
	ToolStats    []agent.ToolStat
	Iterations   int
	FinishReason string
	Workflow     string // which workflow was used
}

// ContextCompressor is the minimal interface workflows need to trigger message
// compaction mid-run. conversation.ContextWindow (and the runtime's
// hook-decorated wrapper) satisfies it; the orchestrator does not import
// conversation directly to avoid a package dependency cycle.
type ContextCompressor interface {
	CompressMessages(ctx context.Context, provider llm.Provider, messages []llm.Message, maxTokens int) ([]llm.Message, error)
}

// WorkflowConfig holds tuning parameters passed to every workflow.
type WorkflowConfig struct {
	MaxIterations        int
	MaxTokens            int
	Model                string
	SystemPrompt         string
	Hooks                *agent.HookRegistry
	Permission           *agent.Permission
	ToolExecutor         tools.Executor
	ContextWindow        ContextCompressor // nil disables agent-loop compaction
	CompressionMaxTokens int               // token budget passed to ContextWindow.CompressMessages

	// ReflectionEnqueuer, when non-nil, is forwarded to each agent.New call as
	// an observe-only hook (Phase 0 self-healing, spec §3.2). The enqueuer
	// must be non-blocking; runtime adapters typically wrap
	// internal/agent/reflection.Pool.Enqueue.
	ReflectionEnqueuer agent.ReflectionEnqueuer
}
