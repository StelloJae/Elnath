package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	ToolSearchName              = "tool_search"
	defaultToolSearchMaxResults = 5
	maxToolSearchResults        = 20
	toolSearchSchemaPreviewLen  = 600
)

type SchemaDeferralProvider interface {
	DeferInitialToolSchema() bool
}

func ShouldDeferToolSchema(tool Tool) bool {
	if tool == nil {
		return false
	}
	if tool.Name() == ToolSearchName {
		return false
	}
	if strings.HasPrefix(tool.Name(), "mcp_") {
		return true
	}
	if deferrable, ok := tool.(SchemaDeferralProvider); ok {
		return deferrable.DeferInitialToolSchema()
	}
	return false
}

// ToolSearchTool searches the current registry without changing which tools
// are exposed to the model. Deferred exposure is a separate runtime policy.
type ToolSearchTool struct {
	registry *Registry
}

func NewToolSearchTool(registry *Registry) *ToolSearchTool {
	return &ToolSearchTool{registry: registry}
}

func (t *ToolSearchTool) Name() string { return ToolSearchName }

func (t *ToolSearchTool) Description() string {
	return "Search registered tools by name and description"
}

func (t *ToolSearchTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"query":       String(`Search query. Use "select:<tool_name>[,<tool_name>...]" for exact selection.`),
		"max_results": Int("Maximum number of matches to return. Defaults to 5 and caps at 20."),
		"allow_names": Array("Optional exact tool-name allowlist that restricts the searchable candidate set.", "string"),
	}, []string{"query"})
}

func (t *ToolSearchTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *ToolSearchTool) Reversible() bool { return true }

func (t *ToolSearchTool) Scope(json.RawMessage) ToolScope { return ToolScope{} }

func (t *ToolSearchTool) ShouldCancelSiblingsOnError() bool { return false }

type toolSearchInput struct {
	Query      string   `json:"query"`
	MaxResults int      `json:"max_results"`
	AllowNames []string `json:"allow_names"`
}

type toolSearchOutput struct {
	Query      string            `json:"query"`
	TotalTools int               `json:"total_tools"`
	Matches    []toolSearchMatch `json:"matches"`
}

type toolSearchMatch struct {
	Name                  string `json:"name"`
	Description           string `json:"description"`
	SchemaPreview         string `json:"schema_preview"`
	Deferred              bool   `json:"deferred"`
	ConcurrencySafe       bool   `json:"concurrency_safe"`
	Reversible            bool   `json:"reversible"`
	CancelSiblingsOnError bool   `json:"cancel_siblings_on_error"`
}

type toolSearchCandidate struct {
	tool  Tool
	score int
}

func (t *ToolSearchTool) Execute(_ context.Context, params json.RawMessage) (*Result, error) {
	var input toolSearchInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}

	maxResults := normalizeToolSearchMax(input.MaxResults)
	tools := filterToolSearchAllowNames(t.searchableTools(), input.AllowNames)
	query := strings.TrimSpace(input.Query)

	var matches []toolSearchMatch
	if strings.HasPrefix(strings.ToLower(query), "select:") {
		matches = t.selectTools(tools, query, maxResults)
	} else {
		matches = t.searchTools(tools, query, maxResults)
	}

	out := toolSearchOutput{
		Query:      query,
		TotalTools: len(tools),
		Matches:    matches,
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return ErrorResult(fmt.Sprintf("tool_search: marshal output: %v", err)), nil
	}
	return SuccessResult(string(raw)), nil
}

func (t *ToolSearchTool) searchableTools() []Tool {
	if t == nil || t.registry == nil {
		return nil
	}
	all := t.registry.List()
	out := make([]Tool, 0, len(all))
	for _, tool := range all {
		if tool == nil || tool.Name() == t.Name() {
			continue
		}
		out = append(out, tool)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out
}

func (t *ToolSearchTool) selectTools(tools []Tool, query string, maxResults int) []toolSearchMatch {
	rawNames := strings.TrimSpace(query[len("select:"):])
	if rawNames == "" {
		return nil
	}

	byName := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		byName[strings.ToLower(tool.Name())] = tool
	}

	matches := make([]toolSearchMatch, 0, maxResults)
	for _, name := range strings.Split(rawNames, ",") {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if tool, ok := byName[key]; ok {
			matches = append(matches, buildToolSearchMatch(tool))
			if len(matches) >= maxResults {
				break
			}
		}
	}
	return matches
}

