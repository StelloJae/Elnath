package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/stello/elnath/internal/tools"
)

const CatalogToolName = "skill_catalog"

type CatalogTool struct {
	registry *Registry
}

func NewCatalogTool(registry *Registry) *CatalogTool {
	return &CatalogTool{registry: registry}
}

func (t *CatalogTool) Name() string { return CatalogToolName }

func (t *CatalogTool) Description() string {
	return "List or inspect registered skill metadata without executing skills"
}

func (t *CatalogTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"action":         tools.StringEnum("Catalog action.", "list", "show", "recommend", "match_paths"),
		"skill":          tools.String("Skill name for action=show."),
		"query":          tools.String("Intent or task query for action=recommend."),
		"paths":          tools.Array("File paths for action=match_paths.", "string"),
		"cwd":            tools.String("Project root used to normalize absolute paths for action=match_paths."),
		"max_results":    tools.Int("Maximum recommendations for action=recommend. Defaults to 5, max 20."),
		"include_prompt": tools.Bool("Include the full skill prompt for action=show. Defaults to false."),
		"allow_trust_levels": tools.Array(
			"Optional trust-level allowlist. Supported values: wiki, local_compatible, plugin_cache, declared.",
			"string",
		),
	}, []string{"action"})
}

func (t *CatalogTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *CatalogTool) Reversible() bool { return true }

func (t *CatalogTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *CatalogTool) ShouldCancelSiblingsOnError() bool { return false }

type catalogToolInput struct {
	Action        string   `json:"action"`
	Skill         string   `json:"skill"`
	Query         string   `json:"query"`
	Paths         []string `json:"paths"`
	CWD           string   `json:"cwd"`
	MaxResults    int      `json:"max_results"`
	IncludePrompt bool     `json:"include_prompt"`
	AllowTrust    []string `json:"allow_trust_levels"`
}

type catalogSkillEntry struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Trigger       string   `json:"trigger,omitempty"`
	RequiredTools []string `json:"required_tools,omitempty"`
	Paths         []string `json:"paths,omitempty"`
	ArgumentNames []string `json:"arguments,omitempty"`
	Model         string   `json:"model,omitempty"`
	Effort        string   `json:"effort,omitempty"`
	BaseDir       string   `json:"base_dir,omitempty"`
	Status        string   `json:"status,omitempty"`
	Source        string   `json:"source,omitempty"`
	TrustLevel    string   `json:"trust_level,omitempty"`
	External      bool     `json:"external"`
	Score         int      `json:"score,omitempty"`
	MatchedFields []string `json:"matched_fields,omitempty"`
	Prompt        string   `json:"prompt,omitempty"`
}

func (t *CatalogTool) Execute(_ context.Context, params json.RawMessage) (*tools.Result, error) {
	var input catalogToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	filter, filterErr := newSkillTrustFilter(input.AllowTrust)
	if filterErr != nil {
		return tools.ErrorResult(filterErr.Error()), nil
	}

	action := strings.ToLower(strings.TrimSpace(input.Action))
	switch action {
	case "", "list":
		return marshalSkillCatalogOutput(map[string]any{
			"action": "list",
			"skills": t.skillEntries(false, filter),
		})
	case "show":
		name := strings.TrimSpace(strings.TrimPrefix(input.Skill, "/"))
		if name == "" {
			return tools.ErrorResult("skill_catalog: skill is required for action=show"), nil
		}
		sk, ok := t.registry.Get(name)
		if !ok {
			return tools.ErrorResult(fmt.Sprintf("skill %q not found", name)), nil
		}
		if !filter.allowsSkill(sk) {
			return tools.ErrorResult(fmt.Sprintf("skill %q filtered by allow_trust_levels", name)), nil
		}
		return marshalSkillCatalogOutput(map[string]any{
			"action": "show",
			"skill":  skillCatalogEntry(sk, input.IncludePrompt),
		})
	case "recommend":
		query := strings.TrimSpace(input.Query)
		return marshalSkillCatalogOutput(map[string]any{
			"action": "recommend",
			"query":  query,
			"skills": t.recommendedSkillEntries(query, normalizeSkillCatalogMax(input.MaxResults), filter),
		})
	case "match_paths":
		return marshalSkillCatalogOutput(map[string]any{
			"action":  "match_paths",
			"matches": filter.filterMatches(t.registry.ConditionalMatchesForPaths(input.Paths, input.CWD)),
		})
	default:
		return tools.ErrorResult(fmt.Sprintf("skill_catalog: unsupported action %q; supported actions are list, show, recommend, and match_paths", input.Action)), nil
	}
}

