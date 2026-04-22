package scorecard

import (
	"fmt"
	"sort"
	"time"

	"github.com/stello/elnath/internal/eval"
	"github.com/stello/elnath/internal/learning"
)

// TrendConfig tunes the rolling-Spearman routing-trend axis introduced in
// Phase 7.4c. Window bucketing, the eligibility gate, and (in Phase 2) the
// decay-weighted rate all read their parameters from this struct.
type TrendConfig struct {
	Window               int
	MinCellSamples       int
	MinDistinctWorkflows int
	HalfLifeDays         float64
}

// DefaultTrendConfig returns the partner-approved v31 defaults: W=5,
// minCellSamples=15, diversity≥2, half-life 7 days.
func DefaultTrendConfig() TrendConfig {
	return TrendConfig{
		Window:               5,
		MinCellSamples:       15,
		MinDistinctWorkflows: 2,
		HalfLifeDays:         7.0,
	}
}

// CellKey identifies a single (intent, workflow) routing cell.
type CellKey struct {
	Intent   string
	Workflow string
}

// CellVerdict classifies trend health for one cell.
type CellVerdict string

const (
	CellVerdictOK               CellVerdict = "OK"
	CellVerdictDegraded         CellVerdict = "DEGRADED"
	CellVerdictFail             CellVerdict = "FAIL"
	CellVerdictInsufficientData CellVerdict = "INSUFFICIENT_DATA"
)

// CellResult is the per-cell output surfaced by the axis.
type CellResult struct {
	Key            CellKey
	N              int
	Verdict        CellVerdict
	SpearmanCoeff  float64
	WindowHitRates []float64
	Reason         string
}

// TrendAxisResult aggregates cell-level verdicts for the whole stream.
type TrendAxisResult struct {
	Cells                []CellResult
	OverallVerdict       CellVerdict
	TotalRecords         int
	SkippedZeroTimestamp int
	EligibleCells        int
	InsufficientCells    int
}

// EvaluateRoutingTrend is the Phase 1 entrypoint: a pure function over a
// slice of outcome records. Records with zero Timestamp are excluded and
// counted in SkippedZeroTimestamp (Risk §2 R2). The `now` parameter is
// retained for Phase 2's decay-weighted rate and is presently unused.
func EvaluateRoutingTrend(records []learning.OutcomeRecord, cfg TrendConfig, now time.Time) TrendAxisResult {
	_ = now

	usable := make([]learning.OutcomeRecord, 0, len(records))
	skipped := 0
	for _, r := range records {
		if r.Timestamp.IsZero() {
			skipped++
			continue
		}
		usable = append(usable, r)
	}

	byCell := map[CellKey][]learning.OutcomeRecord{}
	workflowsByIntent := map[string]map[string]struct{}{}
	for _, r := range usable {
		k := CellKey{Intent: r.Intent, Workflow: r.Workflow}
		byCell[k] = append(byCell[k], r)
		if workflowsByIntent[r.Intent] == nil {
			workflowsByIntent[r.Intent] = map[string]struct{}{}
		}
		workflowsByIntent[r.Intent][r.Workflow] = struct{}{}
	}

	keys := make([]CellKey, 0, len(byCell))
	for k := range byCell {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Intent != keys[j].Intent {
			return keys[i].Intent < keys[j].Intent
		}
		return keys[i].Workflow < keys[j].Workflow
	})

	cells := make([]CellResult, 0, len(keys))
	eligible := 0
	insufficient := 0
	for _, k := range keys {
		distinct := len(workflowsByIntent[k.Intent])
		res := evaluateTrendCell(k, byCell[k], distinct, cfg)
		cells = append(cells, res)
		if res.Verdict == CellVerdictInsufficientData {
			insufficient++
		} else {
			eligible++
		}
	}

	return TrendAxisResult{
		Cells:                cells,
		OverallVerdict:       worstEligibleTrendVerdict(cells),
		TotalRecords:         len(records),
		SkippedZeroTimestamp: skipped,
		EligibleCells:        eligible,
		InsufficientCells:    insufficient,
	}
}

