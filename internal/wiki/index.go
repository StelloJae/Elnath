package wiki

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Index wraps a SQLite DB and provides FTS5-backed search for wiki pages.
// If the SQLite build does not include FTS5, hasFTS5 is false and search
// falls back to LIKE queries.
type Index struct {
	db      *sql.DB
	hasFTS5 bool
}

// NewIndex creates an Index using the provided wiki DB connection.
// It initialises the schema immediately.
func NewIndex(db *sql.DB) (*Index, error) {
	idx := &Index{
		db:      db,
		hasFTS5: hasFTS5(db),
	}
	if err := idx.InitSchema(); err != nil {
		return nil, fmt.Errorf("wiki index: init schema: %w", err)
	}
	return idx, nil
}

// hasFTS5 tests whether FTS5 is available in this SQLite build.
func hasFTS5(db *sql.DB) bool {
	_, err := db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS _fts5_probe USING fts5(x)")
	if err != nil {
		return false
	}
	db.Exec("DROP TABLE IF EXISTS _fts5_probe")
	return true
}

// InitSchema creates the wiki_pages table, the FTS5 virtual table (if available),
// and the sync triggers that keep them in step.
func (idx *Index) InitSchema() error {
	_, err := idx.db.Exec(`
CREATE TABLE IF NOT EXISTS wiki_pages (
    path       TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    type       TEXT NOT NULL,
    content    TEXT NOT NULL,
    tags       TEXT,
    confidence TEXT,
    ttl        TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`)
	if err != nil {
		return fmt.Errorf("create wiki_pages: %w", err)
	}

	if !idx.hasFTS5 {
		return nil
	}

	_, err = idx.db.Exec(`
CREATE VIRTUAL TABLE IF NOT EXISTS wiki_fts USING fts5(
    title,
    content,
    tags,
    content='wiki_pages',
    content_rowid='rowid',
    tokenize='unicode61 remove_diacritics 2'
)`)
	if err != nil {
		return fmt.Errorf("create wiki_fts: %w", err)
	}

	// Triggers keep wiki_fts in sync with wiki_pages automatically.
	triggers := []string{
		`CREATE TRIGGER IF NOT EXISTS wiki_pages_ai AFTER INSERT ON wiki_pages BEGIN
    INSERT INTO wiki_fts(rowid, title, content, tags)
    VALUES (new.rowid, new.title, new.content, new.tags);
END`,
		`CREATE TRIGGER IF NOT EXISTS wiki_pages_ad AFTER DELETE ON wiki_pages BEGIN
    INSERT INTO wiki_fts(wiki_fts, rowid, title, content, tags)
    VALUES ('delete', old.rowid, old.title, old.content, old.tags);
END`,
		`CREATE TRIGGER IF NOT EXISTS wiki_pages_au AFTER UPDATE ON wiki_pages BEGIN
    INSERT INTO wiki_fts(wiki_fts, rowid, title, content, tags)
    VALUES ('delete', old.rowid, old.title, old.content, old.tags);
    INSERT INTO wiki_fts(rowid, title, content, tags)
    VALUES (new.rowid, new.title, new.content, new.tags);
END`,
	}

	for _, t := range triggers {
		if _, err := idx.db.Exec(t); err != nil {
			return fmt.Errorf("create trigger: %w", err)
		}
	}

	return nil
}

// Upsert inserts or replaces a page record in wiki_pages.
// The FTS virtual table is updated automatically via triggers.
func (idx *Index) Upsert(page *Page) error {
	tags := encodeTags(page.Tags)
	createdAt := page.Created.UTC().Format(time.RFC3339)
	updatedAt := page.Updated.UTC().Format(time.RFC3339)

	_, err := idx.db.Exec(`
INSERT INTO wiki_pages (path, title, type, content, tags, confidence, ttl, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
    title      = excluded.title,
    type       = excluded.type,
    content    = excluded.content,
    tags       = excluded.tags,
    confidence = excluded.confidence,
    ttl        = excluded.ttl,
    updated_at = excluded.updated_at`,
		page.Path, page.Title, string(page.Type), page.Content,
		tags, page.Confidence, page.TTL, createdAt, updatedAt,
	)
	if err != nil {
		return fmt.Errorf("wiki index: upsert %q: %w", page.Path, err)
	}
	return nil
}

// Remove deletes a page record from wiki_pages (FTS sync via trigger).
func (idx *Index) Remove(path string) error {
	_, err := idx.db.Exec("DELETE FROM wiki_pages WHERE path = ?", path)
	if err != nil {
		return fmt.Errorf("wiki index: remove %q: %w", path, err)
	}
	return nil
}

// Rebuild drops and recreates the FTS index, then re-indexes all pages from store.
func (idx *Index) Rebuild(store *Store) error {
	if idx.hasFTS5 {
		if _, err := idx.db.Exec("INSERT INTO wiki_fts(wiki_fts) VALUES ('rebuild')"); err != nil {
			// Non-fatal: full content table rebuild may not be strictly necessary.
			_ = err
		}
	}

	// Clear wiki_pages and reinsert from store.
	if _, err := idx.db.Exec("DELETE FROM wiki_pages"); err != nil {
		return fmt.Errorf("wiki index: clear pages: %w", err)
	}

	pages, err := store.List()
	if err != nil {
		return fmt.Errorf("wiki index: list pages for rebuild: %w", err)
	}

	for _, page := range pages {
		if err := idx.Upsert(page); err != nil {
			return fmt.Errorf("wiki index: rebuild upsert %q: %w", page.Path, err)
		}
	}

	return nil
}

// HasFTS5 reports whether the FTS5 extension is available.
func (idx *Index) HasFTS5() bool {
	return idx.hasFTS5
}

// encodeTags serialises a string slice as a JSON-like quoted list so that
// tag filtering via `LIKE '%"tag"%'` works correctly.
func encodeTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	quoted := make([]string, len(tags))
	for i, t := range tags {
		quoted[i] = `"` + strings.ReplaceAll(t, `"`, `\"`) + `"`
	}
	return "[" + strings.Join(quoted, ",") + "]"
}
