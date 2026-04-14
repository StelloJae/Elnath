package prompt

import (
	"context"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/wiki"
)

const memoryContextQuery = "session summary memory context"

var memoryContextFallbackQueries = []wiki.SearchOpts{
	{Query: memoryContextQuery, Tags: []string{"session"}, Type: wiki.PageTypeSource},
	{Query: "session summary", Tags: []string{"session"}, Type: wiki.PageTypeSource},
	{Query: memoryContextQuery},
}

type MemoryContextNode struct {
	priority   int
	maxEntries int
	maxChars   int
}

func NewMemoryContextNode(priority, maxEntries, maxChars int) *MemoryContextNode {
	if maxEntries <= 0 {
		maxEntries = 5
	}
	if maxChars <= 0 {
		maxChars = 1200
	}
	return &MemoryContextNode{priority: priority, maxEntries: maxEntries, maxChars: maxChars}
}

func (n *MemoryContextNode) Name() string {
	return "memory_context"
}

func (n *MemoryContextNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *MemoryContextNode) Render(ctx context.Context, state *RenderState) (string, error) {
	if n == nil || state == nil || state.BenchmarkMode || state.WikiIdx == nil {
		return "", nil
	}

	results, err := searchMemoryContextResults(ctx, state.WikiIdx, n.maxEntries)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", nil
	}

	body := buildMemoryContextBody(results, n.maxEntries)
	body = truncateString(body, n.maxChars)
	if strings.TrimSpace(body) == "" {
		return "", nil
	}

	return fmt.Sprintf("<<memory_context>>\n%s\n<</memory_context>>", body), nil
}

func searchMemoryContextResults(ctx context.Context, idx *wiki.Index, limit int) ([]wiki.SearchResult, error) {
	for _, opts := range memoryContextFallbackQueries {
		opts.Limit = limit
		results, err := idx.Search(ctx, opts)
		if err != nil {
			return nil, err
		}
		if len(results) > 0 {
			return results, nil
		}
	}
	return nil, nil
}

func buildMemoryContextBody(results []wiki.SearchResult, maxEntries int) string {
	if len(results) > maxEntries {
		results = results[:maxEntries]
	}

	var b strings.Builder
	for _, result := range results {
		if result.Page == nil {
			continue
		}
		snippet := memoryContextSnippet(result)
		if strings.TrimSpace(snippet) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%s]\n%s", result.Page.Title, snippet)
	}
	return b.String()
}

func memoryContextSnippet(result wiki.SearchResult) string {
	for _, highlight := range result.Highlights {
		highlight = strings.TrimSpace(highlight)
		if highlight != "" {
			return highlight
		}
	}
	if result.Page == nil {
		return ""
	}
	return strings.TrimSpace(result.Page.Content)
}
