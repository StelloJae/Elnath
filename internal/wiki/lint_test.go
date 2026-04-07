package wiki

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestLinter(t *testing.T) (*Linter, *Store) {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	idx, err := NewIndex(db)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}

	return NewLinter(store, idx), store
}

// TestLintOrphanLinks verifies that a page not referenced from index.md is
// flagged as an orphan, and a page that IS linked is not flagged.
func TestLintOrphanLinks(t *testing.T) {
	linter, store := newTestLinter(t)

	// Create the orphan page (no entry in index.md).
	orphan := &Page{
		Path:    "orphan.md",
		Title:   "Orphaned Page",
		Type:    PageTypeConcept,
		Content: "This page has no inbound links.",
	}
	if err := store.Create(orphan); err != nil {
		t.Fatalf("Create orphan: %v", err)
	}

	// Create the linked page.
	linked := &Page{
		Path:    "linked.md",
		Title:   "Linked Page",
		Type:    PageTypeConcept,
		Content: "This page is referenced from index.md.",
	}
	if err := store.Create(linked); err != nil {
		t.Fatalf("Create linked: %v", err)
	}

	// Write an index.md that only links to linked.md.
	indexContent := "# Index\n\n- [Linked Page](linked.md)\n"
	if err := os.WriteFile(filepath.Join(store.WikiDir(), "index.md"), []byte(indexContent), 0o644); err != nil {
		t.Fatalf("write index.md: %v", err)
	}

	issues, err := linter.Lint(context.Background())
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}

	orphanFlagged := false
	linkedFlagged := false
	for _, issue := range issues {
		if issue.Type == IssueOrphan {
			if issue.Path == "orphan.md" {
				orphanFlagged = true
			}
			if issue.Path == "linked.md" {
				linkedFlagged = true
			}
		}
	}

	if !orphanFlagged {
		t.Errorf("expected orphan.md to be flagged as orphan; issues: %v", issues)
	}
	if linkedFlagged {
		t.Errorf("linked.md should not be flagged as orphan; issues: %v", issues)
	}
}

// TestLintMissingFrontmatter verifies that a raw .md file with no frontmatter
// delimiter is reported as a missing-frontmatter error.
func TestLintMissingFrontmatter(t *testing.T) {
	linter, store := newTestLinter(t)

	// Write a malformed .md file directly (bypassing Store.Create) so it has
	// no YAML frontmatter.
	badFile := filepath.Join(store.WikiDir(), "bad.md")
	if err := os.WriteFile(badFile, []byte("just plain text, no frontmatter\n"), 0o644); err != nil {
		t.Fatalf("write bad.md: %v", err)
	}

	issues, err := linter.Lint(context.Background())
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}

	found := false
	for _, issue := range issues {
		if issue.Path == "bad.md" && issue.Type == IssueMissingFrontmatter {
			found = true
		}
	}
	if !found {
		t.Errorf("expected bad.md to be flagged as missing_frontmatter; issues: %v", issues)
	}
}
