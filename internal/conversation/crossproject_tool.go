package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/tools"
)

// CrossProjectConversationSearchTool implements tools.Tool and searches
// conversation history across all registered projects.
type CrossProjectConversationSearchTool struct {
	searcher *CrossProjectConversationSearcher
}

// NewCrossProjectConversationSearchTool creates a tool backed by the given searcher.
func NewCrossProjectConversationSearchTool(searcher *CrossProjectConversationSearcher) *CrossProjectConversationSearchTool {
	return &CrossProjectConversationSearchTool{searcher: searcher}
}

func (t *CrossProjectConversationSearchTool) Name() string {
	return "cross_project_conversation_search"
}

func (t *CrossProjectConversationSearchTool) Description() string {
	return "Search conversation history across all configured projects. Returns matching message snippets with project names and session IDs."
}

func (t *CrossProjectConversationSearchTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"query": tools.String("Full-text search query to find across all project conversations."),
		"limit": tools.Int("Maximum number of results to return (default 10)."),
	}, []string{"query"})
}

func (t *CrossProjectConversationSearchTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
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
		return tools.SuccessResult("No conversation results found across projects."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d result(s) across projects:\n\n", len(results))
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. [%s] [%s] session:%s (%s)\n   %s\n",
			i+1, r.Project, r.Role, r.SessionID,
			r.CreatedAt.Format("2006-01-02 15:04"),
			r.Snippet)
	}
	return tools.SuccessResult(sb.String()), nil
}
