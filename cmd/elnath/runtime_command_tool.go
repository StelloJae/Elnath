package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/stello/elnath/internal/skill"
	"github.com/stello/elnath/internal/tools"
)

const commandCatalogToolName = "command_catalog"

type commandCatalogTool struct {
	skillReg *skill.Registry
}

func newCommandCatalogTool(registries ...*skill.Registry) *commandCatalogTool {
	var skillReg *skill.Registry
	if len(registries) > 0 {
		skillReg = registries[0]
	}
	return &commandCatalogTool{skillReg: skillReg}
}

func (t *commandCatalogTool) Name() string { return commandCatalogToolName }

func (t *commandCatalogTool) Description() string {
	return "List or inspect Elnath command metadata without executing commands"
}

func (t *commandCatalogTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"action":         tools.StringEnum("Catalog action. This tool never executes commands.", "list", "show", "recommend"),
		"command":        tools.String("Command name for action=show."),
		"include_hidden": tools.Bool("Include hidden internal commands. Defaults to false."),
		"query":          tools.String("Intent or task query for action=recommend."),
		"max_results":    tools.Int("Maximum recommendations for action=recommend. Defaults to 5, max 20."),
	}, []string{"action"})
}

func (t *commandCatalogTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *commandCatalogTool) Reversible() bool { return true }

func (t *commandCatalogTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *commandCatalogTool) ShouldCancelSiblingsOnError() bool { return false }

type commandCatalogToolInput struct {
	Action        string `json:"action"`
	Command       string `json:"command"`
	IncludeHidden bool   `json:"include_hidden"`
	Query         string `json:"query"`
	MaxResults    int    `json:"max_results"`
}

type commandCatalogRecommendation struct {
	commandCatalogEntry
	Score         int      `json:"score,omitempty"`
	MatchedFields []string `json:"matched_fields,omitempty"`
}

type commandCatalogToolReceipt struct {
	Tool                  string `json:"tool"`
	Action                string `json:"action"`
	ReadOnly              bool   `json:"read_only"`
	RegistryAvailable     bool   `json:"registry_available"`
	ExecutionAvailable    bool   `json:"execution_available"`
	ExecutionPolicy       string `json:"execution_policy"`
	TotalCommands         int    `json:"total_commands"`
	ReturnedCommands      int    `json:"returned_commands"`
	ExecutableCommands    int    `json:"executable_commands,omitempty"`
	ModelCallableCommands int    `json:"model_callable_commands,omitempty"`
	IncludeHidden         bool   `json:"include_hidden"`
	MaxResults            int    `json:"max_results,omitempty"`
	Query                 string `json:"query,omitempty"`
	Command               string `json:"command,omitempty"`
	FollowupTool          string `json:"followup_tool,omitempty"`
}

func (t *commandCatalogTool) Execute(_ context.Context, params json.RawMessage) (*tools.Result, error) {
	var input commandCatalogToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}

	action := strings.ToLower(strings.TrimSpace(input.Action))
	switch action {
	case "", "list":
		commands := runtimeCommandCatalogWithSkills(t.skillReg, input.IncludeHidden)
		return marshalCommandCatalogToolOutput(map[string]any{
			"action":   "list",
			"commands": commands,
			"receipt":  commandCatalogReceipt("list", input.IncludeHidden, commands, len(commands), 0, "", ""),
		})
	case "show":
		name := strings.TrimSpace(input.Command)
		if name == "" {
			return tools.ErrorResult("command_catalog: command is required for action=show"), nil
		}
		entry, ok := findCommandCatalogEntry(name, input.IncludeHidden, t.skillReg)
		if !ok {
			return tools.ErrorResult(fmt.Sprintf("command_catalog: command %q not found", name)), nil
		}
		commands := runtimeCommandCatalogWithSkills(t.skillReg, input.IncludeHidden)
		receipt := commandCatalogReceipt("show", input.IncludeHidden, commands, 1, 0, "", entry.Name)
		receipt.FollowupTool = commandCatalogEntryFollowupTool(entry)
		return marshalCommandCatalogToolOutput(map[string]any{
			"action":  "show",
			"command": entry,
			"receipt": receipt,
		})
	case "recommend":
		query := strings.TrimSpace(input.Query)
		maxResults := normalizeCommandCatalogMax(input.MaxResults)
		commands := runtimeCommandCatalogWithSkills(t.skillReg, input.IncludeHidden)
		recommendations := recommendedCommandCatalogEntriesFromCatalog(commands, query, maxResults)
		receipt := commandCatalogReceipt("recommend", input.IncludeHidden, commands, len(recommendations), maxResults, query, "")
		receipt.FollowupTool = commandCatalogRecommendationsFollowupTool(recommendations)
		return marshalCommandCatalogToolOutput(map[string]any{
			"action":   "recommend",
			"query":    query,
			"commands": recommendations,
			"receipt":  receipt,
		})
	default:
		return tools.ErrorResult(fmt.Sprintf("command_catalog: unsupported action %q; supported actions are list, show, and recommend", input.Action)), nil
	}
}

