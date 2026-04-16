package scorecard

import (
	"fmt"
	"os"
	"time"

	"github.com/stello/elnath/internal/learning"
)

// computeLessonExtraction measures whether lessons are being extracted and
// then superseded by synthesis (the compounding cycle).
func computeLessonExtraction(paths SourcesPaths, _ time.Time) AxisReport {
	if _, err := os.Stat(paths.LessonsPath); err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("lessons file missing: %s", paths.LessonsPath),
		}
	}
	store := learning.NewStore(paths.LessonsPath)
	lessons, err := store.List()
	if err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("load lessons: %v", err),
		}
	}

	total := len(lessons)
	active := 0
	superseded := 0
	for _, l := range lessons {
		if l.SupersededBy == "" {
			active++
		} else {
			superseded++
		}
	}
	ratio := 0.0
	if total > 0 {
		ratio = float64(superseded) / float64(total)
	}
	metrics := map[string]any{
		"lessons_total":      total,
		"lessons_active":     active,
		"lessons_superseded": superseded,
		"supersession_ratio": ratio,
	}

	switch {
	case total < 5:
		return AxisReport{Score: ScoreNascent, Metrics: metrics, Reason: fmt.Sprintf("lessons_total=%d < 5", total)}
	case superseded == 0:
		return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: "no lessons superseded - consolidation inactive"}
	}
	return AxisReport{Score: ScoreOK, Metrics: metrics, Reason: fmt.Sprintf("%d lessons, %d superseded (ratio=%.2f)", total, superseded, ratio)}
}
