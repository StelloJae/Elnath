package scorecard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeOutcomesFile(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "outcomes.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write outcomes: %v", err)
	}
	return p
}

func twoDigits(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestComputeRoutingAdaptationUnknown(t *testing.T) {
	paths := SourcesPaths{OutcomesPath: "/nonexistent/nope.jsonl"}
	got := computeRoutingAdaptation(paths, time.Now())
	if got.Score != ScoreUnknown {
		t.Errorf("missing file: got %v, want UNKNOWN", got.Score)
	}
}

func TestComputeRoutingAdaptationNascent(t *testing.T) {
	p := writeOutcomesFile(t, []string{
		`{"id":"a","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":true,"timestamp":"2026-04-16T20:46:51Z"}`,
		`{"id":"b","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":true,"timestamp":"2026-04-16T21:12:40Z"}`,
	})
	got := computeRoutingAdaptation(SourcesPaths{OutcomesPath: p}, time.Now())
	if got.Score != ScoreNascent {
		t.Errorf("2 outcomes: got %v, want NASCENT", got.Score)
	}
	if got.Metrics["outcomes_total"] != 2 {
		t.Errorf("outcomes_total: got %v, want 2", got.Metrics["outcomes_total"])
	}
}

func TestComputeRoutingAdaptationOK(t *testing.T) {
	var lines []string
	for i := 0; i < 10; i++ {
		pref := "false"
		if i >= 5 {
			pref = "true"
		}
		lines = append(lines,
			`{"id":"id`+twoDigits(i)+`","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":true,"preference_used":`+pref+`,"timestamp":"2026-04-16T`+twoDigits(i)+`:00:00Z"}`,
		)
	}
	p := writeOutcomesFile(t, lines)
	got := computeRoutingAdaptation(SourcesPaths{OutcomesPath: p}, time.Now())
	if got.Score != ScoreOK {
		t.Errorf("10 outcomes with PreferenceUsed: got %v (%s), want OK", got.Score, got.Reason)
	}
	if got.Metrics["preference_used_count"] != 5 {
		t.Errorf("preference_used_count: got %v, want 5", got.Metrics["preference_used_count"])
	}
}

func TestComputeRoutingAdaptationDegradedRegression(t *testing.T) {
	var lines []string
	for i := 0; i < 10; i++ {
		succ := "true"
		fr := "stop"
		if i >= 5 {
			succ = "false"
			fr = "error"
		}
		lines = append(lines,
			`{"id":"id`+twoDigits(i)+`","project_id":"p","intent":"i","workflow":"w","finish_reason":"`+fr+`","success":`+succ+`,"preference_used":true,"timestamp":"2026-04-16T`+twoDigits(i)+`:00:00Z"}`,
		)
	}
	p := writeOutcomesFile(t, lines)
	got := computeRoutingAdaptation(SourcesPaths{OutcomesPath: p}, time.Now())
	if got.Score != ScoreDegraded {
		t.Errorf("regressing trend: got %v (%s), want DEGRADED", got.Score, got.Reason)
	}
}
