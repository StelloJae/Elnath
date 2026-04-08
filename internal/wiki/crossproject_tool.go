package wiki

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/tools"
)

// CrossProjectSearchTool implements tools.Tool and searches across all registered project wikis.
type CrossProjectSearchTool struct {
	searcher *CrossProjectSearcher
}

// NewCrossProjectSearchTool creates a CrossProjectSearchTool backed by the given searcher.
func NewCrossProjectSearchTool(searcher *CrossProjectSearcher) *CrossProjectSearchTool {
	return &CrossProjectSearchTool{searcher: searcher}
}

func (t *CrossProjectSearchTool) Name() string { return "cross_project_search" }

func (t *CrossProjectSearchTool) Description() string {
	return "Search wiki knowledge bases across all configured projects. Returns matching pages with project names and relevance scores."
}

func (t *CrossProjectSearchTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"query": tools.String("Full-text search query to find across all project wikis."),
		"limit": tools.Int("Maximum number of results to return (default 10)."),
	}, []string{"query"})
}

func (t *CrossProjectSearchTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	var input struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	if strings.TrimSpace(input.Query) == "" {
		return tools.ErrorResult("query must not be empty"), nil
	}
	if input.Limit <= 0 {
		input.Limit = 10
	}

	results, err := t.searcher.Search(ctx, input.Query, input.Limit)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("search failed: %v", err)), nil
	}

	if len(results) == 0 {
		return tools.SuccessResult("No results found across projects."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d result(s) across projects:\n\n", len(results))
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. [%s] [%.2f] %s (%s)\n   Path: %s\n",
			i+1, r.Project, r.Score, r.Page.Title, r.Page.Type, r.Page.Path)
		for _, h := range r.Highlights {
			fmt.Fprintf(&sb, "   > %s\n", h)
		}
	}
	return tools.SuccessResult(sb.String()), nil
}