func evaluateTrendCell(k CellKey, records []learning.OutcomeRecord, distinctWorkflowsInIntent int, cfg TrendConfig) CellResult {
	n := len(records)
	if distinctWorkflowsInIntent < cfg.MinDistinctWorkflows {
		return CellResult{
			Key:     k,
			N:       n,
			Verdict: CellVerdictInsufficientData,
			Reason:  fmt.Sprintf("intent %q has only %d distinct workflow(s); need >= %d", k.Intent, distinctWorkflowsInIntent, cfg.MinDistinctWorkflows),
		}
	}
	if n < cfg.MinCellSamples {
		return CellResult{
			Key:     k,
			N:       n,
			Verdict: CellVerdictInsufficientData,
			Reason:  fmt.Sprintf("n=%d < minCellSamples=%d", n, cfg.MinCellSamples),
		}
	}

	sorted := append([]learning.OutcomeRecord(nil), records...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})

	rates := bucketTrendHitRates(sorted, cfg.Window)
	if len(rates) < 3 {
		return CellResult{
			Key:            k,
			N:              n,
			Verdict:        CellVerdictInsufficientData,
			WindowHitRates: rates,
			Reason:         fmt.Sprintf("only %d non-empty windows; need >= 3 for Spearman", len(rates)),
		}
	}

	coeff, constant := eval.SpearmanRank(rates)
	if constant {
		return CellResult{
			Key:            k,
			N:              n,
			Verdict:        CellVerdictDegraded,
			SpearmanCoeff:  0.0,
			WindowHitRates: rates,
			Reason:         "flat hit-rate across all windows",
		}
	}

	verdict := CellVerdictDegraded
	reason := fmt.Sprintf("coeff=%.3f inside flat/noisy band (-0.3, 0.5)", coeff)
	switch {
	case coeff >= 0.5:
		verdict = CellVerdictOK
		reason = fmt.Sprintf("improving trend (coeff=%.3f)", coeff)
	case coeff <= -0.3:
		verdict = CellVerdictFail
		reason = fmt.Sprintf("declining trend (coeff=%.3f)", coeff)
	}

	return CellResult{
		Key:            k,
		N:              n,
		Verdict:        verdict,
		SpearmanCoeff:  coeff,
		WindowHitRates: rates,
		Reason:         reason,
	}
}

// bucketTrendHitRates groups time-sorted records into W equal-count windows
// and returns per-window hit rate. When n < W the window count collapses to n
// so every returned bucket is non-empty; callers must check the length for
// Spearman eligibility (>= 3).
func bucketTrendHitRates(sorted []learning.OutcomeRecord, W int) []float64 {
	n := len(sorted)
	if W <= 0 || n == 0 {
		return nil
	}
	if n < W {
		W = n
	}
	rates := make([]float64, 0, W)
	for i := 0; i < W; i++ {
		start := (i * n) / W
		end := ((i + 1) * n) / W
		if start == end {
			continue
		}
		succ := 0
		for _, r := range sorted[start:end] {
			if r.Success {
				succ++
			}
		}
		rates = append(rates, float64(succ)/float64(end-start))
	}
	return rates
}

// worstEligibleTrendVerdict composes the axis-level verdict: FAIL > DEGRADED
// > OK across eligible cells. Returns INSUFFICIENT_DATA only when no cell is
// eligible (e.g., single-workflow intents, freshly seeded streams).
func worstEligibleTrendVerdict(cells []CellResult) CellVerdict {
	rank := map[CellVerdict]int{
		CellVerdictOK:       0,
		CellVerdictDegraded: 1,
		CellVerdictFail:     2,
	}
	worst := CellVerdictOK
	sawEligible := false
	for _, c := range cells {
		if c.Verdict == CellVerdictInsufficientData {
			continue
		}
		sawEligible = true
		if rank[c.Verdict] > rank[worst] {
			worst = c.Verdict
		}
	}
	if !sawEligible {
		return CellVerdictInsufficientData
	}
	return worst
}
