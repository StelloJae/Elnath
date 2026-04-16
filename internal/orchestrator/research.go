package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/research"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/wiki"
)

// ResearchWorkflow runs the autoresearch loop: hypothesize → experiment → evaluate → wiki write.
type ResearchWorkflow struct {
	logger *slog.Logger
}

// NewResearchWorkflow returns a ResearchWorkflow ready to use.
func NewResearchWorkflow() *ResearchWorkflow {
	return &ResearchWorkflow{logger: slog.Default()}
}

// Name implements Workflow.
func (w *ResearchWorkflow) Name() string { return "research" }

// ResearchDeps carries the extra dependencies that the research workflow
// needs beyond the standard WorkflowInput. Callers attach it to
// WorkflowInput.Extra.
type ResearchDeps struct {
	WikiIndex     research.WikiSearcher
	WikiStore     *wiki.Store
	UsageTracker  *llm.UsageTracker
	LearningStore *learning.Store
	SelfState     *self.SelfState
	MaxRounds     int
	CostCapUSD    float64
}

// Run implements Workflow.
// It extracts the user message as the research topic, builds a research.Loop,
// and executes it. The result is returned as a WorkflowResult with the
// research summary as an assistant message.
func (w *ResearchWorkflow) Run(ctx context.Context, input WorkflowInput) (*WorkflowResult, error) {
	if input.Sink == nil {
		input.Sink = event.NopSink{}
	}
	topic := strings.TrimSpace(input.Message)
	if topic == "" {
		return nil, fmt.Errorf("research workflow: topic is required")
	}

	deps, _ := input.Extra.(*ResearchDeps)
	if deps == nil {
		// Fallback: run as a single agent workflow if research deps aren't wired.
		w.logger.Warn("research deps not provided, falling back to single workflow")
		return NewSingleWorkflow().Run(ctx, input)
	}

	hypGen := research.NewHypothesisGenerator(input.Provider, input.Config.Model, w.logger)
	expRunner := research.NewExperimentRunner(input.Provider, input.Tools, input.Config.Model, w.logger).
		WithSink(input.Sink)
	if input.Config.ToolExecutor != nil {
		expRunner.WithToolExecutor(input.Config.ToolExecutor)
	}

	var opts []research.LoopOption
	if deps.MaxRounds > 0 {
		opts = append(opts, research.WithMaxRounds(deps.MaxRounds))
	}
	if deps.CostCapUSD > 0 {
		opts = append(opts, research.WithCostCap(deps.CostCapUSD))
	}
	if input.Session != nil {
		opts = append(opts, research.WithSessionID(input.Session.ID))
	}
	opts = append(opts, research.WithSink(input.Sink))

	loop := research.NewLoop(
		hypGen,
		expRunner,
		deps.WikiIndex,
		deps.WikiStore,
		deps.UsageTracker,
		input.Provider,
		input.Config.Model,
		w.logger,
		opts...,
	)

	result, err := loop.Run(ctx, topic)
	if err != nil {
		return nil, fmt.Errorf("research workflow: %w", err)
	}
	research.ApplyLearning(result, deps.LearningStore, deps.SelfState, w.logger)

	messages := append(input.Messages,
		llm.NewUserMessage(input.Message),
		llm.NewAssistantMessage(result.Summary),
	)

	return &WorkflowResult{
		Messages: messages,
		Summary:  result.Summary,
		Usage:    totalResearchUsage(result),
		Workflow: w.Name(),
	}, nil
}

func totalResearchUsage(r *research.ResearchResult) llm.UsageStats {
	var total llm.UsageStats
	for _, rr := range r.Rounds {
		total.InputTokens += rr.Result.Usage.InputTokens
		total.OutputTokens += rr.Result.Usage.OutputTokens
		total.CacheRead += rr.Result.Usage.CacheRead
		total.CacheWrite += rr.Result.Usage.CacheWrite
	}
	return total
}

// Ensure the interface is satisfied at compile time.
var _ Workflow = (*ResearchWorkflow)(nil)
