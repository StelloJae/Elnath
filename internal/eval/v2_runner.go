package eval

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/routing"
)

// Phase 7.4a Milestone 2 synthetic success model.
//
// Success for a training outcome is drawn from a Bernoulli whose
// probability depends on whether the routed workflow matches the task's
// ExpectedWorkflow. The 0.9/0.3 strawman keeps the correctness signal
// dominant while leaving enough noise that the advisor needs >= a few
// samples to separate the two. Tune later if learning curves saturate.
const (
	synthCorrectSuccessRate = 0.9
	synthWrongSuccessRate   = 0.3
)

// uniqueWorkflows returns the corpus's ExpectedWorkflow set in sorted
// order so the epsilon-greedy sampler has a deterministic, corpus-
// defined draw space. Empty workflow fields are skipped; duplicates
// collapse.
func uniqueWorkflows(corpus *Corpus) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, task := range corpus.Tasks {
		if task.ExpectedWorkflow == "" {
			continue
		}
		if _, ok := seen[task.ExpectedWorkflow]; ok {
			continue
		}
		seen[task.ExpectedWorkflow] = struct{}{}
		out = append(out, task.ExpectedWorkflow)
	}
	sort.Strings(out)
	return out
}

// V2DefaultRunCount is the number of sequential training+held-out runs in
// one Phase 7.3 cycle. Paired with the Spearman primary metric, 10 runs is
// enough for a monotonic-trend threshold of 0.5 to have meaningful
// statistical weight.
const V2DefaultRunCount = 10

// V2AdvisorFactory produces a fresh RoutingAdvisor over the given scratch
// OutcomeStore. The v2 benchmark injects a custom factory so production
// code (which uses NewRoutingAdvisor's 30/5 defaults) does not leak its
// window saturation into the benchmark's cumulative-learning signal.
type V2AdvisorFactory func(store *learning.OutcomeStore) *learning.RoutingAdvisor

// DefaultV2AdvisorFactory constructs the production v2 benchmark advisor:
// windowSize=200 covers all 10 runs of 12-15 training outcomes without
// eviction, minSamples=3 lets recommendations emerge by run 2-3 under the
// 3-intent distribution.
func DefaultV2AdvisorFactory(store *learning.OutcomeStore) *learning.RoutingAdvisor {
	return learning.NewRoutingAdvisorWithConfig(store, 200, 3)
}

// V2RunOptions configure one benchmark cycle. AdvisorFactory defaults to
// DefaultV2AdvisorFactory, RunCount to V2DefaultRunCount, Clock to
// time.Now; tests may override all three for determinism.
//
// Router is optional (Phase 7.4). When non-nil, each training task is
// routed through the real decision path and the router's pick is what
// gets recorded as the outcome's Workflow. When nil, the legacy stub
// path (Phase 7.3) is used: Workflow is set to task.ExpectedWorkflow
// and Success is fixed at true.
//
// Epsilon (Phase 7.4a Milestone 2) is the per-training-task probability
// that the router's pick is replaced with a uniform-random draw from
// the corpus's workflow set. Values outside [0, 1] are clamped. Default
// 0 disables exploration — the router's decision stands as-is. Epsilon
// is benchmark-only; production Router never samples randomly.
//
// Rand (Phase 7.4a Milestone 2) is the random source consumed by
// epsilon-greedy and the Bernoulli success model. Tests MUST seed
// explicitly for determinism. When Router != nil and Rand == nil, RunV2
// seeds a default source from the clock — acceptable for the CLI path,
// avoided in tests.
type V2RunOptions struct {
	Corpus         *Corpus
	OutputDir      string
	RunCount       int
	AdvisorFactory V2AdvisorFactory
	Clock          func() time.Time
	Router         RealRouter
	Epsilon        float64
	Rand           *rand.Rand
}

