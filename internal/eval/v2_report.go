package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// V2ReportOptions configure the Markdown report writer. Corpus is used
// to surface training/held-out sizes and the declared intent
// distribution. ScorecardSnapshot is an optional read-only view of the
// live scorecard axes so the report can correlate the benchmark verdict
// with in-situ maturity; the report never writes back to scorecard.
type V2ReportOptions struct {
	Corpus            *Corpus
	OutputPath        string
	ScorecardSnapshot map[string]string
}

// RenderV2Report produces a human-readable Markdown report of the
// benchmark cycle and optionally writes it to OutputPath. Returns the
// rendered string so callers (e.g., tests) can assert on it without
// reading the file back.
func RenderV2Report(series *V2TimeSeries, opts V2ReportOptions) (string, error) {
	if series == nil {
		return "", fmt.Errorf("render v2 report: series is required")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Phase 7.3 Benchmark v2 Report\n\n")
	fmt.Fprintf(&b, "## Verdict: **%s**\n\n", series.Verdict)

	fmt.Fprintf(&b, "- Spearman rank correlation: %.4f", series.SpearmanCoeff)
	if series.IsConstant {
		b.WriteString(" _(constant input — no trend signal)_")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- First 3 runs average hit rate: %.4f\n", series.First3Avg)
	fmt.Fprintf(&b, "- Last 3 runs average hit rate: %.4f\n", series.Last3Avg)
	fmt.Fprintf(&b, "- Improvement delta (last3 - first3): %+.4f\n\n", series.Last3Avg-series.First3Avg)

	b.WriteString("## Hit Rate Sparkline\n\n`")
	b.WriteString(hitRateSparkline(series.Runs))
	b.WriteString("`\n\n")

	b.WriteString("## Run Time Series\n\n")
	b.WriteString("| Run | Hit Rate | Outcomes | Timestamp |\n")
	b.WriteString("|-----|----------|----------|-----------|\n")
	for _, r := range series.Runs {
		fmt.Fprintf(&b, "| %d | %.4f | %d | %s |\n",
			r.RunIndex, r.HeldOutHitRate, r.OutcomesCount, r.Timestamp)
	}
	b.WriteString("\n")

	if opts.Corpus != nil {
		b.WriteString("## Corpus\n\n")
		fmt.Fprintf(&b, "- Version: `%s`\n", opts.Corpus.Version)
		fmt.Fprintf(&b, "- Training set: %d tasks\n", len(opts.Corpus.TrainingSet))
		fmt.Fprintf(&b, "- Held-out set: %d tasks\n", len(opts.Corpus.HeldOutSet))
		if len(opts.Corpus.IntentDistribution) > 0 {
			b.WriteString("- Intent distribution:\n")
			intents := make([]string, 0, len(opts.Corpus.IntentDistribution))
			for intent := range opts.Corpus.IntentDistribution {
				intents = append(intents, intent)
			}
			sort.Strings(intents)
			for _, intent := range intents {
				fmt.Fprintf(&b, "  - `%s`: %.2f%%\n", intent, opts.Corpus.IntentDistribution[intent]*100)
			}
		}
		b.WriteString("\n")
	}

	if len(opts.ScorecardSnapshot) > 0 {
		b.WriteString("## Scorecard Axes (read-only reference)\n\n")
		keys := make([]string, 0, len(opts.ScorecardSnapshot))
		for k := range opts.ScorecardSnapshot {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "- `%s`: %s\n", k, opts.ScorecardSnapshot[k])
		}
		b.WriteString("\n")
	}

	b.WriteString("## Methodology\n\n")
	b.WriteString("- Primary metric: Spearman rank correlation of held-out hit rate across runs. Threshold 0.5 for PASS.\n")
	b.WriteString("- Supporting metric: first-3 vs last-3 average delta. +0.10 required for STRONG_PASS.\n")
	b.WriteString("- Held-out outcomes are NOT written to the scratch store; training outcomes accumulate within one cycle.\n")
	b.WriteString("- Stub execution: each task records its `ExpectedWorkflow` as the successful workflow (Phase 7.3 limitation; real runner in Phase 7.4).\n")

	rendered := b.String()
	if opts.OutputPath != "" {
		if err := os.MkdirAll(filepath.Dir(opts.OutputPath), 0o755); err != nil {
			return rendered, fmt.Errorf("render v2 report: mkdir: %w", err)
		}
		if err := os.WriteFile(opts.OutputPath, []byte(rendered), 0o644); err != nil {
			return rendered, fmt.Errorf("render v2 report: write: %w", err)
		}
	}
	return rendered, nil
}

// hitRateSparkline renders the hit-rate series as a compact text
// sparkline using 8 unicode block levels, so the reader can eyeball the
// trajectory without a chart library.
func hitRateSparkline(runs []V2RunResult) string {
	if len(runs) == 0 {
		return ""
	}
	blocks := []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	var b strings.Builder
	for _, r := range runs {
		// Clamp to [0, 1] then bucket into 0..8.
		v := r.HeldOutHitRate
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		idx := int(v * 8)
		if idx > 8 {
			idx = 8
		}
		b.WriteRune(blocks[idx])
	}
	return b.String()
}
