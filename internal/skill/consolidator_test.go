package skill

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stello/elnath/internal/wiki"
)

func TestConsolidatorPromotesMeetingThreshold(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	tracker := NewTracker(t.TempDir())
	registry := NewRegistry()
	creator := NewCreator(store, tracker, registry)

	if _, err := creator.Create(CreateParams{
		Name:   "deploy-check",
		Prompt: "Check deployment status.",
		Status: "draft",
		Source: "analyst",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := tracker.RecordPattern(PatternRecord{
		ID:         "pat-1",
		DraftSkill: "deploy-check",
		SessionIDs: []string{"sess-1", "sess-2", "sess-3"},
	}); err != nil {
		t.Fatalf("RecordPattern(pat-1) error = %v", err)
	}
	if err := tracker.RecordPattern(PatternRecord{
		ID:         "pat-2",
		DraftSkill: "deploy-check",
		SessionIDs: []string{"sess-4", "sess-5"},
	}); err != nil {
		t.Fatalf("RecordPattern(pat-2) error = %v", err)
	}

	consolidator := NewConsolidator(creator, tracker, registry, store, ConsolidatorConfig{
		MinSessions:   5,
		MinPrevalence: 2,
		MaxDraftAge:   90 * 24 * time.Hour,
	})

	result, err := consolidator.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !slices.Contains(result.Promoted, "deploy-check") {
		t.Fatalf("Promoted = %v, want deploy-check", result.Promoted)
	}

	page, err := store.Read("skills/deploy-check.md")
	if err != nil {
		t.Fatalf("Read(promoted skill) error = %v", err)
	}
	if got := page.Extra["status"]; got != "active" {
		t.Fatalf("promoted status = %v, want active", got)
	}
	if got := page.Extra["source"]; got != "promoted" {
		t.Fatalf("promoted source = %v, want promoted", got)
	}
	if _, ok := registry.Get("deploy-check"); !ok {
		t.Fatal("registry missing promoted skill after Run()")
	}
}

func TestConsolidatorSkipsBelowThreshold(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	tracker := NewTracker(t.TempDir())
	registry := NewRegistry()
	creator := NewCreator(store, tracker, registry)

	if _, err := creator.Create(CreateParams{
		Name:   "draft-only",
		Prompt: "Still a draft.",
		Status: "draft",
		Source: "analyst",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := tracker.RecordPattern(PatternRecord{
		ID:         "pat-1",
		DraftSkill: "draft-only",
		SessionIDs: []string{"sess-1", "sess-2", "sess-3"},
	}); err != nil {
		t.Fatalf("RecordPattern(pat-1) error = %v", err)
	}
	if err := tracker.RecordPattern(PatternRecord{
		ID:         "pat-2",
		DraftSkill: "draft-only",
		SessionIDs: []string{"sess-3", "sess-4"},
	}); err != nil {
		t.Fatalf("RecordPattern(pat-2) error = %v", err)
	}

	consolidator := NewConsolidator(creator, tracker, registry, store, ConsolidatorConfig{
		MinSessions:   5,
		MinPrevalence: 2,
		MaxDraftAge:   90 * 24 * time.Hour,
	})

	result, err := consolidator.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Promoted) != 0 {
		t.Fatalf("Promoted = %v, want empty", result.Promoted)
	}

	page, err := store.Read("skills/draft-only.md")
	if err != nil {
		t.Fatalf("Read(draft skill) error = %v", err)
	}
	if got := page.Extra["status"]; got != "draft" {
		t.Fatalf("draft status = %v, want draft", got)
	}
	if _, ok := registry.Get("draft-only"); ok {
		t.Fatal("registry unexpectedly contains unpromoted draft")
	}
}

func TestConsolidatorCleansOldDrafts(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	tracker := NewTracker(t.TempDir())
	registry := NewRegistry()
	creator := NewCreator(store, tracker, registry)

	if _, err := creator.Create(CreateParams{
		Name:   "expired-draft",
		Prompt: "Temporary draft.",
		Status: "draft",
		Source: "analyst",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	consolidator := NewConsolidator(creator, tracker, registry, store, ConsolidatorConfig{
		MinSessions:   5,
		MinPrevalence: 2,
		MaxDraftAge:   0,
	})

	result, err := consolidator.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !slices.Contains(result.Cleaned, "expired-draft") {
		t.Fatalf("Cleaned = %v, want expired-draft", result.Cleaned)
	}
	if _, err := store.Read("skills/expired-draft.md"); err == nil {
		t.Fatal("Read(deleted draft) error = nil, want not found")
	}
}