func (t *ToolSearchTool) searchTools(tools []Tool, query string, maxResults int) []toolSearchMatch {
	if query == "" {
		return firstToolSearchMatches(tools, maxResults)
	}

	required, optional := splitToolSearchTerms(query)
	candidates := make([]toolSearchCandidate, 0, len(tools))
	for _, tool := range tools {
		score := scoreToolSearchCandidate(tool, required, optional)
		if score <= 0 {
			continue
		}
		candidates = append(candidates, toolSearchCandidate{tool: tool, score: score})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].tool.Name() < candidates[j].tool.Name()
	})

	matches := make([]toolSearchMatch, 0, minInt(maxResults, len(candidates)))
	for _, candidate := range candidates {
		matches = append(matches, buildToolSearchMatch(candidate.tool))
		if len(matches) >= maxResults {
			break
		}
	}
	return matches
}

func firstToolSearchMatches(tools []Tool, maxResults int) []toolSearchMatch {
	matches := make([]toolSearchMatch, 0, minInt(maxResults, len(tools)))
	for _, tool := range tools {
		matches = append(matches, buildToolSearchMatch(tool))
		if len(matches) >= maxResults {
			break
		}
	}
	return matches
}

func splitToolSearchTerms(query string) (required, optional []string) {
	for _, term := range strings.Fields(strings.ToLower(query)) {
		if strings.HasPrefix(term, "+") && len(term) > 1 {
			required = append(required, strings.TrimPrefix(term, "+"))
			continue
		}
		optional = append(optional, term)
	}
	if len(required) == 0 && len(optional) == 0 {
		return nil, nil
	}
	return required, optional
}

func scoreToolSearchCandidate(tool Tool, required, optional []string) int {
	name := strings.ToLower(tool.Name())
	description := strings.ToLower(tool.Description())
	searchText := name + " " + description

	for _, term := range required {
		if !strings.Contains(searchText, term) {
			return 0
		}
	}

	score := 0
	for _, term := range append(append([]string{}, required...), optional...) {
		switch {
		case name == term:
			score += 1000
		case strings.HasPrefix(name, term):
			score += 250
		case strings.Contains(name, term):
			score += 100
		case strings.Contains(description, term):
			score += 25
		}
	}
	return score
}

func buildToolSearchMatch(tool Tool) toolSearchMatch {
	return toolSearchMatch{
		Name:                  tool.Name(),
		Description:           tool.Description(),
		SchemaPreview:         compactSchemaPreview(tool.Schema()),
		Deferred:              ShouldDeferToolSchema(tool),
		ConcurrencySafe:       tool.IsConcurrencySafe(nil),
		Reversible:            tool.Reversible(),
		CancelSiblingsOnError: tool.ShouldCancelSiblingsOnError(),
	}
}

func filterToolSearchAllowNames(tools []Tool, allowNames []string) []Tool {
	if len(allowNames) == 0 {
		return tools
	}
	allowed := make(map[string]struct{}, len(allowNames))
	for _, name := range allowNames {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			allowed[name] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return tools
	}
	out := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		if _, ok := allowed[strings.ToLower(tool.Name())]; ok {
			out = append(out, tool)
		}
	}
	return out
}

func compactSchemaPreview(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err == nil {
		raw = buf.Bytes()
	}
	preview := string(raw)
	if len(preview) > toolSearchSchemaPreviewLen {
		return preview[:toolSearchSchemaPreviewLen] + "..."
	}
	return preview
}

func normalizeToolSearchMax(maxResults int) int {
	if maxResults <= 0 {
		return defaultToolSearchMaxResults
	}
	if maxResults > maxToolSearchResults {
		return maxToolSearchResults
	}
	return maxResults
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
