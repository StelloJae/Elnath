package scorecard

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/stello/elnath/internal/learning"
)

// computeRoutingAdaptation measures whether RoutingAdvisor actually uses
// past outcomes to influence future routing. Reads outcomes.jsonl.
func computeRoutingAdaptation(paths SourcesPaths, _ time.Time) AxisReport {
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

	total := len(outcomes)
	prefUsed := 0
	successes := 0
	for _, o := range outcomes {
		if o.PreferenceUsed {
			prefUsed++
		}
		if o.Success {
			successes++
		}
	}
	var successRate float64
	if total > 0 {
		successRate = float64(successes) / float64(total)
	}
	var trend any
	if total >= 10 {
		sort.Slice(outcomes, func(i, j int) bool {
			return outcomes[i].Timestamp.Before(outcomes[j].Timestamp)
		})
		mid := total / 2
		first := outcomes[:mid]
		second := outcomes[mid:]
		trend = rateOf(second) - rateOf(first)
	}
	metrics := map[string]any{
		"outcomes_total":        total,
		"preference_used_count": prefUsed,
		"preference_used_pct":   pct(prefUsed, total),
		"success_rate":          successRate,
		"trend":                 trend,
	}

	switch {
	case total < 10:
		return AxisReport{Score: ScoreNascent, Metrics: metrics, Reason: fmt.Sprintf("%d outcomes; need >=10 for trend", total)}
	case prefUsed == 0:
		return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: "preference_used never true - advisor not consulted"}
	default:
		if t, ok := trend.(float64); ok && t < -0.10 {
			return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: fmt.Sprintf("trend %.2f below -0.10", t)}
		}
	}
	return AxisReport{Score: ScoreOK, Metrics: metrics, Reason: fmt.Sprintf("%d outcomes, %d with preference_used", total, prefUsed)}
}

func rateOf(records []learning.OutcomeRecord) float64 {
	if len(records) == 0 {
		return 0
	}
	succ := 0
	for _, r := range records {
		if r.Success {
			succ++
		}
	}
	return float64(succ) / float64(len(records))
}

func pct(num, denom int) float64 {
	if denom == 0 {
		return 0
	}
	return float64(num) / float64(denom)
}
