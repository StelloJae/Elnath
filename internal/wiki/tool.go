package wiki

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/tools"
)

// WikiSearchTool implements tools.Tool and searches the wiki index.
type WikiSearchTool struct {
	index *Index
}

// NewWikiSearchTool creates a WikiSearchTool backed by the given index.
func NewWikiSearchTool(index *Index) *WikiSearchTool {
	return &WikiSearchTool{index: index}
}

func (t *WikiSearchTool) Name() string { return "wiki_search" }

func (t *WikiSearchTool) Description() string {
	return "Search the wiki knowledge base. Returns matching pages with relevance scores."
}

func (t *WikiSearchTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Full-text search query"
    },
    "tags": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Filter by tags (optional)"
    },
    "type": {
      "type": "string",
      "enum": ["entity", "concept", "source", "analysis", "map"],
      "description": "Filter by page type (optional)"
    },
    "limit": {
      "type": "integer",
      "description": "Maximum number of results (default 10)"
    }
  },
  "required": ["query"]
}`)
}

func (t *WikiSearchTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	var input struct {
		Query string   `json:"query"`
		Tags  []string `json:"tags"`
		Type  PageType `json:"type"`
		Limit int      `json:"limit"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	if input.Limit <= 0 {
		input.Limit = 10
	}

	results, err := t.index.Search(ctx, SearchOpts{
		Query: input.Query,
		Tags:  input.Tags,
		Type:  input.Type,
		Limit: input.Limit,
	})
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("search failed: %v", err)), nil
	}

	if len(results) == 0 {
		return tools.SuccessResult("No results found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d result(s):\n\n", len(results))
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. [%.2f] %s (%s)\n   Path: %s\n",
			i+1, r.Score, r.Page.Title, r.Page.Type, r.Page.Path)
		for _, h := range r.Highlights {
			fmt.Fprintf(&sb, "   > %s\n", h)
		}
	}
	return tools.SuccessResult(sb.String()), nil
}

// WikiReadTool implements tools.Tool and reads a single wiki page.
type WikiReadTool struct {
	store *Store
}

// NewWikiReadTool creates a WikiReadTool backed by the given store.
func NewWikiReadTool(store *Store) *WikiReadTool {
	return &WikiReadTool{store: store}
}

func (t *WikiReadTool) Name() string { return "wiki_read" }

func (t *WikiReadTool) Description() string {
	return "Read a wiki page by its relative path. Returns the full content including metadata."
}

func (t *WikiReadTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Relative path of the wiki page (e.g. 'entities/foo.md')"
    }
  },
  "required": ["path"]
}`)
}

func (t *WikiReadTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	if input.Path == "" {
		return tools.ErrorResult("path is required"), nil
	}

	page, err := t.store.Read(input.Path)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("read failed: %v", err)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\n", page.Title)
	fmt.Fprintf(&sb, "**Type:** %s | **Tags:** %s | **Confidence:** %s\n",
		page.Type, strings.Join(page.Tags, ", "), page.Confidence)
	fmt.Fprintf(&sb, "**Created:** %s | **Updated:** %s\n\n",
		page.Created.Format(time.RFC3339), page.Updated.Format(time.RFC3339))
	sb.WriteString(page.Content)

	return tools.SuccessResult(sb.String()), nil
}

// WikiWriteTool implements tools.Tool and creates or updates a wiki page.
type WikiWriteTool struct {
	store *Store
}

// NewWikiWriteTool creates a WikiWriteTool backed by the given store.
func NewWikiWriteTool(store *Store) *WikiWriteTool {
	return &WikiWriteTool{store: store}
}

func (t *WikiWriteTool) Name() string { return "wiki_write" }

func (t *WikiWriteTool) Description() string {
	return "Create or update a wiki page. Existing pages are overwritten; new pages are created."
}

func (t *WikiWriteTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Relative path for the wiki page (e.g. 'concepts/foo.md')"
    },
    "title": {
      "type": "string",
      "description": "Page title"
    },
    "type": {
      "type": "string",
      "enum": ["entity", "concept", "source", "analysis", "map"],
      "description": "Page type"
    },
    "content": {
      "type": "string",
      "description": "Markdown body content"
    },
    "tags": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Tags for the page"
    },
    "confidence": {
      "type": "string",
      "enum": ["high", "medium", "low"],
      "description": "Confidence level of the information"
    },
    "ttl": {
      "type": "string",
      "description": "Time-to-live (e.g. '7d', '30d', '' for permanent)"
    }
  },
  "required": ["path", "title", "content"]
}`)
}

func (t *WikiWriteTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	var input struct {
		Path       string   `json:"path"`
		Title      string   `json:"title"`
		Type       PageType `json:"type"`
		Content    string   `json:"content"`
		Tags       []string `json:"tags"`
		Confidence string   `json:"confidence"`
		TTL        string   `json:"ttl"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	if input.Path == "" || input.Title == "" {
		return tools.ErrorResult("path and title are required"), nil
	}
	if input.Type == "" {
		input.Type = PageTypeConcept
	}

	page := &Page{
		Path:       input.Path,
		Title:      input.Title,
		Type:       input.Type,
		Content:    input.Content,
		Tags:       input.Tags,
		Confidence: input.Confidence,
		TTL:        input.TTL,
	}

	if err := t.store.Upsert(page); err != nil {
		return tools.ErrorResult(fmt.Sprintf("write failed: %v", err)), nil
	}

	return tools.SuccessResult(fmt.Sprintf("Page written: %s", input.Path)), nil
}
