package eval

import (
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/routing"
)

// fakeRouter is a test double for RealRouter. DecideWorkflow looks up the
// intent in decisions; falls back to fallback if unset. Tests use this to
// reproduce the Router chicken-and-egg scenario motivating Phase 7.4 D1:
// set decisions["bugfix"] = "single" to mimic production Router's default
// for an unknown intent.
type fakeRouter struct {
	decisions map[string]string
	fallback  string
}

func (f *fakeRouter) DecideWorkflow(intent string, _ *routing.WorkflowPreference) string {
	if wf, ok := f.decisions[intent]; ok {
		return wf
	}
	return f.fallback
}

// neverRecommendFactory returns an advisor configured with an unreachable
// minSamples threshold. For a 12-task training set, no intent ever crosses
// minSamples=10000, so the advisor consistently returns nil and hit rate
// stays at 0.0 across every run.
func neverRecommendFactory() V2AdvisorFactory {
	return func(store *learning.OutcomeStore) *learning.RoutingAdvisor {
		return learning.NewRoutingAdvisorWithConfig(store, 200, 10_000)
	}
}

// gradualLearningFactory raises minSamples above the per-intent per-run
// yield so the advisor cannot recommend until several runs of training
// have accumulated. This shapes a gradual learning curve suitable for
// testing the Spearman trend gate. With validV2Corpus (12 training tasks
// / 3 intents = 4 per intent per run) and minSamples=8, recommendations
// emerge starting in run 2.
func gradualLearningFactory() V2AdvisorFactory {
	return func(store *learning.OutcomeStore) *learning.RoutingAdvisor {
		return learning.NewRoutingAdvisorWithConfig(store, 200, 8)
	}
}

// TestRunV2_GradualLearningProducesPass runs the harness with a factory
// that delays advisor recommendations until enough training data has
// accumulated. The resulting hit-rate curve rises from 0 in run 1 to 1.0
// once the minSamples threshold is crossed, producing a positive Spearman
// correlation and a PASS/STRONG_PASS verdict.
func TestRunV2_GradualLearningProducesPass(t *testing.T) {
	fixedClock := func() time.Time {
		return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	}
	series, err := RunV2(V2RunOptions{
		Corpus:         validV2Corpus(),
		OutputDir:      t.TempDir(),
		RunCount:       V2DefaultRunCount,
		AdvisorFactory: gradualLearningFactory(),
		Clock:          fixedClock,
	})
	if err != nil {
		t.Fatalf("RunV2 = %v, want success", err)
	}
	if len(series.Runs) != V2DefaultRunCount {
		t.Fatalf("len(Runs) = %d, want %d", len(series.Runs), V2DefaultRunCount)
	}
	if series.IsConstant {
		t.Fatalf("IsConstant = true with gradual factory; harness should produce a learning curve (runs=%+v)", series.Runs)
	}
	if series.SpearmanCoeff < v2SpearmanPass {
		t.Errorf("spearman = %v, want >= %v for a monotonic learning curve", series.SpearmanCoeff, v2SpearmanPass)
	}
	if series.Verdict != V2VerdictPass && series.Verdict != V2VerdictStrongPass {
		t.Errorf("verdict = %q, want PASS or STRONG_PASS (spearman=%v, first3=%v, last3=%v)",
			series.Verdict, series.SpearmanCoeff, series.First3Avg, series.Last3Avg)
	}
	// Every run must have a non-empty timestamp.
	for i, r := range series.Runs {
		if r.Timestamp == "" {
			t.Errorf("run[%d] has empty timestamp", i)
		}
	}
}

