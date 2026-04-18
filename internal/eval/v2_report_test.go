package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleSeries returns a deterministic V2TimeSeries for report tests.
// Values match a gradual-learning curve: first two runs fail, the rest
// succeed — the shape the harness produces with minSamples=8 and
// validV2Corpus.
func sampleSeries() *V2TimeSeries {
	runs := []V2RunResult{
		{RunIndex: 1, Timestamp: "2026-04-18T12:00:00Z", HeldOutHitRate: 0.0, OutcomesCount: 12},
		{RunIndex: 2, Timestamp: "2026-04-18T12:01:00Z", HeldOutHitRate: 1.0, OutcomesCount: 24},
		{RunIndex: 3, Timestamp: "2026-04-18T12:02:00Z", HeldOutHitRate: 1.0, OutcomesCount: 36},
		{RunIndex: 4, Timestamp: "2026-04-18T12:03:00Z", HeldOutHitRate: 1.0, OutcomesCount: 48},
		{RunIndex: 5, Timestamp: "2026-04-18T12:04:00Z", HeldOutHitRate: 1.0, OutcomesCount: 60},
	}
	coeff, isConst := SpearmanRank([]float64{0, 1, 1, 1, 1})
	return &V2TimeSeries{
		Runs:          runs,
		SpearmanCoeff: coeff,
		IsConstant:    isConst,
		First3Avg:     meanFirstN([]float64{0, 1, 1, 1, 1}, 3),
		Last3Avg:      meanLastN([]float64{0, 1, 1, 1, 1}, 3),
		Verdict:       V2VerdictStrongPass,
	}
}

func TestRenderV2Report_IncludesEssentials(t *testing.T) {
	series := sampleSeries()
	out, err := RenderV2Report(series, V2ReportOptions{
		Corpus: validV2Corpus(),
	})
	if err != nil {
		t.Fatalf("RenderV2Report: %v", err)
	}
	checks := []string{
		"Phase 7.3 Benchmark v2 Report",
		"STRONG_PASS",
		"Spearman rank correlation",
		"First 3 runs average hit rate",
		"Last 3 runs average hit rate",
		"Improvement delta",
		"Hit Rate Sparkline",
		"Run Time Series",
		"| 1 | 0.0000 | 12 |",
		"| 5 | 1.0000 | 60 |",
		"Corpus",
		"Training set: 12 tasks",
		"Held-out set: 5 tasks",
		"Intent distribution",
		"question",
		"Methodology",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("report missing substring %q", want)
		}
	}
}

func TestRenderV2Report_ScorecardSnapshotOptional(t *testing.T) {
	series := sampleSeries()
	// With snapshot
	outWith, err := RenderV2Report(series, V2ReportOptions{
		ScorecardSnapshot: map[string]string{
			"routing_adaptation": "OK",
			"outcome_recording":  "DEGRADED",
		},
	})
	if err != nil {
		t.Fatalf("RenderV2Report(with): %v", err)
	}
	if !strings.Contains(outWith, "Scorecard Axes") {
		t.Error("with snapshot: missing Scorecard Axes heading")
	}
	if !strings.Contains(outWith, "routing_adaptation") {
		t.Error("with snapshot: missing routing_adaptation entry")
	}

	// Without snapshot
	outWithout, err := RenderV2Report(series, V2ReportOptions{})
	if err != nil {
		t.Fatalf("RenderV2Report(without): %v", err)
	}
	if strings.Contains(outWithout, "Scorecard Axes") {
		t.Error("without snapshot: report should not have Scorecard Axes section")
	}
}

func TestRenderV2Report_WritesFile(t *testing.T) {
	series := sampleSeries()
	outDir := t.TempDir()
	reportPath := filepath.Join(outDir, "report.md")
	rendered, err := RenderV2Report(series, V2ReportOptions{OutputPath: reportPath})
	if err != nil {
		t.Fatalf("RenderV2Report: %v", err)
	}
	fileContents, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(fileContents) != rendered {
		t.Errorf("file contents != returned string")
	}
}

func TestHitRateSparkline(t *testing.T) {
	runs := []V2RunResult{
		{HeldOutHitRate: 0.0},
		{HeldOutHitRate: 0.5},
		{HeldOutHitRate: 1.0},
	}
	got := hitRateSparkline(runs)
	if got == "" {
		t.Fatal("sparkline is empty")
	}
	// Expect 3 runes rendering the trajectory; exact characters depend on
	// the bucketing scheme, but the string should not be all one rune when
	// the underlying values span the full range.
	runes := []rune(got)
	if len(runes) != 3 {
		t.Fatalf("len(runes) = %d, want 3", len(runes))
	}
	if runes[0] == runes[2] {
		t.Errorf("sparkline first and last rune are identical (%q, %q); trajectory should vary", runes[0], runes[2])
	}
}
