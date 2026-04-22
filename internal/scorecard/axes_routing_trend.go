package scorecard

import (
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"github.com/stello/elnath/internal/eval"
	"github.com/stello/elnath/internal/learning"
)

// TrendConfig tunes the rolling-Spearman routing-trend axis introduced in
// Phase 7.4c. Window bucketing, the eligibility gate, and the decay-weighted
// companion rate all read their parameters from this struct.
type TrendConfig struct {
	Window               int
	MinCellSamples       int
	MinDistinctWorkflows int
	HalfLifeDays         float64
}

// DefaultTrendConfig returns the partner-approved v31 defaults: W=5,
// minCellSamples=15, diversity>=2, half-life 7 days.
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
	DecayedRate    float64
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

// EvaluateRoutingTrend is the pure Phase 1/2 entrypoint. Records with zero
// Timestamp are excluded and counted in SkippedZeroTimestamp (Plan §2 R2).
// `now` feeds the decay-weighted companion rate.
func EvaluateRoutingTrend(records []learning.OutcomeRecord, cfg TrendConfig, now time.Time) TrendAxisResult {
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
		res := evaluateTrendCell(k, byCell[k], distinct, cfg, now)
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

func evaluateTrendCell(k CellKey, records []learning.OutcomeRecord, distinctWorkflowsInIntent int, cfg TrendConfig, now time.Time) CellResult {
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

	decayed := decayedHitRate(sorted, cfg.HalfLifeDays, now)

	rates := bucketTrendHitRates(sorted, cfg.Window)
	if len(rates) < 3 {
		return CellResult{
			Key:            k,
			N:              n,
			Verdict:        CellVerdictInsufficientData,
			WindowHitRates: rates,
			DecayedRate:    decayed,
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
			DecayedRate:    decayed,
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
		DecayedRate:    decayed,
		Reason:         reason,
	}
}

// bucketTrendHitRates groups time-sorted records into W equal-count windows
// and returns per-window hit rate. Callers must check len(rates) >= 3 before
// feeding Spearman; the caller path already enforces n >= minCellSamples so
// with W=5 every returned bucket has at least (minCellSamples/W) records.
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

// decayedHitRate computes the exponentially-decayed success rate:
//
//	rate = Σ (success_i × w_i) / Σ w_i
//	w_i  = exp(-ln(2) / halfLife × age_days_i)
//
// Records with future timestamps (clock skew) are clamped to age=0 so they
// don't blow up the weight. Empty input or non-positive halfLife returns 0.
func decayedHitRate(records []learning.OutcomeRecord, halfLifeDays float64, now time.Time) float64 {
	if halfLifeDays <= 0 || len(records) == 0 {
		return 0
	}
	lambda := math.Ln2 / halfLifeDays
	var weightedSuccess, weightTotal float64
	for _, r := range records {
		ageDays := now.Sub(r.Timestamp).Hours() / 24.0
		if ageDays < 0 {
			ageDays = 0
		}
		w := math.Exp(-lambda * ageDays)
		if r.Success {
			weightedSuccess += w
		}
		weightTotal += w
	}
	if weightTotal == 0 {
		return 0
	}
	return weightedSuccess / weightTotal
}

// worstEligibleTrendVerdict composes the axis-level verdict: FAIL > DEGRADED
// > OK across eligible cells. Returns INSUFFICIENT_DATA only when no cell is
// eligible.
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

// computeRoutingTrendSpearman is the scorecard adapter: loads records from
// OutcomesPath, evaluates the trend axis, and projects the rich
// TrendAxisResult into the Score/Metrics/Reason triple the rest of the
// scorecard consumes. Both FAIL and DEGRADED cells surface as ScoreDegraded
// — the existing Score enum has no FAIL slot, but the raw verdict stays in
// Metrics["overall_verdict"] (and per-cell entries) so operators keep the
// FAIL vs DEGRADED distinction.
func computeRoutingTrendSpearman(paths SourcesPaths, now time.Time) AxisReport {
	if _, err := os.Stat(paths.OutcomesPath); err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("outcomes file missing: %s", paths.OutcomesPath),
		}
	}
	store := learning.NewOutcomeStore(paths.OutcomesPath)
	outcomes, err := store.Recent(0)
	if err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("load outcomes: %v", err),
		}
	}

	cfg := DefaultTrendConfig()
	res := EvaluateRoutingTrend(outcomes, cfg, now)

	score := ScoreUnknown
	var reason string
	switch res.OverallVerdict {
	case CellVerdictOK:
		score = ScoreOK
		reason = fmt.Sprintf("%d eligible cell(s) improving or stable", res.EligibleCells)
	case CellVerdictDegraded:
		score = ScoreDegraded
		reason = fmt.Sprintf("%d eligible cell(s) include a DEGRADED (flat) trend", res.EligibleCells)
	case CellVerdictFail:
		score = ScoreDegraded
		reason = fmt.Sprintf("%d eligible cell(s) include a FAIL (declining) trend", res.EligibleCells)
	case CellVerdictInsufficientData:
		score = ScoreNascent
		reason = fmt.Sprintf("no eligible cells (total=%d records, insufficient=%d cells)", res.TotalRecords, res.InsufficientCells)
	}

	cellSummaries := make([]map[string]any, 0, len(res.Cells))
	for _, c := range res.Cells {
		cellSummaries = append(cellSummaries, map[string]any{
			"intent":         c.Key.Intent,
			"workflow":       c.Key.Workflow,
			"n":              c.N,
			"verdict":        string(c.Verdict),
			"spearman_coeff": c.SpearmanCoeff,
			"decayed_rate":   c.DecayedRate,
			"reason":         c.Reason,
		})
	}
	metrics := map[string]any{
		"overall_verdict":        string(res.OverallVerdict),
		"total_records":          res.TotalRecords,
		"skipped_zero_timestamp": res.SkippedZeroTimestamp,
		"eligible_cells":         res.EligibleCells,
		"insufficient_cells":     res.InsufficientCells,
		"window":                 cfg.Window,
		"min_cell_samples":       cfg.MinCellSamples,
		"half_life_days":         cfg.HalfLifeDays,
		"cells":                  cellSummaries,
	}
	return AxisReport{Score: score, Metrics: metrics, Reason: reason}
}
