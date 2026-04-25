package scorecard

import (
	"path/filepath"
	"time"
)

// ScorecardFilePath returns the per-day JSONL file path for a scorecard run.
// The filename uses the date portion of day in day's own location (not the
// process-local timezone), so a run at 2026-04-17 08:15+09:00 lands in
// 2026-04-17.jsonl regardless of where the binary is executing. Calling
// Local() here would silently shift the bucket on a UTC host (CI runners,
// most cloud workloads) and split a single day across two files.
func ScorecardFilePath(dataDir string, day time.Time) string {
	return filepath.Join(dataDir, "scorecard", day.Format("2006-01-02")+".jsonl")
}
