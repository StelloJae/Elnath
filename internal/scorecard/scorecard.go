// Package scorecard computes the Phase 7.2 maturity scorecard by reading
// outcomes, lessons, synthesis pages, and consolidation state without
// modifying them. It emits a structured Report intended for append-only
// per-day JSONL persistence.
package scorecard

import "time"

type Score string

const (
	ScoreOK       Score = "OK"
	ScoreNascent  Score = "NASCENT"
	ScoreDegraded Score = "DEGRADED"
	ScoreUnknown  Score = "UNKNOWN"
)

type AxisReport struct {
	Score   Score          `json:"score"`
	Metrics map[string]any `json:"metrics"`
	Reason  string         `json:"reason"`
}

type AxesReport struct {
	RoutingAdaptation    AxisReport `json:"routing_adaptation"`
	OutcomeRecording     AxisReport `json:"outcome_recording"`
	LessonExtraction     AxisReport `json:"lesson_extraction"`
	SynthesisCompounding AxisReport `json:"synthesis_compounding"`
}

type SourcesPaths struct {
	OutcomesPath string `json:"outcomes_path"`
	LessonsPath  string `json:"lessons_path"`
	SynthesisDir string `json:"synthesis_dir"`
	StatePath    string `json:"state_path"`
}

type Report struct {
	Timestamp     time.Time    `json:"timestamp"`
	SchemaVersion string       `json:"schema_version"`
	ElnathVersion string       `json:"elnath_version"`
	Overall       Score        `json:"overall"`
	Axes          AxesReport   `json:"axes"`
	Sources       SourcesPaths `json:"sources"`
}

const SchemaVersion = "1.0"

// aggregateOverall applies the composition rule:
// any DEGRADED wins; else all OK is OK; else any UNKNOWN is UNKNOWN; else NASCENT.
func aggregateOverall(a AxesReport) Score {
	all := []Score{
		a.RoutingAdaptation.Score,
		a.OutcomeRecording.Score,
		a.LessonExtraction.Score,
		a.SynthesisCompounding.Score,
	}
	for _, s := range all {
		if s == ScoreDegraded {
			return ScoreDegraded
		}
	}
	allOK := true
	anyUnknown := false
	for _, s := range all {
		if s != ScoreOK {
			allOK = false
		}
		if s == ScoreUnknown {
			anyUnknown = true
		}
	}
	switch {
	case allOK:
		return ScoreOK
	case anyUnknown:
		return ScoreUnknown
	default:
		return ScoreNascent
	}
}
