package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Store manages wiki pages as markdown files on disk.
// All page paths are relative to wikiDir.
type Store struct {
	wikiDir string
}

// NewStore creates a Store rooted at wikiDir.
// The directory is created if it does not exist.
func NewStore(wikiDir string) (*Store, error) {
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		return nil, fmt.Errorf("wiki store: create dir %q: %w", wikiDir, err)
	}
	return &Store{wikiDir: wikiDir}, nil
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
			return nil, fmt.Errorf("wiki store: page not found: %q", path)
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
		return fmt.Errorf("wiki store: page not found: %q", page.Path)
	}

	page.Updated = time.Now().UTC()

	data, err := RenderFrontmatter(page)
	if err != nil {
		return fmt.Errorf("wiki store: render %q: %w", page.Path, err)
	}

	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return fmt.Errorf("wiki store: write %q: %w", page.Path, err)
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
			return fmt.Errorf("wiki store: page not found: %q", path)
		}
		return fmt.Errorf("wiki store: delete %q: %w", path, err)
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

// rebuildIndex walks all .md files and rewrites index.md with links to every page.
func (s *Store) rebuildIndex() error {
	pages, err := s.List()
	if err != nil {
		return fmt.Errorf("wiki store: rebuild index: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("# Wiki Index\n\n")
	sb.WriteString("_Auto-generated. Do not edit manually._\n\n")

	for _, p := range pages {
		if p.Path == "index.md" || p.Path == "log.md" {
			continue
		}
		title := p.Title
		if title == "" {
			title = p.Path
		}
		fmt.Fprintf(&sb, "- [%s](%s)\n", title, p.Path)
	}

	indexPath := filepath.Join(s.wikiDir, "index.md")
	if err := os.WriteFile(indexPath, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("wiki store: write index.md: %w", err)
	}
	return nil
}

// appendLog appends a timestamped entry to log.md.
func (s *Store) appendLog(entry string) error {
	logPath := filepath.Join(s.wikiDir, "log.md")

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("wiki store: open log.md: %w", err)
	}
	defer f.Close()

	ts := time.Now().UTC().Format(time.RFC3339)
	line := fmt.Sprintf("- %s — %s\n", ts, entry)
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("wiki store: append log.md: %w", err)
	}
	return nil
}
