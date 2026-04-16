package scorecard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func buildSynthesisFixture(t *testing.T, synthCount int, lessons []string, state string) SourcesPaths {
	t.Helper()
	dir := t.TempDir()
	synDir := filepath.Join(dir, "synthesis")
	for i := 0; i < synthCount; i++ {
		sub := filepath.Join(synDir, "cluster")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir synth cluster: %v", err)
		}
		fp := filepath.Join(sub, "page"+twoDigits(i)+".md")
		if err := os.WriteFile(fp, []byte("# synth"), 0o600); err != nil {
			t.Fatalf("write synth: %v", err)
		}
	}
	var lp string
	if lessons != nil {
		lp = writeLessonsFile(t, lessons)
	}
	sp := filepath.Join(dir, "state.json")
	if err := os.WriteFile(sp, []byte(state), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	return SourcesPaths{
		LessonsPath:  lp,
		SynthesisDir: synDir,
		StatePath:    sp,
	}
}

func TestComputeSynthesisUnknown(t *testing.T) {
	got := computeSynthesisCompounding(SourcesPaths{StatePath: "/nope"}, time.Now())
	if got.Score != ScoreUnknown {
		t.Errorf("missing state: got %v", got.Score)
	}
}

func TestComputeSynthesisNascent(t *testing.T) {
	paths := buildSynthesisFixture(t, 0, nil, `{"run_count":0,"success_count":0}`)
	got := computeSynthesisCompounding(paths, time.Now())
	if got.Score != ScoreNascent {
		t.Errorf("run_count=0: got %v", got.Score)
	}
}

func TestComputeSynthesisOK(t *testing.T) {
	lessons := []string{
		`{"id":"1","topic":"t","content":"c","tags":[],"superseded_by":"synth-1"}`,
		`{"id":"2","topic":"t","content":"c","tags":[],"superseded_by":"synth-1"}`,
		`{"id":"3","topic":"t","content":"c","tags":[]}`,
	}
	paths := buildSynthesisFixture(t, 1, lessons, `{"run_count":1,"success_count":1,"last_success_at":"2026-04-17T07:01:28+09:00"}`)
	got := computeSynthesisCompounding(paths, time.Now())
	if got.Score != ScoreOK {
		t.Errorf("1 synth, 1 success, supersession>0: got %v (%s)", got.Score, got.Reason)
	}
}

func TestComputeSynthesisDegradedRunWithoutSuccess(t *testing.T) {
	paths := buildSynthesisFixture(t, 0, nil, `{"run_count":3,"success_count":0}`)
	got := computeSynthesisCompounding(paths, time.Now())
	if got.Score != ScoreDegraded {
		t.Errorf("runs without success: got %v (%s)", got.Score, got.Reason)
	}
}
