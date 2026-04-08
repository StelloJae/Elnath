package wiki

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBuildRAGContext_NilIndex(t *testing.T) {
	result := BuildRAGContext(context.Background(), nil, "some query", 3)
	if result != "" {
		t.Errorf("expected empty string for nil index, got %q", result)
	}
}

func TestBuildRAGContext_EmptyQuery(t *testing.T) {
	idx := newTestIndex(t)
	result := BuildRAGContext(context.Background(), idx, "", 3)
	if result != "" {
		t.Errorf("expected empty string for empty query, got %q", result)
	}
}

func TestBuildRAGContext_NoMatches(t *testing.T) {
	idx := newTestIndex(t)
	result := BuildRAGContext(context.Background(), idx, "xyzzy_no_match_ever", 3)
	if result != "" {
		t.Errorf("expected empty string when no pages match, got %q", result)
	}
}

func TestBuildRAGContext_WithMatches(t *testing.T) {
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
	}
	for _, p := range pages {
		if err := idx.Upsert(p); err != nil {
			t.Fatalf("Upsert %q: %v", p.Path, err)
		}
	}

	result := BuildRAGContext(context.Background(), idx, "language", 3)
	if result == "" {
		t.Fatal("expected non-empty RAG context for matching query")
	}
	if !strings.Contains(result, "Relevant knowledge from wiki:") {
		t.Errorf("expected header in result, got %q", result)
	}
	if !strings.Contains(result, "Go Programming Language") && !strings.Contains(result, "Python Programming Language") {
		t.Errorf("expected at least one page title in result, got %q", result)
	}
}

func TestBuildRAGContext_ContentTruncation(t *testing.T) {
	idx := newTestIndex(t)
	now := time.Now().UTC()

	longContent := strings.Repeat("word ", 200) // well over 500 chars
	page := &Page{
		Path:    "concepts/long.md",
		Title:   "Long Page",
		Type:    PageTypeConcept,
		Content: longContent,
		Created: now,
		Updated: now,
	}
	if err := idx.Upsert(page); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	result := BuildRAGContext(context.Background(), idx, "word", 3)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	// The full content is ~1000 chars; the result must be shorter due to truncation.
	if strings.Contains(result, longContent) {
		t.Error("expected content to be truncated, but full content appeared")
	}
	if !strings.Contains(result, "...") {
		t.Error("expected truncation indicator '...' in result")
	}
}

func TestBuildRAGContext_MultipleResults(t *testing.T) {
	idx := newTestIndex(t)
	now := time.Now().UTC()

	pages := []*Page{
		{
			Path:    "a.md",
			Title:   "Alpha",
			Type:    PageTypeConcept,
			Content: "database storage and retrieval",
			Created: now,
			Updated: now,
		},
		{
			Path:    "b.md",
			Title:   "Beta",
			Type:    PageTypeConcept,
			Content: "database indexing strategies",
			Created: now,
			Updated: now,
		},
		{
			Path:    "c.md",
			Title:   "Gamma",
			Type:    PageTypeConcept,
			Content: "database query optimization",
			Created: now,
			Updated: now,
		},
	}
	for _, p := range pages {
		if err := idx.Upsert(p); err != nil {
			t.Fatalf("Upsert %q: %v", p.Path, err)
		}
	}

	result := BuildRAGContext(context.Background(), idx, "database", 3)
	if result == "" {
		t.Fatal("expected non-empty result for multiple matches")
	}

	// Count how many ### headers appear — each result gets one.
	count := strings.Count(result, "### ")
	if count == 0 {
		t.Errorf("expected at least one result section, got 0")
	}
	if count > 3 {
		t.Errorf("expected at most 3 results (maxResults=3), got %d", count)
	}
}
