package magicdocs

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stello/elnath/internal/wiki"
)

func testStore(t *testing.T) *wiki.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := wiki.NewStore(filepath.Join(dir, "wiki"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func TestWikiWriter_CreatePage(t *testing.T) {
	store := testStore(t)
	w := NewWikiWriter(store, slog.Default())

	actions := []PageAction{{
		Action:     "create",
		Path:       "analyses/test-finding.md",
		Title:      "Test Finding",
		Type:       "analysis",
		Content:    "Some finding content",
		Confidence: "medium",
		Tags:       []string{"test"},
	}}

	created, updated := w.Apply(actions, "sess-1", "agent_finish")
	if created != 1 || updated != 0 {
		t.Errorf("created=%d updated=%d, want 1,0", created, updated)
	}

	page, err := store.Read("analyses/test-finding.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if page.Title != "Test Finding" {
		t.Errorf("Title = %q, want %q", page.Title, "Test Finding")
	}
	source, _ := page.Extra["source"].(string)
	if source != "magic-docs" {
		t.Errorf("Extra[source] = %q, want %q", source, "magic-docs")
	}
	sess, _ := page.Extra["source_session"].(string)
	if sess != "sess-1" {
		t.Errorf("Extra[source_session] = %q, want %q", sess, "sess-1")
	}
}

func TestWikiWriter_UpdateOwnedPage(t *testing.T) {
	store := testStore(t)
	w := NewWikiWriter(store, slog.Default())

	err := store.Create(&wiki.Page{
		Path:    "analyses/owned.md",
		Title:   "Owned",
		Type:    wiki.PageTypeAnalysis,
		Content: "original",
		Extra: map[string]any{
			"source":         "magic-docs",
			"source_session": "old-sess",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	actions := []PageAction{{
		Action:     "update",
		Path:       "analyses/owned.md",
		Title:      "Owned Updated",
		Type:       "analysis",
		Content:    "updated content",
		Confidence: "high",
		Tags:       []string{"updated"},
	}}

	created, updated := w.Apply(actions, "sess-2", "research_progress")
	if created != 0 || updated != 1 {
		t.Errorf("created=%d updated=%d, want 0,1", created, updated)
	}

	page, _ := store.Read("analyses/owned.md")
	if page.Content != "updated content\n" {
		t.Errorf("Content = %q, want %q", page.Content, "updated content\n")
	}
}

func TestWikiWriter_UpdateHumanPage_CreatesLinkedPage(t *testing.T) {
	store := testStore(t)
	w := NewWikiWriter(store, slog.Default())

	err := store.Create(&wiki.Page{
		Path:    "concepts/go-errors.md",
		Title:   "Go Errors",
		Type:    wiki.PageTypeConcept,
		Content: "Human written content",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	actions := []PageAction{{
		Action:     "update",
		Path:       "concepts/go-errors.md",
		Title:      "Go Error Wrapping Discovery",
		Type:       "concept",
		Content:    "Auto-discovered pattern",
		Confidence: "medium",
		Tags:       []string{"go"},
	}}

	created, updated := w.Apply(actions, "sess-3", "agent_finish")
	if created != 1 || updated != 0 {
		t.Errorf("created=%d updated=%d, want 1,0 (should create linked, not update)", created, updated)
	}

	original, _ := store.Read("concepts/go-errors.md")
	if original.Content != "Human written content\n" {
		t.Error("human page should not be modified")
	}
}

func TestWikiWriter_UpdateNonexistent_FallsBackToCreate(t *testing.T) {
	store := testStore(t)
	w := NewWikiWriter(store, slog.Default())

	actions := []PageAction{{
		Action:     "update",
		Path:       "analyses/nonexistent.md",
		Title:      "New Finding",
		Type:       "analysis",
		Content:    "Content",
		Confidence: "low",
	}}

	created, updated := w.Apply(actions, "sess-4", "agent_finish")
	if created != 1 || updated != 0 {
		t.Errorf("created=%d updated=%d, want 1,0 (fallback to create)", created, updated)
	}
}

func TestIsOwnedByMagicDocs(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]any
		want  bool
	}{
		{"both present", map[string]any{"source": "magic-docs", "source_session": "s1"}, true},
		{"source only", map[string]any{"source": "magic-docs"}, true},
		{"wrong source", map[string]any{"source": "human", "source_session": "s1"}, false},
		{"nil extra", nil, false},
		{"empty extra", map[string]any{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := &wiki.Page{Extra: tt.extra}
			if got := page.IsOwnedBy(wiki.SourceMagicDocs); got != tt.want {
				t.Errorf("IsOwnedBy(SourceMagicDocs) = %v, want %v", got, tt.want)
			}
		})
	}
}
