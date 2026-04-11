package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/tools"
)

// ConversationSearchTool implements tools.Tool and searches conversation history.
type ConversationSearchTool struct {
	store *DBHistoryStore
}

// NewConversationSearchTool creates a ConversationSearchTool backed by the given store.
func NewConversationSearchTool(store *DBHistoryStore) *ConversationSearchTool {
	return &ConversationSearchTool{store: store}
}

func (t *ConversationSearchTool) Name() string { return "conversation_search" }

func (t *ConversationSearchTool) Description() string {
	return "Search past conversation history using full-text search. Returns matching message snippets with session IDs."
}

func (t *ConversationSearchTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"query": tools.String("Full-text search query to find in conversation history."),
		"limit": tools.Int("Maximum number of results to return (default 10)."),
	}, []string{"query"})
}

func (t *ConversationSearchTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *ConversationSearchTool) Reversible() bool { return true }

func (t *ConversationSearchTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *ConversationSearchTool) Scope(params json.RawMessage) tools.ToolScope {
	var input struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return tools.ConservativeScope()
	}
	return tools.ToolScope{}
}

func (t *ConversationSearchTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
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

	results, err := t.store.Search(ctx, input.Query, input.Limit)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("search failed: %v", err)), nil
	}

	if len(results) == 0 {
		return tools.SuccessResult("No matching conversation history found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d result(s):\n\n", len(results))
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. [%s] session:%s msg:%d (%s)\n   %s\n",
			i+1, r.Role, r.SessionID, r.MessageID,
			r.CreatedAt.Format("2006-01-02 15:04"),
			r.Snippet)
	}
	return tools.SuccessResult(sb.String()), nil
}