// RunV2 executes one full benchmark cycle and returns the V2TimeSeries
// with Spearman coefficient, first3/last3 averages, and the verdict.
// Cycle invariants:
//   - The scratch OutcomeStore at <OutputDir>/scratch-outcomes.jsonl is
//     DELETED at cycle start so the advisor learns from a clean slate.
//   - Within the cycle, training outcomes accumulate across all runs; the
//     advisor therefore sees a growing sample each run.
//   - Held-out outcomes are NOT appended; they exist only for hit-rate
//     measurement so they cannot contaminate the advisor's training signal
//     via PreferenceUsed filtering.
func RunV2(opts V2RunOptions) (*V2TimeSeries, error) {
	if opts.Corpus == nil {
		return nil, fmt.Errorf("run v2: corpus is required")
	}
	if opts.Corpus.Version != "v2" {
		return nil, fmt.Errorf("run v2: corpus version %q is not v2", opts.Corpus.Version)
	}
	if err := opts.Corpus.Validate(); err != nil {
		return nil, fmt.Errorf("run v2: corpus invalid: %w", err)
	}
	if opts.OutputDir == "" {
		return nil, fmt.Errorf("run v2: output_dir is required")
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("run v2: create output dir: %w", err)
	}

	runCount := opts.RunCount
	if runCount <= 0 {
		runCount = V2DefaultRunCount
	}
	factory := opts.AdvisorFactory
	if factory == nil {
		factory = DefaultV2AdvisorFactory
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}

	scratchPath := filepath.Join(opts.OutputDir, "scratch-outcomes.jsonl")
	if err := os.Remove(scratchPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("run v2: clear scratch store: %w", err)
	}
	store := learning.NewOutcomeStore(scratchPath)
	advisor := factory(store)

	tasksByID := make(map[string]Task, len(opts.Corpus.Tasks))
	for _, t := range opts.Corpus.Tasks {
		tasksByID[t.ID] = t
	}

	// Phase 7.4a Milestone 2: epsilon-greedy + Bernoulli success need a
	// *rand.Rand. Only seed a default when the router path is active and
	// the caller did not inject one — tests always inject.
	rng := opts.Rand
	if opts.Router != nil && rng == nil {
		rng = rand.New(rand.NewSource(clock().UnixNano()))
	}
	epsilon := opts.Epsilon
	if epsilon < 0 {
		epsilon = 0
	}
	if epsilon > 1 {
		epsilon = 1
	}
	workflowSet := uniqueWorkflows(opts.Corpus)

	results := make([]V2RunResult, 0, runCount)
	successfulRuns := 0
	for i := 0; i < runCount; i++ {
		res, err := runV2SingleRun(store, advisor, opts.Corpus, tasksByID, i+1, clock(), opts.Router, rng, epsilon, workflowSet)
		if err != nil {
			results = append(results, V2RunResult{
				RunIndex:  i + 1,
				Timestamp: clock().UTC().Format(time.RFC3339),
			})
			continue
		}
		successfulRuns++
		results = append(results, *res)
	}

	series := &V2TimeSeries{Runs: results}
	if successfulRuns == 0 {
		series.Verdict = V2VerdictFail
		return series, fmt.Errorf("run v2: all %d runs failed (execution infra failure)", runCount)
	}

	hitRates := make([]float64, 0, len(results))
	for _, r := range results {
		hitRates = append(hitRates, r.HeldOutHitRate)
	}
	series.SpearmanCoeff, series.IsConstant = SpearmanRank(hitRates)
	series.First3Avg = meanFirstN(hitRates, 3)
	series.Last3Avg = meanLastN(hitRates, 3)
	series.Verdict = decideVerdict(series.SpearmanCoeff, series.IsConstant, series.First3Avg, series.Last3Avg)
	return series, nil
}

