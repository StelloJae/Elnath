package wiki

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SearchResult pairs a Page with its relevance score and highlighted snippets.
type SearchResult struct {
	Page       *Page
	Score      float64
	Highlights []string
}

// SearchOpts configures a search query.
type SearchOpts struct {
	Query string
	Tags  []string
	Type  PageType
	Limit int
}

// Search runs a hybrid search against the index.
// When FTS5 is available it uses a ranked full-text search; otherwise it falls
// back to LIKE-based matching. Results from multiple signals are merged with
// weighted scores: FTS5 rank (0.7), path match (0.2), tag match (0.1).
func (idx *Index) Search(ctx context.Context, opts SearchOpts) ([]SearchResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	if idx.hasFTS5 {
		return idx.searchFTS5(ctx, opts)
	}
	return idx.searchLike(ctx, opts)
}

// row holds raw data fetched from the DB.
type row struct {
	path       string
	title      string
	typ        string
	content    string
	tags       string
	confidence string
	ttl        string
	createdAt  string
	updatedAt  string
	ftsRank    float64
}

func (idx *Index) searchFTS5(ctx context.Context, opts SearchOpts) ([]SearchResult, error) {
	// Build a query that unions FTS5 results with path-match results, then
	// applies optional type/tag filters.
	args := []interface{}{}
	var conditions []string

	// FTS5 match on query term.
	ftsQuery := ""
	if opts.Query != "" {
		ftsQuery = opts.Query
	}

	// Base query: join wiki_pages with wiki_fts on rowid.
	baseSQL := `
SELECT p.path, p.title, p.type, p.content, p.tags, p.confidence, p.ttl,
       p.created_at, p.updated_at,
       bm25(wiki_fts) AS rank
FROM wiki_pages p
JOIN wiki_fts ON wiki_fts.rowid = p.rowid
WHERE wiki_fts MATCH ?`
	args = append(args, ftsQuery)

	if opts.Type != "" {
		conditions = append(conditions, "p.type = ?")
		args = append(args, string(opts.Type))
	}
	for _, tag := range opts.Tags {
		conditions = append(conditions, `p.tags LIKE ?`)
		args = append(args, `%"`+tag+`"%`)
	}

	if len(conditions) > 0 {
		baseSQL += " AND " + strings.Join(conditions, " AND ")
	}
	baseSQL += " ORDER BY rank LIMIT ?"
	args = append(args, opts.Limit*2) // fetch extra for merging

	rows, err := idx.db.QueryContext(ctx, baseSQL, args...)
	if err != nil {
		// FTS5 MATCH can fail on malformed queries; fall back to LIKE.
		return idx.searchLike(ctx, opts)
	}
	defer rows.Close()

	scored := map[string]*SearchResult{}

	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			continue
		}

		// bm25 returns negative values (lower = better match); normalise to 0-1.
		ftsScore := 0.0
		if r.ftsRank < 0 {
			ftsScore = 1.0 / (1.0 - r.ftsRank)
		}

		pathScore := 0.0
		if opts.Query != "" && strings.Contains(strings.ToLower(r.path), strings.ToLower(opts.Query)) {
			pathScore = 1.0
		}

		tagScore := 0.0
		if len(opts.Tags) > 0 {
			matched := 0
			for _, tag := range opts.Tags {
				if strings.Contains(r.tags, `"`+tag+`"`) {
					matched++
				}
			}
			tagScore = float64(matched) / float64(len(opts.Tags))
		}

		score := ftsScore*0.7 + pathScore*0.2 + tagScore*0.1

		if existing, ok := scored[r.path]; ok {
			if score > existing.Score {
				existing.Score = score
			}
		} else {
			page := rowToPage(r)
			scored[r.path] = &SearchResult{
				Page:       page,
				Score:      score,
				Highlights: extractHighlights(r.content, opts.Query),
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("wiki search: fts5 rows: %w", err)
	}

	// Also include pure path matches not caught by FTS5.
	if opts.Query != "" {
		pathResults, err := idx.pathMatch(ctx, opts)
		if err == nil {
			for _, pr := range pathResults {
				if _, ok := scored[pr.Page.Path]; !ok {
					scored[pr.Page.Path] = pr
				}
			}
		}
	}

	return rankAndLimit(scored, opts.Limit), nil
}

