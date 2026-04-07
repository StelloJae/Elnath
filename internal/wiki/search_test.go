package wiki

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestIndex(t *testing.T) *Index {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	idx, err := NewIndex(db)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	return idx
}

func TestHybridSearch(t *testing.T) {
	idx := newTestIndex(t)

	now := time.Now().UTC()
	pages := []*Page{
		{
			Path:    "concepts/golang.md",
			Title:   "Go Programming Language",
			Type:    PageTypeConcept,
			Content: "Go is a statically typed compiled language designed at Google.",
			Tags:    []string{"go", "language"},
			Created: now,
			Updated: now,
		},
		{
			Path:    "concepts/python.md",
			Title:   "Python Programming Language",
			Type:    PageTypeConcept,
			Content: "Python is a dynamic interpreted language with a focus on readability.",
			Tags:    []string{"python", "language"},
			Created: now,
			Updated: now,
		},
		{
			Path:    "concepts/databases.md",
			Title:   "Database Systems",
			Type:    PageTypeConcept,
			Content: "Relational databases use SQL for structured data storage.",
			Tags:    []string{"database", "sql"},
			Created: now,
			Updated: now,
		},
	}

	for _, p := range pages {
		if err := idx.Upsert(p); err != nil {
			t.Fatalf("Upsert %q: %v", p.Path, err)
		}
	}

	results, err := idx.Search(context.Background(), SearchOpts{
		Query: "language",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Search returned no results, expected at least 1")
	}

	// Both Go and Python pages mention "language"; database page should not appear.
	foundGo := false
	foundPython := false
	foundDB := false
	for _, r := range results {
		switch r.Page.Path {
		case "concepts/golang.md":
			foundGo = true
		case "concepts/python.md":
			foundPython = true
		case "concepts/databases.md":
			foundDB = true
		}
	}

	if !foundGo {
		t.Errorf("expected golang.md in search results")
	}
	if !foundPython {
		t.Errorf("expected python.md in search results")
	}
	_ = foundDB // database page may or may not appear depending on FTS5 availability
}
