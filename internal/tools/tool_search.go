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
	return ToolSchemaDeferReason(tool) != ""
}

func ToolSchemaDeferReason(tool Tool) string {
	if tool == nil {
		return ""
	}
	if tool.Name() == ToolSearchName {
		return ""
	}
	if strings.HasPrefix(tool.Name(), "mcp_") {
		return "mcp_prefix"
	}
	if deferrable, ok := tool.(SchemaDeferralProvider); ok {
		if deferrable.DeferInitialToolSchema() {
			return "tool_declared_deferred"
		}
	}
	return ""
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
		"category":    String("Optional routing category filter, such as task, schedule, skill, command, worktree, process, file, or mcp."),
		"surface":     String("Optional routing surface filter, such as daemon, scheduler, skill, runtime, worktree, process, builtin, or mcp."),
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
	Category   string   `json:"category"`
	Surface    string   `json:"surface"`
}

type toolSearchOutput struct {
	Query      string            `json:"query"`
	TotalTools int               `json:"total_tools"`
	Matches    []toolSearchMatch `json:"matches"`
	Receipt    toolSearchReceipt `json:"receipt"`
}

type toolSearchMatch struct {
	Name                  string `json:"name"`
	Description           string `json:"description"`
	Category              string `json:"category"`
	Surface               string `json:"surface"`
	SchemaPreview         string `json:"schema_preview"`
	Deferred              bool   `json:"deferred"`
	DeferReason           string `json:"defer_reason,omitempty"`
	ExecutionAvailable    bool   `json:"execution_available"`
	ExecutionPolicy       string `json:"execution_policy"`
	ModelCallable         bool   `json:"model_callable"`
	ConcurrencySafe       bool   `json:"concurrency_safe"`
	Reversible            bool   `json:"reversible"`
	CancelSiblingsOnError bool   `json:"cancel_siblings_on_error"`
}

type toolSearchReceipt struct {
	Tool               string `json:"tool"`
	Action             string `json:"action"`
	ReadOnly           bool   `json:"read_only"`
	RegistryAvailable  bool   `json:"registry_available"`
	ExecutionAvailable bool   `json:"execution_available"`
	ExecutionPolicy    string `json:"execution_policy"`
	TotalTools         int    `json:"total_tools"`
	ReturnedMatches    int    `json:"returned_matches"`
	DeferredMatches    int    `json:"deferred_matches"`
	MaxResults         int    `json:"max_results"`
	AllowNamesCount    int    `json:"allow_names_count"`
	Category           string `json:"category,omitempty"`
	Surface            string `json:"surface,omitempty"`
	Query              string `json:"query"`
}

type toolSearchCandidate struct {
	tool  Tool
	score int
}

type toolSearchRoutingMetadata struct {
	Category string
	Surface  string
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
	tools = filterToolSearchRouting(tools, input.Category, input.Surface)
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
		Receipt:    t.receipt(query, maxResults, input.AllowNames, input.Category, input.Surface, len(tools), matches),
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return ErrorResult(fmt.Sprintf("tool_search: marshal output: %v", err)), nil
	}
	return SuccessResult(string(raw)), nil
}

func (t *ToolSearchTool) receipt(query string, maxResults int, allowNames []string, category string, surface string, totalTools int, matches []toolSearchMatch) toolSearchReceipt {
	return toolSearchReceipt{
		Tool:               ToolSearchName,
		Action:             toolSearchAction(query),
		ReadOnly:           true,
		RegistryAvailable:  t != nil && t.registry != nil,
		ExecutionAvailable: false,
		ExecutionPolicy:    "metadata_only",
		TotalTools:         totalTools,
		ReturnedMatches:    len(matches),
		DeferredMatches:    countDeferredToolSearchMatches(matches),
		MaxResults:         maxResults,
		AllowNamesCount:    countNonEmptyToolSearchNames(allowNames),
		Category:           normalizeToolSearchFilter(category),
		Surface:            normalizeToolSearchFilter(surface),
		Query:              strings.TrimSpace(query),
	}
}

func toolSearchAction(query string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "select:") {
		return "select"
	}
	return "search"
}

func countDeferredToolSearchMatches(matches []toolSearchMatch) int {
	count := 0
	for _, match := range matches {
		if match.Deferred {
			count++
		}
	}
	return count
}

