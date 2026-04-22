package scorecard

import (
	"testing"
	"time"

	"github.com/stello/elnath/internal/learning"
)

var trendBaseTime = time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)

func buildTrendCell(intent, workflow string, base time.Time, successes []bool) []learning.OutcomeRecord {
	recs := make([]learning.OutcomeRecord, len(successes))
	for i, s := range successes {
		recs[i] = learning.OutcomeRecord{
			Intent:    intent,
			Workflow:  workflow,
			Success:   s,
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		}
	}
	return recs
}

func sisterTrendRec(intent, workflow string, ts time.Time) learning.OutcomeRecord {
	return learning.OutcomeRecord{
		Intent:    intent,
		Workflow:  workflow,
		Success:   true,
		Timestamp: ts,
	}
}

func findTrendCell(t *testing.T, res TrendAxisResult, intent, workflow string) CellResult {
	t.Helper()
	for _, c := range res.Cells {
		if c.Key.Intent == intent && c.Key.Workflow == workflow {
			return c
		}
	}
	t.Fatalf("cell %s/%s not found in result; cells=%+v", intent, workflow, res.Cells)
	return CellResult{}
}

func TestEvaluateRoutingTrend_ImprovingCellOK(t *testing.T) {
	// 15 records in 5 equal-count buckets produce hit rates
	// (0, 1/3, 2/3, 1, 1) -> Spearman ~0.97 -> OK.
	pattern := []bool{
		false, false, false,
		false, false, true,
		false, true, true,
		true, true, true,
		true, true, true,
	}
	cell := buildTrendCell("code", "single", trendBaseTime, pattern)
	sister := sisterTrendRec("code", "team", trendBaseTime.Add(time.Hour))
	records := append(cell, sister)

	res := EvaluateRoutingTrend(records, DefaultTrendConfig(), trendBaseTime.Add(2*time.Hour))
	got := findTrendCell(t, res, "code", "single")
	if got.Verdict != CellVerdictOK {
		t.Fatalf("improving cell: got %v (coeff=%.3f reason=%q), want OK", got.Verdict, got.SpearmanCoeff, got.Reason)
	}
	if got.SpearmanCoeff < 0.5 {
		t.Fatalf("improving cell Spearman coeff: got %.3f, want >= 0.5", got.SpearmanCoeff)
	}
	if len(got.WindowHitRates) != 5 {
		t.Fatalf("improving cell windows: got %d, want 5", len(got.WindowHitRates))
	}
	if res.SkippedZeroTimestamp != 0 {
		t.Fatalf("skipped zero-timestamp: got %d, want 0", res.SkippedZeroTimestamp)
	}
}

func TestEvaluateRoutingTrend_DecliningCellFAIL(t *testing.T) {
	// Reverse of improving: hit rates 1, 1, 2/3, 1/3, 0 -> Spearman ~-0.97.
	pattern := []bool{
		true, true, true,
		true, true, true,
		false, true, true,
		false, false, true,
		false, false, false,
	}
	cell := buildTrendCell("code", "single", trendBaseTime, pattern)
	sister := sisterTrendRec("code", "team", trendBaseTime.Add(time.Hour))
	records := append(cell, sister)

	res := EvaluateRoutingTrend(records, DefaultTrendConfig(), trendBaseTime.Add(2*time.Hour))
	got := findTrendCell(t, res, "code", "single")
	if got.Verdict != CellVerdictFail {
		t.Fatalf("declining cell: got %v (coeff=%.3f reason=%q), want FAIL", got.Verdict, got.SpearmanCoeff, got.Reason)
	}
	if got.SpearmanCoeff > -0.3 {
		t.Fatalf("declining cell Spearman coeff: got %.3f, want <= -0.3", got.SpearmanCoeff)
	}
	if res.OverallVerdict != CellVerdictFail {
		t.Fatalf("overall with a FAIL cell: got %v, want FAIL", res.OverallVerdict)
	}
}

func TestEvaluateRoutingTrend_FlatCellDEGRADED(t *testing.T) {
	// All 15 records succeed -> every bucket rate 1.0 -> allEqual -> DEGRADED.
	pattern := make([]bool, 15)
	for i := range pattern {
		pattern[i] = true
	}
	cell := buildTrendCell("code", "single", trendBaseTime, pattern)
	sister := sisterTrendRec("code", "team", trendBaseTime.Add(time.Hour))
	records := append(cell, sister)

	res := EvaluateRoutingTrend(records, DefaultTrendConfig(), trendBaseTime.Add(2*time.Hour))
	got := findTrendCell(t, res, "code", "single")
	if got.Verdict != CellVerdictDegraded {
		t.Fatalf("flat cell: got %v (coeff=%.3f reason=%q), want DEGRADED", got.Verdict, got.SpearmanCoeff, got.Reason)
	}
}

