package skill

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTrackerRecordUsage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tracker := NewTracker(dir)

	err := tracker.RecordUsage(UsageRecord{
		SkillName: "pr-review",
		SessionID: "sess-1",
		Timestamp: time.Now().UTC(),
		Success:   true,
	})
	if err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}

	stats, err := tracker.UsageStats()
	if err != nil {
		t.Fatalf("UsageStats() error = %v", err)
	}
	if got := stats["pr-review"]; got != 1 {
		t.Fatalf("UsageStats()[%q] = %d, want 1", "pr-review", got)
	}

	if got := tracker.usagePath; got != filepath.Join(dir, "skill-usage.jsonl") {
		t.Fatalf("usagePath = %q, want %q", got, filepath.Join(dir, "skill-usage.jsonl"))
	}
}

func TestTrackerRecordPattern(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tracker := NewTracker(dir)
	now := time.Now().UTC().Round(0)

	err := tracker.RecordPattern(PatternRecord{
		ID:           "pat-1",
		Description:  "test then commit pattern",
		SessionIDs:   []string{"sess-1", "sess-2"},
		ToolSequence: []string{"bash", "read_file", "write_file"},
		FirstSeen:    now,
		LastSeen:     now,
	})
	if err != nil {
		t.Fatalf("RecordPattern() error = %v", err)
	}

	patterns, err := tracker.LoadPatterns()
	if err != nil {
		t.Fatalf("LoadPatterns() error = %v", err)
	}
	if len(patterns) != 1 {
		t.Fatalf("len(LoadPatterns()) = %d, want 1", len(patterns))
	}
	if patterns[0].ID != "pat-1" {
		t.Fatalf("LoadPatterns()[0].ID = %q, want %q", patterns[0].ID, "pat-1")
	}
	if got := tracker.patternPath; got != filepath.Join(dir, "skill-patterns.jsonl") {
		t.Fatalf("patternPath = %q, want %q", got, filepath.Join(dir, "skill-patterns.jsonl"))
	}
}

func TestTrackerEmptyFiles(t *testing.T) {
	t.Parallel()

	tracker := NewTracker(t.TempDir())

	stats, err := tracker.UsageStats()
	if err != nil {
		t.Fatalf("UsageStats() error = %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("len(UsageStats()) = %d, want 0", len(stats))
	}

	patterns, err := tracker.LoadPatterns()
	if err != nil {
		t.Fatalf("LoadPatterns() error = %v", err)
	}
	if len(patterns) != 0 {
		t.Fatalf("len(LoadPatterns()) = %d, want 0", len(patterns))
	}
}
