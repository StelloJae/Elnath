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
