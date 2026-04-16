package research

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

var _ daemon.TaskRunner = (*TaskRunner)(nil)

type TaskRunner struct {
	provider      llm.Provider
	model         string
	wikiIdx       WikiSearcher
	wikiStore     *wiki.Store
	usageTracker  *llm.UsageTracker
	toolReg       *tools.Registry
	toolExec      tools.Executor
	logger        *slog.Logger
	maxRounds     int
	costCapUSD    float64
	learningStore *learning.Store
	selfState     *self.SelfState
}

type TaskRunnerOption func(*TaskRunner)

func WithRunnerMaxRounds(n int) TaskRunnerOption {
	return func(r *TaskRunner) {
		if n > 0 {
			r.maxRounds = n
		}
	}
}

func WithRunnerCostCap(usd float64) TaskRunnerOption {
	return func(r *TaskRunner) {
		if usd > 0 {
			r.costCapUSD = usd
		}
	}
}

func WithToolRegistry(toolReg *tools.Registry) TaskRunnerOption {
	return func(r *TaskRunner) {
		if toolReg != nil {
			r.toolReg = toolReg
		}
	}
}

func WithToolExecutor(exec tools.Executor) TaskRunnerOption {
	return func(r *TaskRunner) {
		if exec != nil {
			r.toolExec = exec
		}
	}
}

func WithRunnerLearning(store *learning.Store) TaskRunnerOption {
	return func(r *TaskRunner) {
		r.learningStore = store
	}
}

func WithRunnerSelfState(s *self.SelfState) TaskRunnerOption {
	return func(r *TaskRunner) {
		r.selfState = s
	}
}

func NewTaskRunner(
	provider llm.Provider,
	model string,
	wikiIdx WikiSearcher,
	wikiStore *wiki.Store,
	usageTracker *llm.UsageTracker,
	logger *slog.Logger,
	opts ...TaskRunnerOption,
) *TaskRunner {
	if logger == nil {
		logger = slog.Default()
	}
	r := &TaskRunner{
		provider:     provider,
		model:        model,
		wikiIdx:      wikiIdx,
		wikiStore:    wikiStore,
		usageTracker: usageTracker,
		logger:       logger,
		maxRounds:    5,
		costCapUSD:   5.0,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *TaskRunner) Run(ctx context.Context, payload daemon.TaskPayload, sink event.Sink) (daemon.TaskRunnerResult, error) {
	topic := strings.TrimSpace(payload.Prompt)
	if topic == "" {
		return daemon.TaskRunnerResult{}, fmt.Errorf("research topic is required")
	}
	if r.provider == nil {
		return daemon.TaskRunnerResult{}, fmt.Errorf("research provider not configured")
	}
	if r.wikiIdx == nil || r.wikiStore == nil {
		return daemon.TaskRunnerResult{}, fmt.Errorf("research wiki not configured")
	}
	sessionID := payload.SessionID
	if sessionID == "" {
		sessionID = "research-" + uuid.NewString()
	}
	toolReg := r.toolReg
	if toolReg == nil {
		toolReg = tools.NewRegistry()
	}

	hg := NewHypothesisGenerator(r.provider, r.model, r.logger)
	er := NewExperimentRunner(r.provider, toolReg, r.model, r.logger).WithSink(sink)
	if r.toolExec != nil {
		er.WithToolExecutor(r.toolExec)
	}
	loop := NewLoop(
		hg,
		er,
		r.wikiIdx,
		r.wikiStore,
		r.usageTracker,
		r.provider,
		r.model,
		r.logger,
		WithMaxRounds(r.maxRounds),
		WithCostCap(r.costCapUSD),
		WithSink(sink),
		WithSessionID(sessionID),
	)

	result, err := loop.Run(ctx, topic)
	if err != nil {
		return daemon.TaskRunnerResult{}, err
	}
	r.applyLearning(result)
	raw, _ := json.Marshal(result)
	return daemon.TaskRunnerResult{
		Summary:   result.Summary,
		Result:    string(raw),
		SessionID: sessionID,
	}, nil
}

func (r *TaskRunner) applyLearning(result *ResearchResult) {
	if r == nil {
		return
	}
	ApplyLearning(result, r.learningStore, r.selfState, r.logger)
}
