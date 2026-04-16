package scorecard

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stello/elnath/internal/learning"
)

// computeSynthesisCompounding measures whether consolidation actually
// produces compounding synthesis pages.
func computeSynthesisCompounding(paths SourcesPaths, _ time.Time) AxisReport {
	if _, err := os.Stat(paths.StatePath); err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("state file missing: %s", paths.StatePath),
		}
	}
	state, err := learning.LoadConsolidationState(paths.StatePath)
	if err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("load state: %v", err),
		}
	}

	synthCount := 0
	if paths.SynthesisDir != "" {
		pattern := filepath.Join(paths.SynthesisDir, "*", "*.md")
		matches, _ := filepath.Glob(pattern)
		synthCount = len(matches)
	}

	ratio := 0.0
	if paths.LessonsPath != "" {
		if _, err := os.Stat(paths.LessonsPath); err == nil {
			store := learning.NewStore(paths.LessonsPath)
			lessons, _ := store.List()
			total := len(lessons)
			superseded := 0
			for _, l := range lessons {
				if l.SupersededBy != "" {
					superseded++
				}
			}
			if total > 0 {
				ratio = float64(superseded) / float64(total)
			}
		}
	}

	metrics := map[string]any{
		"synthesis_count":    synthCount,
		"run_count":          state.RunCount,
		"success_count":      state.SuccessCount,
		"last_success_at":    state.LastSuccessAt,
		"supersession_ratio": ratio,
	}

	switch {
	case state.RunCount == 0:
		return AxisReport{Score: ScoreNascent, Metrics: metrics, Reason: "no consolidation runs yet"}
	case state.SuccessCount == 0 || synthCount == 0:
		return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: fmt.Sprintf("runs=%d but success=%d, synth=%d", state.RunCount, state.SuccessCount, synthCount)}
	case ratio == 0:
		return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: "synthesis pages exist but no lessons superseded"}
	}
	return AxisReport{Score: ScoreOK, Metrics: metrics, Reason: fmt.Sprintf("%d syntheses, %d successful run(s), supersession=%.2f", synthCount, state.SuccessCount, ratio)}
}
