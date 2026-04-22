package scorecard

import (
	"strings"
	"testing"
	"time"
)

func TestRenderMarkdownContainsAllAxes(t *testing.T) {
	r := Report{
		Timestamp:     time.Date(2026, 4, 17, 8, 15, 0, 0, time.UTC),
		SchemaVersion: "1.0",
		ElnathVersion: "0.6.0",
		Overall:       ScoreNascent,
		Axes: AxesReport{
			RoutingAdaptation:    AxisReport{Score: ScoreNascent, Reason: "2 outcomes"},
			OutcomeRecording:     AxisReport{Score: ScoreNascent, Reason: "outcomes_total < 5"},
			LessonExtraction:     AxisReport{Score: ScoreOK, Reason: "12 lessons, 10 superseded"},
			SynthesisCompounding: AxisReport{Score: ScoreOK, Reason: "2 syntheses, 1 successful run"},
			RoutingTrendSpearman: AxisReport{Score: ScoreNascent, Reason: "no eligible cells"},
		},
		Sources: SourcesPaths{
			OutcomesPath: "/tmp/outcomes.jsonl",
			LessonsPath:  "/tmp/lessons.jsonl",
			SynthesisDir: "/tmp/synthesis",
			StatePath:    "/tmp/state.json",
		},
	}
	md := RenderMarkdown(r)
	for _, want := range []string{
		"Maturity Scorecard",
		"Overall:",
		"NASCENT",
		"routing_adaptation",
		"outcome_recording",
		"lesson_extraction",
		"synthesis_compounding",
		"routing_trend_spearman",
		"no eligible cells",
		"2 outcomes",
		"12 lessons, 10 superseded",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, md)
		}
	}
}
