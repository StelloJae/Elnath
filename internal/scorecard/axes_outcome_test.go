package scorecard

import (
	"testing"
	"time"
)

func TestComputeOutcomeRecordingUnknown(t *testing.T) {
	got := computeOutcomeRecording(SourcesPaths{OutcomesPath: "/nope"}, time.Now())
	if got.Score != ScoreUnknown {
		t.Errorf("missing file: got %v", got.Score)
	}
}

func TestComputeOutcomeRecordingNascent(t *testing.T) {
	p := writeOutcomesFile(t, []string{
		`{"id":"a","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":true,"timestamp":"2026-04-16T20:46:51Z"}`,
	})
	got := computeOutcomeRecording(SourcesPaths{OutcomesPath: p}, time.Now())
	if got.Score != ScoreNascent {
		t.Errorf("1 outcome: got %v", got.Score)
	}
}

func TestComputeOutcomeRecordingDegradedNoError(t *testing.T) {
	now := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
	var lines []string
	for i := 0; i < 5; i++ {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		lines = append(lines,
			`{"id":"id`+twoDigits(i)+`","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":true,"timestamp":"`+day+`T10:00:00Z"}`,
		)
	}
	p := writeOutcomesFile(t, lines)
	got := computeOutcomeRecording(SourcesPaths{OutcomesPath: p}, now)
	if got.Score != ScoreDegraded {
		t.Errorf("all success no error: got %v (%s)", got.Score, got.Reason)
	}
}

func TestComputeOutcomeRecordingOK(t *testing.T) {
	now := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
	var lines []string
	for i := 0; i < 5; i++ {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		succ := "true"
		fr := "stop"
		if i == 0 {
			succ = "false"
			fr = "error"
		}
		lines = append(lines,
			`{"id":"id`+twoDigits(i)+`","project_id":"p","intent":"i","workflow":"w","finish_reason":"`+fr+`","success":`+succ+`,"timestamp":"`+day+`T10:00:00Z"}`,
		)
	}
	p := writeOutcomesFile(t, lines)
	got := computeOutcomeRecording(SourcesPaths{OutcomesPath: p}, now)
	if got.Score != ScoreOK {
		t.Errorf("5 records with 1 error, 5 distinct days: got %v (%s)", got.Score, got.Reason)
	}
}
