package learning

import (
	"path/filepath"
	"testing"
	"time"
)

func seedLessons(t *testing.T, s *Store, lessons []Lesson) {
	t.Helper()
	for _, l := range lessons {
		if err := s.Append(l); err != nil {
			t.Fatalf("seed append: %v", err)
		}
	}
}

func storeAt(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
}

func TestMarkSuperseded_UpdatesMatchingIDs(t *testing.T) {
	s := storeAt(t)
	base := time.Now().UTC()
	seedLessons(t, s, []Lesson{
		{ID: "a", Text: "one", Source: "t", Confidence: "high", Created: base.Add(-3 * time.Hour)},
		{ID: "b", Text: "two", Source: "t", Confidence: "high", Created: base.Add(-2 * time.Hour)},
		{ID: "c", Text: "three", Source: "t", Confidence: "high", Created: base.Add(-1 * time.Hour)},
	})

	n, err := s.MarkSuperseded([]string{"a", "c"}, "synth-xyz")
	if err != nil {
		t.Fatalf("MarkSuperseded: %v", err)
	}
	if n != 2 {
		t.Errorf("updated=%d, want 2", n)
	}

	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	marks := map[string]string{}
	for _, l := range got {
		marks[l.ID] = l.SupersededBy
	}
	if marks["a"] != "synth-xyz" {
		t.Errorf("a.SupersededBy = %q, want synth-xyz", marks["a"])
	}
	if marks["b"] != "" {
		t.Errorf("b.SupersededBy = %q, want empty", marks["b"])
	}
	if marks["c"] != "synth-xyz" {
		t.Errorf("c.SupersededBy = %q, want synth-xyz", marks["c"])
	}
}

func TestMarkSuperseded_EmptySynthesisIDErrors(t *testing.T) {
	s := storeAt(t)
	if _, err := s.MarkSuperseded([]string{"a"}, ""); err == nil {
		t.Fatal("expected error for empty synthesisID")
	}
	if _, err := s.MarkSuperseded([]string{"a"}, "   "); err == nil {
		t.Fatal("expected error for whitespace-only synthesisID")
	}
}

func TestMarkSuperseded_IgnoresMissingAndEmptyIDs(t *testing.T) {
	s := storeAt(t)
	seedLessons(t, s, []Lesson{
		{ID: "a", Text: "one", Source: "t", Confidence: "high", Created: time.Now().UTC()},
	})

	n, err := s.MarkSuperseded([]string{"", "   ", "not-here"}, "synth-1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("updated=%d on all-miss input, want 0", n)
	}
}

func TestMarkSuperseded_IdempotentOnSameSynthesisID(t *testing.T) {
	s := storeAt(t)
	seedLessons(t, s, []Lesson{
		{ID: "a", Text: "one", Source: "t", Confidence: "high", Created: time.Now().UTC()},
	})

	n1, _ := s.MarkSuperseded([]string{"a"}, "synth-1")
	n2, _ := s.MarkSuperseded([]string{"a"}, "synth-1")
	if n1 != 1 {
		t.Errorf("first call updated=%d, want 1", n1)
	}
	if n2 != 0 {
		t.Errorf("second call updated=%d, want 0 (already superseded)", n2)
	}
}

func TestMarkSuperseded_DoesNotOverrideExistingLink(t *testing.T) {
	s := storeAt(t)
	seedLessons(t, s, []Lesson{
		{ID: "a", Text: "one", Source: "t", Confidence: "high", Created: time.Now().UTC()},
	})

	if _, err := s.MarkSuperseded([]string{"a"}, "synth-1"); err != nil {
		t.Fatal(err)
	}
	n, err := s.MarkSuperseded([]string{"a"}, "synth-2")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("updated=%d when re-stamping a different synth, want 0 (first link wins)", n)
	}
	got, _ := s.List()
	if got[0].SupersededBy != "synth-1" {
		t.Errorf("SupersededBy = %q, want synth-1 (first-write-wins)", got[0].SupersededBy)
	}
}

func TestRotate_KeepFnPreservesSupersededOldLessons(t *testing.T) {
	s := storeAt(t)
	base := time.Now().UTC()
	seedLessons(t, s, []Lesson{
		{ID: "old1", Text: "o1", Source: "t", Confidence: "high", Created: base.Add(-5 * time.Hour), SupersededBy: "synth-1"},
		{ID: "old2", Text: "o2", Source: "t", Confidence: "high", Created: base.Add(-4 * time.Hour)},
		{ID: "old3", Text: "o3", Source: "t", Confidence: "high", Created: base.Add(-3 * time.Hour), SupersededBy: "synth-2"},
		{ID: "new1", Text: "n1", Source: "t", Confidence: "high", Created: base.Add(-2 * time.Hour)},
		{ID: "new2", Text: "n2", Source: "t", Confidence: "high", Created: base.Add(-1 * time.Hour)},
	})

	archived, err := s.Rotate(RotateOpts{KeepLast: 2, KeepFn: KeepSuperseded})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if archived != 1 {
		t.Errorf("archived=%d, want 1 (only old2 should move)", archived)
	}

	kept, _ := s.List()
	ids := map[string]bool{}
	for _, l := range kept {
		ids[l.ID] = true
	}
	for _, want := range []string{"old1", "old3", "new1", "new2"} {
		if !ids[want] {
			t.Errorf("expected %s in kept set after rotate, got ids=%v", want, ids)
		}
	}
	if ids["old2"] {
		t.Errorf("old2 should have been archived, ids=%v", ids)
	}
}

func TestRotate_NoArchiveReturnsZero(t *testing.T) {
	s := storeAt(t)
	base := time.Now().UTC()
	seedLessons(t, s, []Lesson{
		{ID: "old1", Text: "o1", Source: "t", Confidence: "high", Created: base.Add(-3 * time.Hour), SupersededBy: "synth-1"},
		{ID: "old2", Text: "o2", Source: "t", Confidence: "high", Created: base.Add(-2 * time.Hour), SupersededBy: "synth-1"},
		{ID: "new1", Text: "n1", Source: "t", Confidence: "high", Created: base.Add(-1 * time.Hour)},
	})
	archived, err := s.Rotate(RotateOpts{KeepLast: 1, KeepFn: KeepSuperseded})
	if err != nil {
		t.Fatal(err)
	}
	if archived != 0 {
		t.Errorf("archived=%d, want 0 (all old lessons superseded)", archived)
	}
}
