package scorecard

import (
	"strings"
	"testing"
	"time"
)

func TestScorecardFilePath(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Seoul")
	day := time.Date(2026, 4, 17, 8, 15, 0, 0, loc)
	got := ScorecardFilePath("/data", day)
	want := "/data/scorecard/2026-04-17.jsonl"
	if got != want {
		t.Errorf("ScorecardFilePath: got %q, want %q", got, want)
	}
}

func TestScorecardFilePathShape(t *testing.T) {
	utcDay := time.Date(2026, 4, 17, 23, 0, 0, 0, time.UTC)
	got := ScorecardFilePath("/data", utcDay)
	if !strings.HasPrefix(got, "/data/scorecard/") {
		t.Errorf("unexpected prefix: %q", got)
	}
	if !strings.HasSuffix(got, ".jsonl") {
		t.Errorf("unexpected suffix: %q", got)
	}
	base := strings.TrimPrefix(got, "/data/scorecard/")
	base = strings.TrimSuffix(base, ".jsonl")
	if _, err := time.Parse("2006-01-02", base); err != nil {
		t.Errorf("base %q is not a valid YYYY-MM-DD: %v", base, err)
	}
}
