package scorecard

import (
	"fmt"
	"os"
	"time"

	"github.com/stello/elnath/internal/learning"
)

// computeOutcomeRecording measures whether outcome recording is happening
// across both success and error paths, and across multiple days recently.
func computeOutcomeRecording(paths SourcesPaths, now time.Time) AxisReport {
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
	success := 0
	errs := 0
	var last time.Time
	days := map[string]struct{}{}
	windowStart := now.Add(-7 * 24 * time.Hour)
	for _, o := range outcomes {
		if o.Success {
			success++
		} else {
			errs++
		}
		if o.Timestamp.After(last) {
			last = o.Timestamp
		}
		if !o.Timestamp.Before(windowStart) && !o.Timestamp.After(now) {
			days[o.Timestamp.Local().Format("2006-01-02")] = struct{}{}
		}
	}
	metrics := map[string]any{
		"outcomes_total":       total,
		"success_count":        success,
		"error_count":          errs,
		"distinct_days_last_7": len(days),
		"last_record_at":       last,
	}

	switch {
	case total < 5:
		return AxisReport{Score: ScoreNascent, Metrics: metrics, Reason: fmt.Sprintf("outcomes_total=%d < 5", total)}
	case errs == 0:
		return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: "no error outcomes - survivorship bias risk"}
	case len(days) < 3:
		return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: fmt.Sprintf("only %d distinct days in last 7", len(days))}
	}
	return AxisReport{Score: ScoreOK, Metrics: metrics, Reason: fmt.Sprintf("%d records across %d days, %d errors captured", total, len(days), errs)}
}