func TestEvaluateRoutingTrend_LowNCellInsufficientData(t *testing.T) {
	// 10 records (< 15 MinCellSamples) with diversity gate satisfied via sister.
	pattern := make([]bool, 10)
	for i := range pattern {
		pattern[i] = true
	}
	cell := buildTrendCell("code", "single", trendBaseTime, pattern)
	sister := sisterTrendRec("code", "team", trendBaseTime.Add(time.Hour))
	records := append(cell, sister)

	res := EvaluateRoutingTrend(records, DefaultTrendConfig(), trendBaseTime.Add(2*time.Hour))
	got := findTrendCell(t, res, "code", "single")
	if got.Verdict != CellVerdictInsufficientData {
		t.Fatalf("low-n cell: got %v reason=%q, want INSUFFICIENT_DATA", got.Verdict, got.Reason)
	}
	if res.OverallVerdict != CellVerdictInsufficientData {
		t.Fatalf("no eligible cells: overall got %v, want INSUFFICIENT_DATA", res.OverallVerdict)
	}
}

func TestEvaluateRoutingTrend_SingleWorkflowIntentInsufficientData(t *testing.T) {
	// 30 records under the sole workflow of its intent: plenty of samples, but
	// the diversity gate flips the cell to INSUFFICIENT_DATA regardless of n.
	pattern := make([]bool, 30)
	for i := range pattern {
		pattern[i] = i%2 == 0
	}
	cell := buildTrendCell("solo", "only", trendBaseTime, pattern)

	res := EvaluateRoutingTrend(cell, DefaultTrendConfig(), trendBaseTime.Add(2*time.Hour))
	got := findTrendCell(t, res, "solo", "only")
	if got.Verdict != CellVerdictInsufficientData {
		t.Fatalf("single-workflow cell: got %v reason=%q, want INSUFFICIENT_DATA", got.Verdict, got.Reason)
	}
	if res.OverallVerdict != CellVerdictInsufficientData {
		t.Fatalf("only single-workflow intent: overall got %v, want INSUFFICIENT_DATA", res.OverallVerdict)
	}
}

func TestEvaluateRoutingTrend_ZeroTimestampSkipped(t *testing.T) {
	// Records with zero Timestamp must be excluded from bucketing and counted.
	pattern := []bool{
		false, false, false,
		false, false, true,
		false, true, true,
		true, true, true,
		true, true, true,
	}
	cell := buildTrendCell("code", "single", trendBaseTime, pattern)
	sister := sisterTrendRec("code", "team", trendBaseTime.Add(time.Hour))
	noise := learning.OutcomeRecord{Intent: "code", Workflow: "single", Success: false} // zero timestamp
	records := append(cell, sister, noise)

	res := EvaluateRoutingTrend(records, DefaultTrendConfig(), trendBaseTime.Add(2*time.Hour))
	if res.SkippedZeroTimestamp != 1 {
		t.Fatalf("skipped zero-timestamp: got %d, want 1", res.SkippedZeroTimestamp)
	}
	got := findTrendCell(t, res, "code", "single")
	if got.Verdict != CellVerdictOK {
		t.Fatalf("zero-timestamp noise must not flip verdict: got %v, want OK", got.Verdict)
	}
}

func TestDecayedHitRate_FormulaAndEdgeCases(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	halfLife := 7.0

	if got := decayedHitRate(nil, halfLife, now); got != 0 {
		t.Fatalf("empty records: got %v, want 0", got)
	}

	recs := []learning.OutcomeRecord{{Success: true, Timestamp: now}}
	if got := decayedHitRate(recs, 0, now); got != 0 {
		t.Fatalf("halfLife=0 guard: got %v, want 0", got)
	}

	// fresh weight = 1, old (age=14d, halfLife=7d) weight = 0.25.
	// weightedSuccess = 1*1 + 0.25*0 = 1; weightTotal = 1.25; result = 0.8.
	fresh := learning.OutcomeRecord{Success: true, Timestamp: now}
	old := learning.OutcomeRecord{Success: false, Timestamp: now.Add(-14 * 24 * time.Hour)}
	got := decayedHitRate([]learning.OutcomeRecord{fresh, old}, halfLife, now)
	if got < 0.79 || got > 0.81 {
		t.Fatalf("decay weighting: got %.4f, want ~0.8", got)
	}

	// Clock-skew clamp: future record treated as age=0, weight=1, perfect rate.
	future := learning.OutcomeRecord{Success: true, Timestamp: now.Add(24 * time.Hour)}
	if got := decayedHitRate([]learning.OutcomeRecord{future}, halfLife, now); got != 1.0 {
		t.Fatalf("future record clamp: got %.4f, want 1.0", got)
	}

	// All failures -> 0 regardless of recency.
	fail := learning.OutcomeRecord{Success: false, Timestamp: now}
	if got := decayedHitRate([]learning.OutcomeRecord{fail, fail}, halfLife, now); got != 0 {
		t.Fatalf("all-fail: got %.4f, want 0", got)
	}
}