func (idx *Index) searchLike(ctx context.Context, opts SearchOpts) ([]SearchResult, error) {
	var conditions []string
	var args []interface{}

	if opts.Query != "" {
		like := "%" + opts.Query + "%"
		conditions = append(conditions, "(content LIKE ? OR title LIKE ? OR path LIKE ?)")
		args = append(args, like, like, like)
	}
	if opts.Type != "" {
		conditions = append(conditions, "type = ?")
		args = append(args, string(opts.Type))
	}
	for _, tag := range opts.Tags {
		conditions = append(conditions, `tags LIKE ?`)
		args = append(args, `%"`+tag+`"%`)
	}

	query := `SELECT path, title, type, content, tags, confidence, ttl, created_at, updated_at, 0.0 AS rank FROM wiki_pages`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " LIMIT ?"
	args = append(args, opts.Limit)

	rows, err := idx.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("wiki search: like query: %w", err)
	}
	defer rows.Close()

	scored := map[string]*SearchResult{}
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			continue
		}
		pathScore := 0.0
		if opts.Query != "" && strings.Contains(strings.ToLower(r.path), strings.ToLower(opts.Query)) {
			pathScore = 1.0
		}
		tagScore := 0.0
		if len(opts.Tags) > 0 {
			matched := 0
			for _, tag := range opts.Tags {
				if strings.Contains(r.tags, `"`+tag+`"`) {
					matched++
				}
			}
			tagScore = float64(matched) / float64(len(opts.Tags))
		}
		score := 0.7 + pathScore*0.2 + tagScore*0.1
		scored[r.path] = &SearchResult{
			Page:       rowToPage(r),
			Score:      score,
			Highlights: extractHighlights(r.content, opts.Query),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("wiki search: like rows: %w", err)
	}

	return rankAndLimit(scored, opts.Limit), nil
}

// pathMatch finds pages whose path contains the query string.
func (idx *Index) pathMatch(ctx context.Context, opts SearchOpts) ([]*SearchResult, error) {
	like := "%" + opts.Query + "%"
	rows, err := idx.db.QueryContext(ctx,
		`SELECT path, title, type, content, tags, confidence, ttl, created_at, updated_at, 0.0 AS rank
         FROM wiki_pages WHERE path LIKE ? LIMIT ?`,
		like, opts.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*SearchResult
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			continue
		}
		results = append(results, &SearchResult{
			Page:  rowToPage(r),
			Score: 0.2,
		})
	}
	return results, rows.Err()
}

// scanRow reads one wiki_pages row (with rank column) into a row struct.
func scanRow(rows interface {
	Scan(...interface{}) error
}) (row, error) {
	var r row
	err := rows.Scan(
		&r.path, &r.title, &r.typ, &r.content, &r.tags,
		&r.confidence, &r.ttl, &r.createdAt, &r.updatedAt, &r.ftsRank,
	)
	return r, err
}

// rowToPage converts a DB row into a Page.
func rowToPage(r row) *Page {
	page := &Page{
		Path:       r.path,
		Title:      r.title,
		Type:       PageType(r.typ),
		Content:    r.content,
		Confidence: r.confidence,
		TTL:        r.ttl,
	}
	page.Tags = decodeTags(r.tags)

	if t, err := time.Parse(time.RFC3339, r.createdAt); err == nil {
		page.Created = t
	}
	if t, err := time.Parse(time.RFC3339, r.updatedAt); err == nil {
		page.Updated = t
	}
	return page
}

// decodeTags reverses encodeTags.
func decodeTags(encoded string) []string {
	if encoded == "" {
		return nil
	}
	encoded = strings.TrimPrefix(encoded, "[")
	encoded = strings.TrimSuffix(encoded, "]")
	if encoded == "" {
		return nil
	}
	parts := strings.Split(encoded, ",")
	tags := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"`)
		if p != "" {
			tags = append(tags, p)
		}
	}
	return tags
}

// extractHighlights returns short snippets from content that contain the query.
func extractHighlights(content, query string) []string {
	if query == "" {
		return nil
	}
	lower := strings.ToLower(content)
	lowerQ := strings.ToLower(query)

	var highlights []string
	start := 0
	for len(highlights) < 3 {
		idx := strings.Index(lower[start:], lowerQ)
		if idx == -1 {
			break
		}
		abs := start + idx
		lo := abs - 60
		if lo < 0 {
			lo = 0
		}
		hi := abs + len(query) + 60
		if hi > len(content) {
			hi = len(content)
		}
		snippet := strings.TrimSpace(content[lo:hi])
		highlights = append(highlights, "..."+snippet+"...")
		start = abs + len(query)
	}
	return highlights
}

// rankAndLimit sorts results by score descending and returns at most limit items.
func rankAndLimit(scored map[string]*SearchResult, limit int) []SearchResult {
	results := make([]SearchResult, 0, len(scored))
	for _, r := range scored {
		results = append(results, *r)
	}

	// Simple insertion sort (result sets are small).
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}
	return results
}