func commandCatalogReceipt(action string, includeHidden bool, commands []commandCatalogEntry, returnedCommands, maxResults int, query, command string) commandCatalogToolReceipt {
	return commandCatalogToolReceipt{
		Tool:                  commandCatalogToolName,
		Action:                action,
		ReadOnly:              true,
		RegistryAvailable:     true,
		ExecutionAvailable:    false,
		ExecutionPolicy:       "metadata_only",
		TotalCommands:         len(commands),
		ReturnedCommands:      returnedCommands,
		ExecutableCommands:    countCommandCatalogExecutable(commands),
		ModelCallableCommands: countCommandCatalogModelCallable(commands),
		IncludeHidden:         includeHidden,
		MaxResults:            maxResults,
		Query:                 strings.TrimSpace(query),
		Command:               strings.TrimSpace(command),
	}
}

func commandCatalogEntryFollowupTool(entry commandCatalogEntry) string {
	if !entry.ModelCallable {
		return ""
	}
	switch entry.Surface {
	case "skill_slash":
		return "skill"
	case "runtime_slash":
		return runtimeCommandToolName
	default:
		return ""
	}
}

func commandCatalogRecommendationsFollowupTool(recommendations []commandCatalogRecommendation) string {
	var followup string
	for _, rec := range recommendations {
		next := commandCatalogEntryFollowupTool(rec.commandCatalogEntry)
		if next == "" {
			continue
		}
		if followup == "" {
			followup = next
			continue
		}
		if followup != next {
			return ""
		}
	}
	return followup
}

func countCommandCatalogExecutable(commands []commandCatalogEntry) int {
	count := 0
	for _, entry := range commands {
		if entry.ExecutionAvailable {
			count++
		}
	}
	return count
}

func countCommandCatalogModelCallable(commands []commandCatalogEntry) int {
	count := 0
	for _, entry := range commands {
		if entry.ModelCallable {
			count++
		}
	}
	return count
}

func findCommandCatalogEntry(name string, includeHidden bool, skillReg ...*skill.Registry) (commandCatalogEntry, bool) {
	var reg *skill.Registry
	if len(skillReg) > 0 {
		reg = skillReg[0]
	}
	for _, entry := range runtimeCommandCatalogWithSkills(reg, includeHidden) {
		if entry.Name == name {
			return entry, true
		}
		for _, alias := range entry.Aliases {
			if alias == name {
				return entry, true
			}
		}
	}
	return commandCatalogEntry{}, false
}

func recommendedCommandCatalogEntries(query string, includeHidden bool, maxResults int, skillReg ...*skill.Registry) []commandCatalogRecommendation {
	var reg *skill.Registry
	if len(skillReg) > 0 {
		reg = skillReg[0]
	}
	commands := runtimeCommandCatalogWithSkills(reg, includeHidden)
	return recommendedCommandCatalogEntriesFromCatalog(commands, query, maxResults)
}

func recommendedCommandCatalogEntriesFromCatalog(commands []commandCatalogEntry, query string, maxResults int) []commandCatalogRecommendation {
	terms := commandCatalogQueryTerms(query)
	if len(terms) == 0 {
		if len(commands) > maxResults {
			commands = commands[:maxResults]
		}
		out := make([]commandCatalogRecommendation, 0, len(commands))
		for _, entry := range commands {
			out = append(out, commandCatalogRecommendation{commandCatalogEntry: entry})
		}
		return out
	}

	var matches []commandCatalogRecommendation
	for _, entry := range commands {
		score, fields := scoreCommandCatalogEntry(entry, terms)
		if score == 0 {
			continue
		}
		matches = append(matches, commandCatalogRecommendation{
			commandCatalogEntry: entry,
			Score:               score,
			MatchedFields:       fields,
		})
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

func scoreCommandCatalogEntry(entry commandCatalogEntry, terms []string) (int, []string) {
	fields := []struct {
		name   string
		text   string
		weight int
	}{
		{name: "name", text: entry.Name, weight: 4},
		{name: "description", text: entry.Description, weight: 3},
		{name: "category", text: entry.Category, weight: 2},
		{name: "aliases", text: strings.Join(entry.Aliases, " "), weight: 1},
		{name: "argument_hint", text: entry.ArgumentHint, weight: 1},
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

func commandCatalogQueryTerms(query string) []string {
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

func normalizeCommandCatalogMax(maxResults int) int {
	if maxResults <= 0 {
		return 5
	}
	if maxResults > 20 {
		return 20
	}
	return maxResults
}

func marshalCommandCatalogToolOutput(output any) (*tools.Result, error) {
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("command_catalog: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}
