package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stello/elnath/internal/userfacingerr"
)

// Store manages wiki pages as markdown files on disk.
// All page paths are relative to wikiDir.
type Store struct {
	wikiDir string
	index   indexSync
}

// indexSync is the subset of *Index that Store needs to keep the filesystem
// and the SQLite wiki_pages table in step. Declared as an interface so
// Store stays decoupled from Index for testing, even within the same
// package.
type indexSync interface {
	Upsert(page *Page) error
	Remove(path string) error
}

// StoreOption configures a Store during NewStore.
type StoreOption func(*Store)

// WithIndex wires an indexSync (typically *Index) so Create/Update/Delete/
// Upsert mirror their filesystem mutations into the wiki DB (and FTS5 via
// triggers). Without this option, Store only writes .md files, which means
// search drifts out of sync until a manual reindex.
//
// When an index sync fails the file write is NOT rolled back; the error is
// returned so callers can decide what to do. Run `elnath wiki integrity`
// to detect any drift that slips through.
func WithIndex(idx indexSync) StoreOption {
	return func(s *Store) { s.index = idx }
}

// NewStore creates a Store rooted at wikiDir.
// The directory is created if it does not exist.
func NewStore(wikiDir string, opts ...StoreOption) (*Store, error) {
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		return nil, fmt.Errorf("wiki store: create dir %q: %w", wikiDir, err)
	}
	s := &Store{wikiDir: wikiDir}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// absPath resolves a relative page path to an absolute filesystem path.
// Returns an error if the resolved path escapes the wiki directory.
func (s *Store) absPath(relPath string) (string, error) {
	abs := filepath.Join(s.wikiDir, relPath)
	rel, err := filepath.Rel(s.wikiDir, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("wiki store: path %q escapes wiki directory", relPath)
	}
	return abs, nil
}

// Create writes a new wiki page to disk. Returns an error if the file already exists.
func (s *Store) Create(page *Page) error {
	if page.Path == "" {
		return fmt.Errorf("wiki store: page path must not be empty")
	}

	abs, err := s.absPath(page.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("wiki store: create parent dirs for %q: %w", page.Path, err)
	}

	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("wiki store: page already exists: %q", page.Path)
	}

	now := time.Now().UTC()
	page.Created = now
	page.Updated = now

	data, err := RenderFrontmatter(page)
	if err != nil {
		return fmt.Errorf("wiki store: render %q: %w", page.Path, err)
	}

	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return fmt.Errorf("wiki store: write %q: %w", page.Path, err)
	}
	if s.index != nil {
		if err := s.index.Upsert(page); err != nil {
			return fmt.Errorf("wiki store: sync index for %q: %w", page.Path, err)
		}
	}
	return nil
}

// Read parses and returns the wiki page at relPath.
func (s *Store) Read(path string) (*Page, error) {
	abs, err := s.absPath(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			inner := fmt.Errorf("wiki store: page not found: %q", path)
			return nil, userfacingerr.Wrap(userfacingerr.ELN100, inner, "wiki read")
		}
		return nil, fmt.Errorf("wiki store: read %q: %w", path, err)
	}

	page, err := ParseFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("wiki store: parse %q: %w", path, err)
	}
	page.Path = path
	return page, nil
}

// Update overwrites an existing wiki page, updating its Updated timestamp.
func (s *Store) Update(page *Page) error {
	if page.Path == "" {
		return fmt.Errorf("wiki store: page path must not be empty")
	}

	abs, err := s.absPath(page.Path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		inner := fmt.Errorf("wiki store: page not found: %q", page.Path)
		return userfacingerr.Wrap(userfacingerr.ELN100, inner, "wiki update")
	}

	page.Updated = time.Now().UTC()

	data, err := RenderFrontmatter(page)
	if err != nil {
		return fmt.Errorf("wiki store: render %q: %w", page.Path, err)
	}

	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return fmt.Errorf("wiki store: write %q: %w", page.Path, err)
	}
	if s.index != nil {
		if err := s.index.Upsert(page); err != nil {
			return fmt.Errorf("wiki store: sync index for %q: %w", page.Path, err)
		}
	}
	return nil
}

// Delete removes the wiki page file at relPath.
func (s *Store) Delete(path string) error {
	abs, err := s.absPath(path)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil {
		if os.IsNotExist(err) {
			inner := fmt.Errorf("wiki store: page not found: %q", path)
			return userfacingerr.Wrap(userfacingerr.ELN100, inner, "wiki delete")
		}
		return fmt.Errorf("wiki store: delete %q: %w", path, err)
	}
	if s.index != nil {
		if err := s.index.Remove(path); err != nil {
			return fmt.Errorf("wiki store: sync index remove %q: %w", path, err)
		}
	}
	return nil
}

// List walks wikiDir and returns all parsed .md pages.
// Files that fail to parse are skipped with a warning (non-fatal).
func (s *Store) List() ([]*Page, error) {
	var pages []*Page

	err := filepath.WalkDir(s.wikiDir, func(absPath string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		relPath, relErr := filepath.Rel(s.wikiDir, absPath)
		if relErr != nil {
			return nil // skip unparseable paths
		}

		page, parseErr := s.Read(relPath)
		if parseErr != nil {
			// Non-fatal: malformed pages are excluded from listing.
			return nil
		}
		pages = append(pages, page)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("wiki store: list pages: %w", err)
	}

	return pages, nil
}

// Upsert creates the page if it does not exist, or updates it if it does.
func (s *Store) Upsert(page *Page) error {
	abs, err := s.absPath(page.Path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		return s.Create(page)
	}
	return s.Update(page)
}

// WikiDir returns the root wiki directory path.
func (s *Store) WikiDir() string {
	return s.wikiDir
}
