package skill

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestTrackerUsageSummariesIncludeOutcomeSignals(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tracker := NewTracker(dir)
	first := time.Date(2026, 5, 17, 1, 2, 3, 0, time.UTC)
	second := first.Add(time.Minute)
	proposalPath := filepath.Join(dir, "skill-improvement-proposals", "20260517T010303Z-pr-review.md")

	records := []UsageRecord{
		{
			SkillName:          "pr-review",
			SessionID:          "sess-1",
			Timestamp:          first,
			Success:            true,
			RequiredTools:      []string{"bash", "read_file", "bash"},
			VerificationResult: SkillVerificationPassed,
			UserOutcome:        "completed",
		},
		{
			SkillName:               "pr-review",
			SessionID:               "sess-2",
			Timestamp:               second,
			Success:                 false,
			RequiredTools:           []string{"apply_patch"},
			VerificationResult:      SkillVerificationFailed,
			UserOutcome:             "failed",
			PromotionCandidate:      true,
			ImprovementProposalPath: proposalPath,
		},
	}
	for _, record := range records {
		if err := tracker.RecordUsage(record); err != nil {
			t.Fatalf("RecordUsage() error = %v", err)
		}
	}

	summaries, err := tracker.UsageSummaries()
	if err != nil {
		t.Fatalf("UsageSummaries() error = %v", err)
	}
	got := summaries["pr-review"]
	if got.Invocations != 2 || got.Successes != 1 || got.Failures != 1 {
		t.Fatalf("summary counts = %+v, want 2 invocations, 1 success, 1 failure", got)
	}
	if !reflect.DeepEqual(got.RequiredTools, []string{"apply_patch", "bash", "read_file"}) {
		t.Fatalf("RequiredTools = %#v", got.RequiredTools)
	}
	if got.VerificationPassed != 1 || got.VerificationFailed != 1 || got.VerificationNotRun != 0 || got.VerificationUnknown != 0 {
		t.Fatalf("verification counts = %+v", got)
	}
	if got.PromotionCandidates != 1 || got.LastUserOutcome != "failed" || got.LastImprovementProposalPath != proposalPath {
		t.Fatalf("outcome summary = %+v", got)
	}
}

func TestTrackerRecordUsageNormalizesUnknownOutcome(t *testing.T) {
	t.Parallel()

	tracker := NewTracker(t.TempDir())
	if err := tracker.RecordUsage(UsageRecord{
		SkillName:          "pr-review",
		SessionID:          "sess-1",
		Success:            true,
		RequiredTools:      []string{"", "bash"},
		VerificationResult: "surprising",
	}); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}

	records, err := readJSONL[UsageRecord](tracker.usagePath)
	if err != nil {
		t.Fatalf("read usage records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("usage records = %+v, want one", records)
	}
	if records[0].VerificationResult != SkillVerificationUnknown || records[0].UserOutcome != "completed" {
		t.Fatalf("normalized usage record = %+v", records[0])
	}
	if !reflect.DeepEqual(records[0].RequiredTools, []string{"bash"}) {
		t.Fatalf("RequiredTools = %#v, want bash", records[0].RequiredTools)
	}
}

func TestTrackerWriteImprovementProposal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tracker := NewTracker(dir)
	createdAt := time.Date(2026, 5, 17, 4, 5, 6, 0, time.UTC)

	path, err := tracker.WriteImprovementProposal(ImprovementProposal{
		SkillName:       "pr-review",
		SessionID:       "sess-1",
		Reason:          "User corrected review ordering.",
		Evidence:        []string{"review missed tests first", "user asked for findings before summary"},
		SuggestedChange: "Start review output with findings and exact file references.",
		CreatedAt:       createdAt,
	})
	if err != nil {
		t.Fatalf("WriteImprovementProposal() error = %v", err)
	}
	wantPath := filepath.Join(dir, "skill-improvement-proposals", "20260517T040506Z-pr-review.md")
	if path != wantPath {
		t.Fatalf("proposal path = %q, want %q", path, wantPath)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read proposal: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"type: skill-improvement-proposal",
		"skill: pr-review",
		"session_id: sess-1",
		"User corrected review ordering.",
		"review missed tests first",
		"Start review output with findings",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("proposal missing %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "pr-review.md")); !os.IsNotExist(err) {
		t.Fatalf("WriteImprovementProposal touched skill file, stat err = %v", err)
	}
}

func TestTrackerReadImprovementProposal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tracker := NewTracker(dir)
	createdAt := time.Date(2026, 5, 17, 4, 5, 6, 0, time.UTC)
	path, err := tracker.WriteImprovementProposal(ImprovementProposal{
		SkillName:       "pr-review",
		SessionID:       "sess-1",
		Reason:          "User corrected review ordering.",
		Evidence:        []string{"findings should come first"},
		SuggestedChange: "Start with findings before summary.",
		CreatedAt:       createdAt,
	})
	if err != nil {
		t.Fatalf("WriteImprovementProposal() error = %v", err)
	}

	got, err := tracker.ReadImprovementProposal(filepath.Base(path))
	if err != nil {
		t.Fatalf("ReadImprovementProposal() error = %v", err)
	}
	if got.SkillName != "pr-review" || got.SessionID != "sess-1" || got.Reason != "User corrected review ordering." || got.SuggestedChange != "Start with findings before summary." {
		t.Fatalf("proposal = %+v", got)
	}
	if !reflect.DeepEqual(got.Evidence, []string{"findings should come first"}) {
		t.Fatalf("Evidence = %#v", got.Evidence)
	}
}

func TestTrackerReadImprovementProposalRejectsOutsidePath(t *testing.T) {
	t.Parallel()

	tracker := NewTracker(t.TempDir())
	_, err := tracker.ReadImprovementProposal(filepath.Join("..", "outside.md"))
	if err == nil || !strings.Contains(err.Error(), "must be under") {
		t.Fatalf("ReadImprovementProposal outside err = %v, want proposal-dir boundary", err)
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
