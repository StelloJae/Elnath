package skill

import (
	"context"
	"encoding/json"
	"fmt"
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
		"action":         tools.StringEnum("Catalog action.", "list", "show"),
		"skill":          tools.String("Skill name for action=show."),
		"include_prompt": tools.Bool("Include the full skill prompt for action=show. Defaults to false."),
	}, []string{"action"})
}

func (t *CatalogTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *CatalogTool) Reversible() bool { return true }

func (t *CatalogTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *CatalogTool) ShouldCancelSiblingsOnError() bool { return false }

type catalogToolInput struct {
	Action        string `json:"action"`
	Skill         string `json:"skill"`
	IncludePrompt bool   `json:"include_prompt"`
}

type catalogSkillEntry struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Trigger       string   `json:"trigger,omitempty"`
	RequiredTools []string `json:"required_tools,omitempty"`
	Model         string   `json:"model,omitempty"`
	Effort        string   `json:"effort,omitempty"`
	Status        string   `json:"status,omitempty"`
	Source        string   `json:"source,omitempty"`
	Prompt        string   `json:"prompt,omitempty"`
}

func (t *CatalogTool) Execute(_ context.Context, params json.RawMessage) (*tools.Result, error) {
	var input catalogToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}

	action := strings.ToLower(strings.TrimSpace(input.Action))
	switch action {
	case "", "list":
		return marshalSkillCatalogOutput(map[string]any{
			"action": "list",
			"skills": t.skillEntries(false),
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
		return marshalSkillCatalogOutput(map[string]any{
			"action": "show",
			"skill":  skillCatalogEntry(sk, input.IncludePrompt),
		})
	default:
		return tools.ErrorResult(fmt.Sprintf("skill_catalog: unsupported action %q; supported actions are list and show", input.Action)), nil
	}
}

func (t *CatalogTool) skillEntries(includePrompt bool) []catalogSkillEntry {
	if t == nil || t.registry == nil {
		return nil
	}
	skills := t.registry.List()
	out := make([]catalogSkillEntry, 0, len(skills))
	for _, sk := range skills {
		out = append(out, skillCatalogEntry(sk, includePrompt))
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
		Model:         sk.Model,
		Effort:        sk.Effort,
		Status:        sk.Status,
		Source:        sk.Source,
	}
	if includePrompt {
		entry.Prompt = sk.Prompt
	}
	return entry
}

func marshalSkillCatalogOutput(output any) (*tools.Result, error) {
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("skill_catalog: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}