func (t *CatalogTool) skillEntries(includePrompt bool, filter skillTrustFilter) []catalogSkillEntry {
	if t == nil || t.registry == nil {
		return nil
	}
	skills := t.registry.List()
	out := make([]catalogSkillEntry, 0, len(skills))
	for _, sk := range skills {
		if !filter.allowsSkill(sk) {
			continue
		}
		out = append(out, skillCatalogEntry(sk, includePrompt))
	}
	return out
}

func (t *CatalogTool) recommendedSkillEntries(query string, maxResults int, filter skillTrustFilter) []catalogSkillEntry {
	if t == nil || t.registry == nil {
		return nil
	}
	terms := skillCatalogQueryTerms(query)
	if len(terms) == 0 {
		return firstSkillCatalogEntries(t.registry.List(), maxResults, filter)
	}
	var matches []catalogSkillEntry
	for _, sk := range t.registry.List() {
		if !filter.allowsSkill(sk) {
			continue
		}
		entry := skillCatalogEntry(sk, false)
		entry.Score, entry.MatchedFields = scoreSkillCatalogEntry(sk, terms)
		if entry.Score > 0 {
			matches = append(matches, entry)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].Name < matches[j].Name
		}
		return matches[i].Score > matches[j].Score
	})
	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}
	return matches
}

func firstSkillCatalogEntries(skills []*Skill, maxResults int, filter skillTrustFilter) []catalogSkillEntry {
	out := make([]catalogSkillEntry, 0, min(len(skills), maxResults))
	for _, sk := range skills {
		if !filter.allowsSkill(sk) {
			continue
		}
		out = append(out, skillCatalogEntry(sk, false))
		if len(out) >= maxResults {
			break
		}
	}
	return out
}

func skillCatalogEntry(sk *Skill, includePrompt bool) catalogSkillEntry {
	if sk == nil {
		return catalogSkillEntry{}
	}
	entry := catalogSkillEntry{
		Name:          sk.Name,
		Description:   sk.Description,
		Trigger:       sk.Trigger,
		RequiredTools: append([]string(nil), sk.RequiredTools...),
		Paths:         append([]string(nil), sk.Paths...),
		ArgumentNames: append([]string(nil), sk.ArgumentNames...),
		Model:         sk.Model,
		Effort:        sk.Effort,
		BaseDir:       sk.BaseDir,
		Status:        sk.Status,
		Source:        sk.Source,
		TrustLevel:    sk.TrustLevel(),
		External:      sk.External(),
	}
	if includePrompt {
		entry.Prompt = sk.Prompt
	}
	return entry
}

func scoreSkillCatalogEntry(sk *Skill, terms []string) (int, []string) {
	if sk == nil {
		return 0, nil
	}
	fields := []struct {
		name   string
		text   string
		weight int
	}{
		{name: "name", text: sk.Name, weight: 4},
		{name: "description", text: sk.Description, weight: 3},
		{name: "trigger", text: sk.Trigger, weight: 2},
		{name: "paths", text: strings.Join(sk.Paths, " "), weight: 1},
		{name: "required_tools", text: strings.Join(sk.RequiredTools, " "), weight: 1},
		{name: "source", text: sk.Source, weight: 1},
	}
	score := 0
	seen := map[string]struct{}{}
	for _, term := range terms {
		for _, field := range fields {
			if strings.Contains(strings.ToLower(field.text), term) {
				score += field.weight
				seen[field.name] = struct{}{}
			}
		}
	}
	if len(seen) == 0 {
		return score, nil
	}
	matched := make([]string, 0, len(seen))
	for field := range seen {
		matched = append(matched, field)
	}
	sort.Strings(matched)
	return score, matched
}

func skillCatalogQueryTerms(query string) []string {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	terms := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.Trim(part, ".,;:()[]{}<>\"'`")
		if len(part) < 2 {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		terms = append(terms, part)
	}
	return terms
}

func normalizeSkillCatalogMax(maxResults int) int {
	if maxResults <= 0 {
		return 5
	}
	if maxResults > 20 {
		return 20
	}
	return maxResults
}

func marshalSkillCatalogOutput(output any) (*tools.Result, error) {
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("skill_catalog: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}
