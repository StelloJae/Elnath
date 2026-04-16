package prompt

import (
	"context"
	"database/sql"
	testing "testing"
	"time"

	"github.com/stello/elnath/internal/wiki"
	_ "modernc.org/sqlite"
)

func newTestWikiIndex(t *testing.T) *wiki.Index {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	idx, err := wiki.NewIndex(db)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	return idx
}

func TestWikiRAGNodeNilState(t *testing.T) {
	t.Parallel()

	got, err := NewWikiRAGNode(10, 3).Render(context.Background(), nil)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestWikiRAGNodeNilWikiIdx(t *testing.T) {
	t.Parallel()

	got, err := NewWikiRAGNode(10, 3).Render(context.Background(), &RenderState{UserInput: "golang"})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestWikiRAGNodeEmptyUserInput(t *testing.T) {
	t.Parallel()

	got, err := NewWikiRAGNode(10, 3).Render(context.Background(), &RenderState{WikiIdx: newTestWikiIndex(t)})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestWikiRAGNodeCallsBuildRAGContext(t *testing.T) {
	t.Parallel()

	idx := newTestWikiIndex(t)
	now := time.Now().UTC()
	page := &wiki.Page{
		Path:    "concepts/go.md",
		Title:   "Go",
		Type:    wiki.PageTypeConcept,
		Content: "Go is a statically typed compiled language.",
		Tags:    []string{"go", "language"},
		Created: now,
		Updated: now,
	}
	if err := idx.Upsert(page); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	node := NewWikiRAGNode(10, 2)
	state := &RenderState{WikiIdx: idx, UserInput: "compiled language"}
	got, err := node.Render(context.Background(), state)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}

	want := wiki.BuildRAGContext(context.Background(), idx, state.UserInput, 2, ScanContent)
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	if got == "" {
		t.Fatal("Render returned empty string")
	}
}

func TestWikiRAGNodeDefaultMaxResults(t *testing.T) {
	t.Parallel()

	node := NewWikiRAGNode(7, 0)
	if node.maxResults != 3 {
		t.Fatalf("maxResults = %d, want 3", node.maxResults)
	}
}

func TestWikiRAGNodeSkipsInBenchmarkMode(t *testing.T) {
	t.Parallel()

	got, err := NewWikiRAGNode(10, 3).Render(context.Background(), &RenderState{
		BenchmarkMode: true,
		WikiIdx:       newTestWikiIndex(t),
		UserInput:     "compiled language",
	})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}