// TestRunV2_ConstantHitRateProducesFail injects an advisor factory that
// always returns a preference matching NO task (wrong workflow), so hit
// rate stays at 0.0 across all runs. The constant-input branch must
// convert this into FAIL, not a spurious PASS.
func TestRunV2_ConstantHitRateProducesFail(t *testing.T) {
	// Use a very high minSamples so the advisor never returns a preference
	// — all runs produce hit_rate=0.0. SpearmanRank flags this as
	// isConstant=true and decideVerdict maps to FAIL.
	series, err := RunV2(V2RunOptions{
		Corpus:         validV2Corpus(),
		OutputDir:      t.TempDir(),
		RunCount:       V2DefaultRunCount,
		AdvisorFactory: neverRecommendFactory(),
		Clock:          func() time.Time { return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("RunV2: %v", err)
	}
	if !series.IsConstant {
		t.Errorf("IsConstant = false, want true (all runs at 0.0)")
	}
	if series.Verdict != V2VerdictFail {
		t.Errorf("verdict = %q, want FAIL", series.Verdict)
	}
}

// TestRunV2_LegacyBehaviorPreservedWhenRouterNil is the Phase 7.4
// regression guard: when V2RunOptions.Router is nil, outcomes must carry
// the task's ExpectedWorkflow — the Phase 7.3 stub invariant. A broken
// default branch would silently change the training signal shape and
// invalidate every Phase 7.3 verdict.
func TestRunV2_LegacyBehaviorPreservedWhenRouterNil(t *testing.T) {
	outputDir := t.TempDir()
	corpus := validV2Corpus()
	_, err := RunV2(V2RunOptions{
		Corpus:         corpus,
		OutputDir:      outputDir,
		RunCount:       1,
		AdvisorFactory: gradualLearningFactory(),
		Clock:          func() time.Time { return time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("RunV2: %v", err)
	}

	scratch := learning.NewOutcomeStore(filepath.Join(outputDir, "scratch-outcomes.jsonl"))
	records, err := scratch.ForProject(V2BenchmarkProjectID, 1_000)
	if err != nil {
		t.Fatalf("ForProject: %v", err)
	}
	if len(records) != len(corpus.TrainingSet) {
		t.Fatalf("outcomes count = %d, want %d", len(records), len(corpus.TrainingSet))
	}
	byID := make(map[string]Task, len(corpus.Tasks))
	for _, tk := range corpus.Tasks {
		byID[tk.ID] = tk
	}
	for i, rec := range records {
		id := corpus.TrainingSet[i]
		expected := byID[id].ExpectedWorkflow
		if rec.Workflow != expected {
			t.Errorf("outcomes[%d].Workflow = %q, want %q (ExpectedWorkflow)", i, rec.Workflow, expected)
		}
	}
}

// TestRunV2_UsesRouterWhenConfigured verifies that V2RunOptions.Router,
// when set, actually shapes the recorded outcomes. This is the positive
// side of the Phase 7.4a Milestone-1 cut: the router's pick — not the
// task's ExpectedWorkflow — flows into the scratch store. The scenario
// mirrors the Phase 7.4 chicken-and-egg problem: fakeRouter is stuck on
// "single" for every intent, so a bugfix training task (ExpectedWorkflow
// = "ralph") must end up recorded as "single", not "ralph".
func TestRunV2_UsesRouterWhenConfigured(t *testing.T) {
	outputDir := t.TempDir()
	corpus := validV2Corpus()
	fake := &fakeRouter{
		decisions: map[string]string{
			"question":     "single",
			"complex_task": "team",
			"bugfix":       "single",
		},
		fallback: "single",
	}
	_, err := RunV2(V2RunOptions{
		Corpus:         corpus,
		OutputDir:      outputDir,
		RunCount:       1,
		AdvisorFactory: gradualLearningFactory(),
		Router:         fake,
		Clock:          func() time.Time { return time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("RunV2: %v", err)
	}

	scratch := learning.NewOutcomeStore(filepath.Join(outputDir, "scratch-outcomes.jsonl"))
	records, err := scratch.ForProject(V2BenchmarkProjectID, 1_000)
	if err != nil {
		t.Fatalf("ForProject: %v", err)
	}
	if len(records) != len(corpus.TrainingSet) {
		t.Fatalf("outcomes count = %d, want %d", len(records), len(corpus.TrainingSet))
	}
	for i, rec := range records {
		want := fake.decisions[rec.Intent]
		if rec.Workflow != want {
			t.Errorf("outcomes[%d] intent=%q Workflow = %q, want %q (fake router decision)",
				i, rec.Intent, rec.Workflow, want)
		}
	}
	// The bugfix-specific assertion is the real test: outcomes for bugfix
	// tasks must NOT carry Workflow="ralph" (the ExpectedWorkflow). If
	// this fails, the router branch never fired and runV2SingleRun
	// silently fell through to the legacy stub path.
	foundBugfix := false
	for _, rec := range records {
		if rec.Intent == "bugfix" {
			foundBugfix = true
			if rec.Workflow == "ralph" {
				t.Errorf("bugfix outcome recorded as Workflow=ralph despite router forcing single; router branch did not fire")
			}
		}
	}
	if !foundBugfix {
		t.Fatalf("test corpus lacks any bugfix training task; fixture assumption broken")
	}
}

// TestRunV2_DeterministicWithSameRand proves that epsilon-greedy +
// Bernoulli draws consume the injected *rand.Rand in a stable order, so
// two RunV2 invocations with the same seed (and otherwise identical
// options) produce byte-identical scratch outcomes. Determinism is a
// hard prerequisite for CI reproducibility and for the "regenerate
// fixtures, diff output" check in the plan's §8.
func TestRunV2_DeterministicWithSameRand(t *testing.T) {
	corpus := validV2Corpus()
	clock := func() time.Time { return time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC) }

	run := func() []learning.OutcomeRecord {
		outputDir := t.TempDir()
		fake := &fakeRouter{
			decisions: map[string]string{
				"question":     "single",
				"complex_task": "team",
				"bugfix":       "single",
			},
			fallback: "single",
		}
		_, err := RunV2(V2RunOptions{
			Corpus:         corpus,
			OutputDir:      outputDir,
			RunCount:       3,
			AdvisorFactory: gradualLearningFactory(),
			Clock:          clock,
			Router:         fake,
			Epsilon:        0.5,
			Rand:           rand.New(rand.NewSource(42)),
		})
		if err != nil {
			t.Fatalf("RunV2: %v", err)
		}
		scratch := learning.NewOutcomeStore(filepath.Join(outputDir, "scratch-outcomes.jsonl"))
		recs, err := scratch.ForProject(V2BenchmarkProjectID, 10_000)
		if err != nil {
			t.Fatalf("ForProject: %v", err)
		}
		return recs
	}

	a := run()
	b := run()
	if len(a) != len(b) {
		t.Fatalf("outcome count diverged: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Intent != b[i].Intent || a[i].Workflow != b[i].Workflow || a[i].Success != b[i].Success {
			t.Errorf("outcomes[%d] diverged: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// TestRunV2_EpsilonGreedyOverridesRouterPick asserts that at epsilon=1.0
// the router's decision is replaced on every training task. The fake
// router forces every intent to "single"; with full exploration, the
// scratch store must contain every workflow in the corpus set, not just
// "single". Without this branch the Phase 7.4 chicken-and-egg problem
// re-emerges: workflows the router never picks would never appear in
// outcomes, and the advisor could never learn to prefer them.
func TestRunV2_EpsilonGreedyOverridesRouterPick(t *testing.T) {
	corpus := validV2Corpus()
	fake := &fakeRouter{fallback: "single"}
	outputDir := t.TempDir()
	_, err := RunV2(V2RunOptions{
		Corpus:         corpus,
		OutputDir:      outputDir,
		RunCount:       3,
		AdvisorFactory: gradualLearningFactory(),
		Clock:          func() time.Time { return time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC) },
		Router:         fake,
		Epsilon:        1.0,
		Rand:           rand.New(rand.NewSource(42)),
	})
	if err != nil {
		t.Fatalf("RunV2: %v", err)
	}
	scratch := learning.NewOutcomeStore(filepath.Join(outputDir, "scratch-outcomes.jsonl"))
	recs, err := scratch.ForProject(V2BenchmarkProjectID, 10_000)
	if err != nil {
		t.Fatalf("ForProject: %v", err)
	}
	counts := make(map[string]int)
	for _, r := range recs {
		counts[r.Workflow]++
	}
	for _, wf := range []string{"single", "team", "ralph"} {
		if counts[wf] == 0 {
			t.Errorf("workflow %q never appeared under epsilon=1.0 (counts=%+v)", wf, counts)
		}
	}
}

// TestRunV2_BernoulliSuccessModelShapesOutcomes validates the 0.9/0.3
// success-rate contract on a large sample. The fake router is tuned so
// question and complex_task tasks route correctly while bugfix tasks
// route wrongly (→ "single" instead of the Expected "ralph"). With
// advisor recommendations suppressed (neverRecommendFactory), the router
// uses its default table every run, so the correct/wrong partition is
// stable across all outcomes. Over 30 runs × 12 training = 360 samples,
// correct-route successes should sit near 0.9 and wrong-route near 0.3.
// Loose ±0.15 tolerances keep the test robust across PRNG seeds.
func TestRunV2_BernoulliSuccessModelShapesOutcomes(t *testing.T) {
	corpus := validV2Corpus()
	fake := &fakeRouter{
		decisions: map[string]string{
			"question":     "single",
			"complex_task": "team",
			"bugfix":       "single",
		},
		fallback: "single",
	}
	outputDir := t.TempDir()
	_, err := RunV2(V2RunOptions{
		Corpus:         corpus,
		OutputDir:      outputDir,
		RunCount:       30,
		AdvisorFactory: neverRecommendFactory(),
		Clock:          func() time.Time { return time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC) },
		Router:         fake,
		Epsilon:        0,
		Rand:           rand.New(rand.NewSource(42)),
	})
	if err != nil {
		t.Fatalf("RunV2: %v", err)
	}
	scratch := learning.NewOutcomeStore(filepath.Join(outputDir, "scratch-outcomes.jsonl"))
	recs, err := scratch.ForProject(V2BenchmarkProjectID, 100_000)
	if err != nil {
		t.Fatalf("ForProject: %v", err)
	}
	expectedByIntent := map[string]string{
		"question":     "single",
		"complex_task": "team",
		"bugfix":       "ralph",
	}
	var correct, wrong, correctSuccess, wrongSuccess int
	for _, r := range recs {
		if r.Workflow == expectedByIntent[r.Intent] {
			correct++
			if r.Success {
				correctSuccess++
			}
		} else {
			wrong++
			if r.Success {
				wrongSuccess++
			}
		}
	}
	if correct == 0 || wrong == 0 {
		t.Fatalf("partition degenerate: correct=%d wrong=%d", correct, wrong)
	}
	correctRate := float64(correctSuccess) / float64(correct)
	wrongRate := float64(wrongSuccess) / float64(wrong)
	if correctRate < 0.75 || correctRate > 1.0 {
		t.Errorf("correct-route success rate = %.3f (%d/%d), want ≈ %.2f ±0.15",
			correctRate, correctSuccess, correct, synthCorrectSuccessRate)
	}
	if wrongRate < 0.15 || wrongRate > 0.45 {
		t.Errorf("wrong-route success rate = %.3f (%d/%d), want ≈ %.2f ±0.15",
			wrongRate, wrongSuccess, wrong, synthWrongSuccessRate)
	}
}

// preferenceAwareFakeRouter extends fakeRouter so the advisor's learned
// preference takes effect once it has enough samples. When the incoming
// WorkflowPreference carries a recommendation for the intent, that beats
// the static decisions table — mirroring orchestrator.Router.Route which
// also folds in the advisor preference before consulting its own table.
// Without this, the chicken-and-egg never resolves: the router ignores the
// advisor's "bugfix → ralph" signal and keeps returning "single", so
// held-out hit rate stays flat regardless of ε-greedy exploration.
type preferenceAwareFakeRouter struct {
	fakeRouter
}

func (r *preferenceAwareFakeRouter) DecideWorkflow(intent string, pref *routing.WorkflowPreference) string {
	if pw := pref.PreferredWorkflow(intent); pw != "" {
		return pw
	}
	return r.fakeRouter.DecideWorkflow(intent, pref)
}

// TestRunV2_EpsilonGreedyEnablesAdvisorLearning is the Phase 7.4a Milestone 3
// end-to-end integration test. It proves that ε-greedy exploration resolves
// the chicken-and-egg problem: the production Router has no "bugfix" case
// (falls through to "single"), so without exploration the advisor never sees
// a "ralph" outcome and cannot learn to prefer it for bugfix tasks. With
// ε=0.2, exploratory "ralph" outcomes accumulate until the advisor crosses
// minSamples=3, its preference map gains "bugfix → ralph", and the
// preference-aware router starts routing bugfix tasks correctly — lifting
// the held-out hit rate over the course of 10 runs.
func TestRunV2_EpsilonGreedyEnablesAdvisorLearning(t *testing.T) {
	corpus := validV2Corpus()
	// The static table is correct for question and complex_task but wrong for
	// bugfix (returns "single" instead of expected "ralph"). This mirrors the
	// production router's missing "bugfix" intent case.
	fake := &preferenceAwareFakeRouter{
		fakeRouter: fakeRouter{
			decisions: map[string]string{
				"question":     "single",
				"complex_task": "team",
				"bugfix":       "single",
			},
			fallback: "single",
		},
	}
	fixedClock := func() time.Time {
		return time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	}

	series, err := RunV2(V2RunOptions{
		Corpus:         corpus,
		OutputDir:      t.TempDir(),
		RunCount:       V2DefaultRunCount,
		AdvisorFactory: DefaultV2AdvisorFactory,
		Clock:          fixedClock,
		Router:         fake,
		Epsilon:        0.2,
		Rand:           rand.New(rand.NewSource(42)),
	})
	if err != nil {
		t.Fatalf("RunV2 = %v, want success", err)
	}
	if len(series.Runs) != V2DefaultRunCount {
		t.Fatalf("len(Runs) = %d, want %d", len(series.Runs), V2DefaultRunCount)
	}

	// Log the full hit-rate trajectory and Spearman for future tuning.
	hitRates := make([]float64, len(series.Runs))
	for i, r := range series.Runs {
		hitRates[i] = r.HeldOutHitRate
		t.Logf("run[%d] hit_rate=%.3f", i+1, r.HeldOutHitRate)
	}
	t.Logf("spearman=%.4f first_run=%.3f last_run=%.3f verdict=%s",
		series.SpearmanCoeff, hitRates[0], hitRates[len(hitRates)-1], series.Verdict)

	// Loose integration assertions: either the hit rate improved by ≥0.10
	// across the cycle, or Spearman shows a positive trend (≥0.3). Both
	// are sufficient to prove the advisor actually learned something.
	firstRun := hitRates[0]
	lastRun := hitRates[len(hitRates)-1]
	hitRateGain := lastRun - firstRun
	spearmanOK := series.SpearmanCoeff >= 0.3
	hitRateOK := hitRateGain >= 0.10

	if !hitRateOK && !spearmanOK {
		t.Errorf("no learning signal detected: first_run=%.3f last_run=%.3f gain=%.3f spearman=%.4f; "+
			"want either gain≥0.10 OR spearman≥0.3 — advisor may not be crossing minSamples "+
			"or preference never emerging (check routing_advisor.go gap≥0.20 threshold)",
			firstRun, lastRun, hitRateGain, series.SpearmanCoeff)
	}
}

// TestRunV2_RejectsV1Corpus guards the brownfield contract: RunV2 must not
// silently accept a v1 corpus. The spec dedicates v2 to self-improvement
// benchmarks; running v1 through it would produce meaningless outcomes.
func TestRunV2_RejectsV1Corpus(t *testing.T) {
	v1 := &Corpus{
		Version: "v1",
		Tasks: []Task{{
			ID: "X", Title: "t", Track: TrackBugfix, Language: LanguageGo,
			RepoClass: "cli_dev_tool", BenchmarkFamily: "f", Prompt: "p",
			Repo: "https://x", RepoRef: "deadbeef",
			AcceptanceCriteria: []string{"ok"},
		}},
	}
	_, err := RunV2(V2RunOptions{Corpus: v1, OutputDir: t.TempDir()})
	if err == nil {
		t.Fatal("RunV2(v1 corpus) error = nil, want error")
	}
}
