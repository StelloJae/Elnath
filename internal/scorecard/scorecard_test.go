package scorecard

import (
	"testing"
	"time"
)

func TestScoreStringValues(t *testing.T) {
	tests := []struct {
		score Score
		want  string
	}{
		{ScoreOK, "OK"},
		{ScoreNascent, "NASCENT"},
		{ScoreDegraded, "DEGRADED"},
		{ScoreUnknown, "UNKNOWN"},
	}
	for _, tc := range tests {
		if got := string(tc.score); got != tc.want {
			t.Errorf("Score %v: got %q, want %q", tc.score, got, tc.want)
		}
	}
}

func TestAggregateOverall(t *testing.T) {
	mk := func(s Score) AxisReport { return AxisReport{Score: s} }
	tests := []struct {
		name string
		axes AxesReport
		want Score
	}{
		{"all OK", AxesReport{mk(ScoreOK), mk(ScoreOK), mk(ScoreOK), mk(ScoreOK)}, ScoreOK},
		{"any DEGRADED wins", AxesReport{mk(ScoreOK), mk(ScoreDegraded), mk(ScoreOK), mk(ScoreOK)}, ScoreDegraded},
		{"DEGRADED beats UNKNOWN", AxesReport{mk(ScoreDegraded), mk(ScoreUnknown), mk(ScoreOK), mk(ScoreOK)}, ScoreDegraded},
		{"any UNKNOWN else", AxesReport{mk(ScoreOK), mk(ScoreUnknown), mk(ScoreOK), mk(ScoreOK)}, ScoreUnknown},
		{"mixed OK/NASCENT", AxesReport{mk(ScoreOK), mk(ScoreNascent), mk(ScoreOK), mk(ScoreNascent)}, ScoreNascent},
		{"all NASCENT", AxesReport{mk(ScoreNascent), mk(ScoreNascent), mk(ScoreNascent), mk(ScoreNascent)}, ScoreNascent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregateOverall(tc.axes)
			if got != tc.want {
				t.Errorf("aggregateOverall: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestComputeEndToEnd(t *testing.T) {
	outcomeLines := []string{
		`{"id":"a","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":true,"timestamp":"2026-04-16T20:46:51Z"}`,
		`{"id":"b","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":true,"timestamp":"2026-04-16T21:12:40Z"}`,
	}
	outcomesPath := writeOutcomesFile(t, outcomeLines)

	lessonLines := []string{}
	for i := 0; i < 12; i++ {
		sup := ""
		if i < 10 {
			sup = `,"superseded_by":"synth-x"`
		}
		lessonLines = append(lessonLines, `{"id":"id`+twoDigits(i)+`","topic":"t","content":"c","tags":[]`+sup+`}`)
	}
	paths := buildSynthesisFixture(t, 2, lessonLines, `{"run_count":1,"success_count":1,"last_success_at":"2026-04-17T07:01:28+09:00"}`)
	paths.OutcomesPath = outcomesPath

	now := time.Date(2026, 4, 17, 8, 15, 0, 0, time.UTC)
	r := Compute(paths, now, "0.6.0-test")

	if r.SchemaVersion != SchemaVersion {
		t.Errorf("schema version: got %q", r.SchemaVersion)
	}
	if r.ElnathVersion != "0.6.0-test" {
		t.Errorf("version not propagated")
	}
	if !r.Timestamp.Equal(now) {
		t.Errorf("timestamp: got %v", r.Timestamp)
	}
	if r.Axes.RoutingAdaptation.Score != ScoreNascent {
		t.Errorf("routing: expected NASCENT, got %v", r.Axes.RoutingAdaptation.Score)
	}
	if r.Axes.OutcomeRecording.Score != ScoreNascent {
		t.Errorf("outcome: expected NASCENT, got %v", r.Axes.OutcomeRecording.Score)
	}
	if r.Axes.LessonExtraction.Score != ScoreOK {
		t.Errorf("lesson: expected OK, got %v", r.Axes.LessonExtraction.Score)
	}
	if r.Axes.SynthesisCompounding.Score != ScoreOK {
		t.Errorf("synthesis: expected OK, got %v", r.Axes.SynthesisCompounding.Score)
	}
	if r.Overall != ScoreNascent {
		t.Errorf("overall: expected NASCENT (mixed OK/NASCENT), got %v", r.Overall)
	}
	if r.Sources.OutcomesPath != outcomesPath {
		t.Errorf("sources not recorded")
	}
}
