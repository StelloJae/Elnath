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

// RebuildOption configures one invocation of Rebuild.
type RebuildOption func(*rebuildOpts)

type rebuildOpts struct {
	onProgress func(done, total int)
}

// WithRebuildProgress registers a callback invoked after each page is
// upserted. done is 1-indexed; total is the full page count. Use this to
// render progress in long-running CLI runs where Rebuild may process
// hundreds of pages.
func WithRebuildProgress(fn func(done, total int)) RebuildOption {
	return func(o *rebuildOpts) { o.onProgress = fn }
}

// Rebuild drops and recreates the FTS index, then re-indexes all pages
// from store. FTS5 rebuild SQL failures are surfaced instead of swallowed
// so callers can tell "silently broken" from "ok". The variadic opts can
// attach a progress callback without changing existing call sites.
func (idx *Index) Rebuild(store *Store, opts ...RebuildOption) error {
	var o rebuildOpts
	for _, opt := range opts {
		opt(&o)
	}

	if idx.hasFTS5 {
		if _, err := idx.db.Exec("INSERT INTO wiki_fts(wiki_fts) VALUES ('rebuild')"); err != nil {
			return fmt.Errorf("wiki index: fts5 rebuild: %w", err)
		}
	}

	if _, err := idx.db.Exec("DELETE FROM wiki_pages"); err != nil {
		return fmt.Errorf("wiki index: clear pages: %w", err)
	}

	pages, err := store.List()
	if err != nil {
		return fmt.Errorf("wiki index: list pages for rebuild: %w", err)
	}

	total := len(pages)
	for i, page := range pages {
		if err := idx.Upsert(page); err != nil {
			return fmt.Errorf("wiki index: rebuild upsert %q: %w", page.Path, err)
		}
		if o.onProgress != nil {
			o.onProgress(i+1, total)
		}
	}

	return nil
}

// HasFTS5 reports whether the FTS5 extension is available.
func (idx *Index) HasFTS5() bool {
	return idx.hasFTS5
}

// IntegrityReport describes the state of the wiki DB relative to the
// filesystem store. A non-zero Drift means search results are lying —
// call Rebuild to resync.
type IntegrityReport struct {
	HasFTS5      bool
	PagesInStore int      // .md files the Store can parse
	PagesInDB    int      // rows in wiki_pages
	RowsInFTS    int      // rows in wiki_fts; -1 when FTS5 is unavailable
	FTS5Check    string   // "ok", "n/a", or the SQL error text
	Triggers     []string // sorted wiki_pages_* triggers present
	TriggersOK   bool     // all three sync triggers present (or n/a)
	Warnings     []string // human-readable findings
}

// Drift reports whether the DB diverges from the Store: either the store
// has more/fewer pages than wiki_pages, or (with FTS5) the wiki_fts row
// count does not match.
func (r *IntegrityReport) Drift() bool {
	if r.PagesInStore != r.PagesInDB {
		return true
	}
	if r.HasFTS5 && r.RowsInFTS != r.PagesInDB {
		return true
	}
	return false
}

// CheckIntegrity inspects the wiki DB and compares it against store.
// The returned report is always non-nil on success; error is reserved for
// fatal SQL failures (schema unreachable etc.). Callers decide what to do
// with Drift() and Warnings.
func (idx *Index) CheckIntegrity(store *Store) (*IntegrityReport, error) {
	rep := &IntegrityReport{
		HasFTS5:   idx.hasFTS5,
		RowsInFTS: -1,
	}

	pages, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("wiki integrity: list store: %w", err)
	}
	rep.PagesInStore = len(pages)

	var dbCount int
	if err := idx.db.QueryRow("SELECT count(*) FROM wiki_pages").Scan(&dbCount); err != nil {
		return nil, fmt.Errorf("wiki integrity: count wiki_pages: %w", err)
	}
	rep.PagesInDB = dbCount

	if idx.hasFTS5 {
		var ftsCount int
		if err := idx.db.QueryRow("SELECT count(*) FROM wiki_fts").Scan(&ftsCount); err != nil {
			return nil, fmt.Errorf("wiki integrity: count wiki_fts: %w", err)
		}
		rep.RowsInFTS = ftsCount

		if _, err := idx.db.Exec("INSERT INTO wiki_fts(wiki_fts) VALUES ('integrity-check')"); err != nil {
			rep.FTS5Check = err.Error()
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("FTS5 integrity-check failed: %v", err))
		} else {
			rep.FTS5Check = "ok"
		}
	} else {
		rep.FTS5Check = "n/a"
	}

	triggers, err := listWikiTriggers(idx.db)
	if err != nil {
		return nil, fmt.Errorf("wiki integrity: list triggers: %w", err)
	}
	rep.Triggers = triggers
	if idx.hasFTS5 {
		required := map[string]bool{"wiki_pages_ai": false, "wiki_pages_ad": false, "wiki_pages_au": false}
		for _, name := range triggers {
			if _, ok := required[name]; ok {
				required[name] = true
			}
		}
		missing := 0
		for name, seen := range required {
			if !seen {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("trigger %q missing — file writes will not sync FTS", name))
				missing++
			}
		}
		rep.TriggersOK = missing == 0
	} else {
		rep.TriggersOK = true
	}

	if rep.Drift() {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf(
			"drift detected: store=%d, db=%d, fts=%d — run `elnath wiki reindex`",
			rep.PagesInStore, rep.PagesInDB, rep.RowsInFTS,
		))
	}

	return rep, nil
}

func listWikiTriggers(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='trigger' AND name LIKE 'wiki_pages_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
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
