package wiki

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// IssueType classifies a lint finding.
type IssueType string

const (
	IssueStale              IssueType = "stale"
	IssueOrphan             IssueType = "orphan"
	IssueContradiction      IssueType = "contradiction"
	IssueMissingFrontmatter IssueType = "missing_frontmatter"
	IssueEmpty              IssueType = "empty"
)

// Severity classifies how serious a lint issue is.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Issue represents a single lint finding for a wiki page.
type Issue struct {
	Path     string
	Type     IssueType
	Message  string
	Severity Severity
}

// Linter runs structural checks across all wiki pages.
type Linter struct {
	store *Store
	index *Index
}

// NewLinter creates a Linter backed by the given store and index.
func NewLinter(store *Store, index *Index) *Linter {
	return &Linter{store: store, index: index}
}

// Lint runs all lint checks and returns the collected issues.
func (l *Linter) Lint(ctx context.Context) ([]Issue, error) {
	pages, err := l.store.List()
	if err != nil {
		return nil, fmt.Errorf("wiki lint: list pages: %w", err)
	}

	// Collect paths linked from index.md for orphan detection.
	linkedPaths, err := l.linkedFromIndex()
	if err != nil {
		// Non-fatal: if index.md is missing, everything is an orphan.
		linkedPaths = map[string]bool{}
	}

	var issues []Issue
	now := time.Now().UTC()

	for _, page := range pages {
		if page.Path == "index.md" || page.Path == "log.md" {
			continue
		}

		// Missing frontmatter: List() already parsed successfully, so a page
		// here always has a title. We re-check the raw file for pages that
		// parsed but had incomplete metadata.
		if page.Title == "" {
			issues = append(issues, Issue{
				Path:     page.Path,
				Type:     IssueMissingFrontmatter,
				Message:  "page is missing a title in frontmatter",
				Severity: SeverityError,
			})
		}

		// Empty content check.
		if strings.TrimSpace(page.Content) == "" {
			issues = append(issues, Issue{
				Path:     page.Path,
				Type:     IssueEmpty,
				Message:  "page has no content",
				Severity: SeverityWarning,
			})
		}

		// Stale check: parse TTL and compare against Updated time.
		if page.TTL != "" {
			ttlDur, err := parseTTL(page.TTL)
			if err == nil && !page.Updated.IsZero() {
				deadline := page.Updated.Add(ttlDur)
				if now.After(deadline) {
					issues = append(issues, Issue{
						Path:     page.Path,
						Type:     IssueStale,
						Message:  fmt.Sprintf("page has not been updated within its TTL (%s); last updated %s", page.TTL, page.Updated.Format(time.RFC3339)),
						Severity: SeverityWarning,
					})
				}
			}
		}

		// Orphan check: page not referenced from index.md.
		if !linkedPaths[page.Path] {
			issues = append(issues, Issue{
				Path:     page.Path,
				Type:     IssueOrphan,
				Message:  "page is not linked from index.md",
				Severity: SeverityInfo,
			})
		}
	}

	// Also check raw .md files that failed to parse (missing frontmatter).
	rawIssues, err := l.findUnparseable()
	if err == nil {
		issues = append(issues, rawIssues...)
	}

	return issues, nil
}

// linkedFromIndex parses index.md and returns a set of linked relative paths.
func (l *Linter) linkedFromIndex() (map[string]bool, error) {
	indexPath := filepath.Join(l.store.WikiDir(), "index.md")
	f, err := os.Open(indexPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	linked := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Match markdown links: [text](path)
		start := strings.Index(line, "](")
		if start == -1 {
			continue
		}
		end := strings.Index(line[start:], ")")
		if end == -1 {
			continue
		}
		linkPath := line[start+2 : start+end]
		if linkPath != "" && !strings.HasPrefix(linkPath, "http") {
			linked[linkPath] = true
		}
	}
	return linked, scanner.Err()
}

// findUnparseable walks the wiki dir and flags .md files that cannot be parsed.
func (l *Linter) findUnparseable() ([]Issue, error) {
	var issues []Issue
	err := filepath.WalkDir(l.store.WikiDir(), func(absPath string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		relPath, err := filepath.Rel(l.store.WikiDir(), absPath)
		if err != nil {
			return nil
		}
		if relPath == "index.md" || relPath == "log.md" {
			return nil
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil
		}
		_, parseErr := ParseFrontmatter(data)
		if parseErr != nil {
			issues = append(issues, Issue{
				Path:     relPath,
				Type:     IssueMissingFrontmatter,
				Message:  fmt.Sprintf("could not parse frontmatter: %v", parseErr),
				Severity: SeverityError,
			})
		}
		return nil
	})
	return issues, err
}

// parseTTL parses a duration string like "7d", "30d", "1h" into a time.Duration.
func parseTTL(ttl string) (time.Duration, error) {
	ttl = strings.TrimSpace(ttl)
	if strings.HasSuffix(ttl, "d") {
		days := strings.TrimSuffix(ttl, "d")
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err != nil {
			return 0, fmt.Errorf("invalid TTL day value: %q", ttl)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	// Fall back to standard Go duration parsing for "1h", "30m", etc.
	return time.ParseDuration(ttl)
}