func countNonEmptyToolSearchNames(names []string) int {
	count := 0
	for _, name := range names {
		if strings.TrimSpace(name) != "" {
			count++
		}
	}
	return count
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
	deferReason := ToolSchemaDeferReason(tool)
	routing := toolSearchRoutingMetadataForName(tool.Name())
	return toolSearchMatch{
		Name:                  tool.Name(),
		Description:           tool.Description(),
		Category:              routing.Category,
		Surface:               routing.Surface,
		SchemaPreview:         compactSchemaPreview(tool.Schema()),
		Deferred:              deferReason != "",
		DeferReason:           deferReason,
		ExecutionAvailable:    true,
		ExecutionPolicy:       "model_tool_call",
		ModelCallable:         true,
		ConcurrencySafe:       tool.IsConcurrencySafe(nil),
		Reversible:            tool.Reversible(),
		CancelSiblingsOnError: tool.ShouldCancelSiblingsOnError(),
	}
}

func toolSearchRoutingMetadataForName(name string) toolSearchRoutingMetadata {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case name == "":
		return toolSearchRoutingMetadata{Category: "other", Surface: "registry"}
	case strings.HasPrefix(name, "mcp_"):
		return toolSearchRoutingMetadata{Category: "mcp", Surface: "mcp"}
	case strings.HasPrefix(name, "task_"):
		return toolSearchRoutingMetadata{Category: "task", Surface: "daemon"}
	case strings.HasPrefix(name, "schedule_"):
		return toolSearchRoutingMetadata{Category: "schedule", Surface: "scheduler"}
	case name == "enter_worktree" || name == "exit_worktree" || strings.HasPrefix(name, "worktree_"):
		return toolSearchRoutingMetadata{Category: "worktree", Surface: "worktree"}
	case strings.HasPrefix(name, "process_"):
		return toolSearchRoutingMetadata{Category: "process", Surface: "process"}
	case strings.HasPrefix(name, "agentic_"):
		return toolSearchRoutingMetadata{Category: "agentic", Surface: "agentic"}
	}

	switch name {
	case "read_file", "write_file", "edit_file", "glob", "grep":
		return toolSearchRoutingMetadata{Category: "file", Surface: "builtin"}
	case "bash":
		return toolSearchRoutingMetadata{Category: "shell", Surface: "builtin"}
	case "git":
		return toolSearchRoutingMetadata{Category: "version_control", Surface: "builtin"}
	case "web_fetch", "web_search":
		return toolSearchRoutingMetadata{Category: "web", Surface: "builtin"}
	case "code_symbols":
		return toolSearchRoutingMetadata{Category: "code_intelligence", Surface: "builtin"}
	case "todo_write":
		return toolSearchRoutingMetadata{Category: "plan", Surface: "builtin"}
	case "sleep":
		return toolSearchRoutingMetadata{Category: "timer", Surface: "builtin"}
	case "tool_search":
		return toolSearchRoutingMetadata{Category: "discovery", Surface: "builtin"}
	case "skill", "skill_catalog", "create_skill":
		return toolSearchRoutingMetadata{Category: "skill", Surface: "skill"}
	case "command_catalog", "runtime_command":
		return toolSearchRoutingMetadata{Category: "command", Surface: "runtime"}
	case "enter_plan_mode", "exit_plan_mode":
		return toolSearchRoutingMetadata{Category: "plan", Surface: "runtime"}
	case "ask_user_question", "user_question_answer":
		return toolSearchRoutingMetadata{Category: "user_input", Surface: "runtime"}
	default:
		return toolSearchRoutingMetadata{Category: "other", Surface: "registry"}
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

func filterToolSearchRouting(tools []Tool, category string, surface string) []Tool {
	category = normalizeToolSearchFilter(category)
	surface = normalizeToolSearchFilter(surface)
	if category == "" && surface == "" {
		return tools
	}
	out := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		routing := toolSearchRoutingMetadataForName(tool.Name())
		if category != "" && routing.Category != category {
			continue
		}
		if surface != "" && routing.Surface != surface {
			continue
		}
		out = append(out, tool)
	}
	return out
}

func normalizeToolSearchFilter(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
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
