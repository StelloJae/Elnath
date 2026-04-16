package scorecard

import (
	"path/filepath"
	"time"
)

// ScorecardFilePath returns the per-day JSONL file path for a scorecard run.
// The filename uses the local-date portion of day (not UTC) so that a run at
// 2026-04-17 08:15+09:00 lands in 2026-04-17.jsonl.
func ScorecardFilePath(dataDir string, day time.Time) string {
	localDay := day.Local().Format("2006-01-02")
	return filepath.Join(dataDir, "scorecard", localDay+".jsonl")
}