func TestComputeRoutingTrendSpearman_MissingFile(t *testing.T) {
	got := computeRoutingTrendSpearman(SourcesPaths{OutcomesPath: "/nonexistent/never.jsonl"}, time.Now())
	if got.Score != ScoreUnknown {
		t.Fatalf("missing outcomes file: got %v, want UNKNOWN", got.Score)
	}
}

func TestComputeRoutingTrendSpearman_CorpusSnapshot(t *testing.T) {
	p := "testdata/trend_corpus.jsonl"
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	got := computeRoutingTrendSpearman(SourcesPaths{OutcomesPath: p}, now)

	if got.Score != ScoreDegraded {
		t.Fatalf("corpus Score: got %v reason=%q, want DEGRADED", got.Score, got.Reason)
	}
	overall, _ := got.Metrics["overall_verdict"].(string)
	if overall != string(CellVerdictFail) {
		t.Fatalf("overall_verdict: got %q, want FAIL", overall)
	}
	if total, _ := got.Metrics["total_records"].(int); total != 50 {
		t.Fatalf("total_records: got %d, want 50", total)
	}

	cells, _ := got.Metrics["cells"].([]map[string]any)
	if len(cells) != 3 {
		t.Fatalf("cells count: got %d, want 3", len(cells))
	}
	verdicts := map[string]string{}
	decays := map[string]float64{}
	ns := map[string]int{}
	for _, c := range cells {
		key := c["intent"].(string) + "/" + c["workflow"].(string)
		verdicts[key] = c["verdict"].(string)
		decays[key] = c["decayed_rate"].(float64)
		ns[key] = c["n"].(int)
	}
	wantVerdicts := map[string]string{
		"coding/single": string(CellVerdictOK),
		"coding/team":   string(CellVerdictFail),
		"solo/only":     string(CellVerdictInsufficientData),
	}
	for k, v := range wantVerdicts {
		if verdicts[k] != v {
			t.Fatalf("cell %s verdict: got %q, want %q", k, verdicts[k], v)
		}
	}
	wantN := map[string]int{
		"coding/single": 15,
		"coding/team":   15,
		"solo/only":     20,
	}
	for k, v := range wantN {
		if ns[k] != v {
			t.Fatalf("cell %s n: got %d, want %d", k, ns[k], v)
		}
	}
	for _, k := range []string{"coding/single", "coding/team"} {
		d := decays[k]
		if d < 0 || d > 1 {
			t.Fatalf("cell %s decayed_rate out of range: %.4f", k, d)
		}
	}
}

func TestComputeRoutingTrendSpearman_DeterministicTwice(t *testing.T) {
	p := "testdata/trend_corpus.jsonl"
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	a := computeRoutingTrendSpearman(SourcesPaths{OutcomesPath: p}, now)
	b := computeRoutingTrendSpearman(SourcesPaths{OutcomesPath: p}, now)

	if a.Score != b.Score {
		t.Fatalf("determinism: Score diff %v vs %v", a.Score, b.Score)
	}
	if a.Reason != b.Reason {
		t.Fatalf("determinism: Reason diff %q vs %q", a.Reason, b.Reason)
	}
	ov1, _ := a.Metrics["overall_verdict"].(string)
	ov2, _ := b.Metrics["overall_verdict"].(string)
	if ov1 != ov2 {
		t.Fatalf("determinism: overall_verdict diff %q vs %q", ov1, ov2)
	}
	t1, _ := a.Metrics["total_records"].(int)
	t2, _ := b.Metrics["total_records"].(int)
	if t1 != t2 {
		t.Fatalf("determinism: total_records diff %d vs %d", t1, t2)
	}
}