func runV2SingleRun(
	store *learning.OutcomeStore,
	advisor *learning.RoutingAdvisor,
	corpus *Corpus,
	tasksByID map[string]Task,
	runIndex int,
	now time.Time,
	router RealRouter,
	rng *rand.Rand,
	epsilon float64,
	workflowSet []string,
) (*V2RunResult, error) {
	// When a router is configured, consult the advisor once before the
	// training batch so its decisions can reflect whatever preference has
	// accumulated through prior runs. First run returns a nil preference
	// (empty store) — the router must tolerate that and fall back to its
	// default table.
	var currentPref *routing.WorkflowPreference
	if router != nil {
		pref, err := advisor.Advise(V2BenchmarkProjectID)
		if err != nil {
			return nil, fmt.Errorf("pre-training advise: %w", err)
		}
		currentPref = pref
	}

	// Training pass: append one outcome per training task.
	//
	// Legacy path (router == nil, Phase 7.3 stub): Workflow is the task's
	// ExpectedWorkflow and Success is always true, so the advisor sees a
	// tautological "intent X succeeds with workflow Y" signal.
	//
	// Real-router path (router != nil, Phase 7.4a Milestone 2): Workflow
	// comes from the router, then epsilon-greedy may override it with a
	// uniform draw from workflowSet, then Bernoulli success is drawn
	// against synthCorrectSuccessRate or synthWrongSuccessRate depending
	// on whether the final workflow matches the task's ExpectedWorkflow.
	// Randomness consumes rng in a strict (epsilon_draw, success_draw)
	// order so repeat runs with the same seed produce byte-identical
	// outcome sequences.
	for _, id := range corpus.TrainingSet {
		task, ok := tasksByID[id]
		if !ok {
			return nil, fmt.Errorf("training task %q missing from corpus", id)
		}
		workflow := task.ExpectedWorkflow
		success := true
		if router != nil {
			workflow = router.DecideWorkflow(task.Intent, currentPref)
			if epsilon > 0 && rng != nil && len(workflowSet) > 0 && rng.Float64() < epsilon {
				workflow = workflowSet[rng.Intn(len(workflowSet))]
			}
			if rng != nil {
				p := synthWrongSuccessRate
				if workflow == task.ExpectedWorkflow {
					p = synthCorrectSuccessRate
				}
				success = rng.Float64() < p
			}
		}
		rec := learning.OutcomeRecord{
			ProjectID:      V2BenchmarkProjectID,
			Intent:         task.Intent,
			Workflow:       workflow,
			FinishReason:   "stop",
			Success:        success,
			Duration:       0.01,
			Iterations:     1,
			PreferenceUsed: false,
			Timestamp:      now,
		}
		if err := store.Append(rec); err != nil {
			return nil, fmt.Errorf("append training outcome %q: %w", id, err)
		}
	}

	// Advisor pass: consult preferences after the training batch.
	pref, err := advisor.Advise(V2BenchmarkProjectID)
	if err != nil {
		return nil, fmt.Errorf("advise: %w", err)
	}

	// Held-out pass: count how many held-out tasks the advisor would have
	// routed to their ExpectedWorkflow via its learned intent preference.
	// Outcomes are NOT appended — held-out is measurement only.
	hits := 0
	for _, id := range corpus.HeldOutSet {
		task, ok := tasksByID[id]
		if !ok {
			return nil, fmt.Errorf("held-out task %q missing from corpus", id)
		}
		recommended := pref.PreferredWorkflow(task.Intent)
		if recommended != "" && recommended == task.ExpectedWorkflow {
			hits++
		}
	}
	hitRate := 0.0
	if n := len(corpus.HeldOutSet); n > 0 {
		hitRate = float64(hits) / float64(n)
	}
	outcomes, _ := store.ForProject(V2BenchmarkProjectID, 1_000_000)

	return &V2RunResult{
		RunIndex:       runIndex,
		Timestamp:      now.UTC().Format(time.RFC3339),
		HeldOutHitRate: hitRate,
		OutcomesCount:  len(outcomes),
	}, nil
}

func meanFirstN(values []float64, n int) float64 {
	if len(values) == 0 {
		return 0
	}
	if n > len(values) {
		n = len(values)
	}
	var sum float64
	for _, v := range values[:n] {
		sum += v
	}
	return sum / float64(n)
}

func meanLastN(values []float64, n int) float64 {
	if len(values) == 0 {
		return 0
	}
	if n > len(values) {
		n = len(values)
	}
	var sum float64
	for _, v := range values[len(values)-n:] {
		sum += v
	}
	return sum / float64(n)
}

// v2SpearmanPass is the single-threshold gate for the primary metric.
// Supporting narrative comes from the first3/last3 delta in the caller.
const v2SpearmanPass = 0.5

// v2StrongDelta is the minimum first3→last3 lift (in proportion units)
// required to upgrade from PASS to STRONG_PASS.
const v2StrongDelta = 0.10

func decideVerdict(coeff float64, isConstant bool, first3, last3 float64) string {
	if isConstant {
		return V2VerdictFail
	}
	if coeff < v2SpearmanPass {
		return V2VerdictFail
	}
	if (last3 - first3) >= v2StrongDelta {
		return V2VerdictStrongPass
	}
	return V2VerdictPass
}
