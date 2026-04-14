package prompt

import (
	"context"
	"strings"
	testing "testing"
	"time"

	"github.com/stello/elnath/internal/wiki"
)

func TestMemoryContextNodeRendersSearchResults(t *testing.T) {
	t.Parallel()

	idx := newTestWikiIndex(t)
	seedMemoryPage(t, idx, "memory/one.md", "Session Memory One", "session summary memory context helps resume earlier work")

	got, err := NewMemoryContextNode(55, 5, 1200).Render(context.Background(), &RenderState{WikiIdx: idx})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	for _, want := range []string{"<<memory_context>>", "[Session Memory One]", "session summary memory context", "<</memory_context>>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render = %q, want substring %q", got, want)
		}
	}
}

func TestMemoryContextNodeRespectsMaxEntries(t *testing.T) {
	t.Parallel()

	idx := newTestWikiIndex(t)
	seedMemoryPage(t, idx, "memory/one.md", "Memory One", "session summary memory context one")
	seedMemoryPage(t, idx, "memory/two.md", "Memory Two", "session summary memory context two")

	got, err := NewMemoryContextNode(55, 1, 1200).Render(context.Background(), &RenderState{WikiIdx: idx})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if strings.Count(got, "[") != 1 {
		t.Fatalf("Render = %q, want exactly one entry", got)
	}
}

func TestMemoryContextNodeRespectsMaxChars(t *testing.T) {
	t.Parallel()

	idx := newTestWikiIndex(t)
	seedMemoryPage(t, idx, "memory/one.md", "Memory One", "session summary memory context "+strings.Repeat("x", 200))

	got, err := NewMemoryContextNode(55, 5, 40).Render(context.Background(), &RenderState{WikiIdx: idx})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if strings.Contains(got, strings.Repeat("x", 80)) {
		t.Fatalf("Render = %q, want truncated content", got)
	}
	if !strings.Contains(got, "[Memory One]") {
		t.Fatalf("Render = %q, want title retained", got)
	}
}

func TestMemoryContextNodeFallsBackToSessionPages(t *testing.T) {
	t.Parallel()

	idx := newTestWikiIndex(t)
	seedMemorySourcePage(t, idx, "sessions/sess-123.md", "Session sess-123", "## Session Metadata\n\n- **Session ID**: sess-123\n\n## Summary\n\nResumed work on the prompt graph.", []string{"session", "interactive_session"})

	got, err := NewMemoryContextNode(55, 5, 1200).Render(context.Background(), &RenderState{WikiIdx: idx})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "[Session sess-123]") || !strings.Contains(got, "## Summary") {
		t.Fatalf("Render = %q, want session fallback content", got)
	}
}

func TestMemoryContextNodeNilWikiIndex(t *testing.T) {
	t.Parallel()

	got, err := NewMemoryContextNode(55, 5, 1200).Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestMemoryContextNodeSkipsBenchmarkMode(t *testing.T) {
	t.Parallel()

	got, err := NewMemoryContextNode(55, 5, 1200).Render(context.Background(), &RenderState{
		BenchmarkMode: true,
		WikiIdx:       newTestWikiIndex(t),
	})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestMemoryContextNodeNoSearchResults(t *testing.T) {
	t.Parallel()

	got, err := NewMemoryContextNode(55, 5, 1200).Render(context.Background(), &RenderState{WikiIdx: newTestWikiIndex(t)})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func seedMemoryPage(t *testing.T, idx *wiki.Index, path, title, content string) {
	t.Helper()

	now := time.Now().UTC()
	if err := idx.Upsert(&wiki.Page{
		Path:    path,
		Title:   title,
		Type:    wiki.PageTypeConcept,
		Content: content,
		Created: now,
		Updated: now,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
}

func seedMemorySourcePage(t *testing.T, idx *wiki.Index, path, title, content string, tags []string) {
	t.Helper()

	now := time.Now().UTC()
	if err := idx.Upsert(&wiki.Page{
		Path:    path,
		Title:   title,
		Type:    wiki.PageTypeSource,
		Tags:    tags,
		Content: content,
		Created: now,
		Updated: now,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
}
