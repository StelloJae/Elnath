package wiki

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newCrossTestIndex(t *testing.T) *Index {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open wiki db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	idx, err := NewIndex(db)
	if err != nil {
		t.Fatalf("new index: %v", err)
	}
	return idx
}

func insertCrossPage(t *testing.T, idx *Index, path, title, content string) {
	t.Helper()
	page := &Page{
		Path:    path,
		Title:   title,
		Type:    PageTypeConcept,
		Content: content,
		Created: time.Now(),
		Updated: time.Now(),
	}
	if err := idx.Upsert(page); err != nil {
		t.Fatalf("upsert page %q: %v", path, err)
	}
}

func TestCrossProjectSearcher_Empty(t *testing.T) {
	s := NewCrossProjectSearcher()
	results, err := s.Search(context.Background(), "anything", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestCrossProjectSearcher_SingleProject(t *testing.T) {
	idx := newCrossTestIndex(t)
	insertCrossPage(t, idx, "concepts/golang.md", "Go Language", "Go is a statically typed compiled language.")

	s := NewCrossProjectSearcher()
	s.AddProject("proj-a", idx)

	results, err := s.Search(context.Background(), "Go", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].Project != "proj-a" {
		t.Errorf("expected project %q, got %q", "proj-a", results[0].Project)
	}
}

func TestCrossProjectSearcher_MultipleProjectsCombinedAndSorted(t *testing.T) {
	idxA := newCrossTestIndex(t)
	insertCrossPage(t, idxA, "concepts/alpha.md", "Alpha Concept", "This is alpha content about testing.")

	idxB := newCrossTestIndex(t)
	insertCrossPage(t, idxB, "concepts/beta.md", "Beta Concept", "This is beta content about testing.")
	insertCrossPage(t, idxB, "concepts/gamma.md", "Gamma Concept", "Gamma also discusses testing extensively.")

	s := NewCrossProjectSearcher()
	s.AddProject("proj-a", idxA)
	s.AddProject("proj-b", idxB)

	results, err := s.Search(context.Background(), "testing", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("expected at least 2 results across projects, got %d", len(results))
	}

	// Verify sorted by score descending.
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted by score at index %d: %.4f > %.4f",
				i, results[i].Score, results[i-1].Score)
		}
	}

	// Verify both projects are represented.
	projects := map[string]bool{}
	for _, r := range results {
		projects[r.Project] = true
	}
	if !projects["proj-a"] || !projects["proj-b"] {
		t.Errorf("expected both projects in results, got: %v", projects)
	}
}

func TestCrossProjectSearcher_LimitRespected(t *testing.T) {
	idxA := newCrossTestIndex(t)
	for i := 0; i < 5; i++ {
		insertCrossPage(t, idxA,
			"concepts/page-a"+string(rune('0'+i))+".md",
			"Page A",
			"common keyword appears here",
		)
	}

	idxB := newCrossTestIndex(t)
	for i := 0; i < 5; i++ {
		insertCrossPage(t, idxB,
			"concepts/page-b"+string(rune('0'+i))+".md",
			"Page B",
			"common keyword appears here",
		)
	}

	s := NewCrossProjectSearcher()
	s.AddProject("proj-a", idxA)
	s.AddProject("proj-b", idxB)

	limit := 3
	results, err := s.Search(context.Background(), "common keyword", limit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) > limit {
		t.Errorf("expected at most %d results, got %d", limit, len(results))
	}
}

func TestCrossProjectSearcher_FailedProjectSkipped(t *testing.T) {
	idxA := newCrossTestIndex(t)
	insertCrossPage(t, idxA, "concepts/healthy.md", "Healthy Page", "This project is healthy and searchable.")

	// Build an index whose DB is immediately closed to simulate failure.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db b: %v", err)
	}
	idxB, err := NewIndex(db)
	if err != nil {
		t.Fatalf("new index b: %v", err)
	}
	db.Close() // force search errors on this index

	s := NewCrossProjectSearcher()
	s.AddProject("proj-a", idxA)
	s.AddProject("proj-b", idxB)

	results, err := s.Search(context.Background(), "healthy", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// proj-b fails; proj-a should still return results.
	if len(results) == 0 {
		t.Error("expected results from healthy project, got none")
	}
	for _, r := range results {
		if r.Project == "proj-b" {
			t.Errorf("broken project proj-b should have been skipped")
		}
	}
}
