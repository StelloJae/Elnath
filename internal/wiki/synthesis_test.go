package wiki

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSynthesisSlug(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"File Writes", "file-writes"},
		{"file writes", "file-writes"},
		{"FILE WRITES", "file-writes"},
		{"File!!!Writes", "file-writes"},
		{"  file  writes  ", "file-writes"},
		{"abc123", "abc123"},
		{"", "misc"},
		{"!!!", "misc"},
		{"   ", "misc"},
		{"한글 topic", "topic"},
		{"already-slugged", "already-slugged"},
	}
	for _, tc := range cases {
		got := SynthesisSlug(tc.in)
		if got != tc.want {
			t.Errorf("SynthesisSlug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSynthesisID_StableForSameText(t *testing.T) {
	a := SynthesisID("atomic swap wins")
	b := SynthesisID("atomic swap wins")
	if a != b {
		t.Errorf("same text should yield same ID: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, SynthesisIDPrefix) {
		t.Errorf("id %q missing prefix %q", a, SynthesisIDPrefix)
	}
	c := SynthesisID("atomic swap wins ")
	if a == c {
		t.Errorf("different text should yield different IDs, both %q", a)
	}
}

func TestBuildSynthesisPage_HasProvenanceAndPath(t *testing.T) {
	created := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	page := BuildSynthesisPage("synth-abcd1234", "File Writes", "Prefer atomic rename.", []string{"io", "fs"}, created)
	if page.Path != "synthesis/file-writes/2026-04-17-abcd1234.md" {
		t.Errorf("path = %q", page.Path)
	}
	if got := page.PageSource(); got != SourceConsolidation {
		t.Errorf("source = %q, want %q", got, SourceConsolidation)
	}
	if got := page.PageSourceEvent(); got != "synth-abcd1234" {
		t.Errorf("source_event = %q, want synth-abcd1234", got)
	}
	if page.Type != PageTypeAnalysis {
		t.Errorf("type = %q, want analysis", page.Type)
	}
	if got := page.Tags; len(got) != 2 || got[0] != "io" || got[1] != "fs" {
		t.Errorf("tags = %v, want [io fs]", got)
	}
	if !strings.Contains(page.Title, "File Writes") {
		t.Errorf("title missing topic: %q", page.Title)
	}
}

func TestBuildSynthesisPage_DefaultsTagsFromPrimaryTopic(t *testing.T) {
	created := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	page := BuildSynthesisPage("synth-abcd1234", "Caching", "body", nil, created)
	if len(page.Tags) != 1 || page.Tags[0] != "Caching" {
		t.Errorf("tags = %v, want [Caching]", page.Tags)
	}
}

func TestBuildSynthesisPage_EmptyTopicFallsBackToMisc(t *testing.T) {
	created := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	page := BuildSynthesisPage("synth-00000000", "", "body", nil, created)
	if !strings.HasPrefix(page.Path, "synthesis/misc/") {
		t.Errorf("path = %q, want synthesis/misc/ prefix", page.Path)
	}
	if len(page.Tags) != 0 {
		t.Errorf("tags = %v, want empty", page.Tags)
	}
	if !strings.HasPrefix(page.Title, "Synthesis 2026-04-17") {
		t.Errorf("title = %q, want 'Synthesis 2026-04-17' prefix when topic empty", page.Title)
	}
}

func TestBuildSynthesisPage_DerivesIDWhenEmpty(t *testing.T) {
	created := time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC)
	page := BuildSynthesisPage("", "Topic", "body text", []string{"io"}, created)
	if page.PageSourceEvent() == "" {
		t.Fatal("empty synthesisID should be derived, got empty source_event")
	}
	if !strings.HasPrefix(page.PageSourceEvent(), SynthesisIDPrefix) {
		t.Errorf("derived id missing prefix: %q", page.PageSourceEvent())
	}
}

func TestBuildSynthesisPage_PersistsThroughStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "wiki"))
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	page := BuildSynthesisPage("synth-abcd1234", "File Writes", "Prefer atomic rename.", []string{"io"}, created)
	if err := store.Create(page); err != nil {
		t.Fatalf("Create: %v", err)
	}
	readBack, err := store.Read(page.Path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := readBack.PageSource(); got != SourceConsolidation {
		t.Errorf("round-tripped source = %q, want %q", got, SourceConsolidation)
	}
	if readBack.PageSourceEvent() != "synth-abcd1234" {
		t.Errorf("round-tripped source_event = %q", readBack.PageSourceEvent())
	}
	if !strings.Contains(readBack.Content, "atomic rename") {
		t.Errorf("content lost body text: %q", readBack.Content)
	}
}
