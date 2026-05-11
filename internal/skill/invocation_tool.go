package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

type InvocationToolConfig struct {
	Registry   *Registry
	Provider   llm.Provider
	Tools      *tools.Registry
	Model      string
	Permission *agent.Permission
	Hooks      *agent.HookRegistry
	Locale     string
}

type InvocationTool struct {
	cfg InvocationToolConfig
}

func NewInvocationTool(cfg InvocationToolConfig) *InvocationTool {
	return &InvocationTool{cfg: cfg}
}

func (t *InvocationTool) Name() string { return "skill" }

func (t *InvocationTool) Description() string {
	return "Execute a registered skill by name with optional arguments"
}

func (t *InvocationTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"skill":      tools.String(`Skill name, with or without leading slash. Example: "review-pr" or "/review-pr".`),
		"args":       tools.String("Optional positional arguments passed to $ARGUMENTS and {arguments}."),
		"named_args": {Type: "object", Description: "Optional JSON object of named placeholder values for {name}-style skill prompts."},
	}, []string{"skill"})
}

func (t *InvocationTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *InvocationTool) Reversible() bool { return false }

func (t *InvocationTool) Scope(json.RawMessage) tools.ToolScope {
	return tools.ConservativeScope()
}

func (t *InvocationTool) ShouldCancelSiblingsOnError() bool { return true }

func (t *InvocationTool) DeferInitialToolSchema() bool { return true }

type invocationInput struct {
	Skill     string            `json:"skill"`
	Args      string            `json:"args"`
	NamedArgs map[string]string `json:"named_args"`
}

type invocationOutput struct {
	Skill  string `json:"skill"`
	Status string `json:"status"`
	Output string `json:"output"`
}

func (t *InvocationTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	var input invocationInput
	if err := json.Unmarshal(params, &input); err != nil {
		return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}

	name := normalizeInvocationSkillName(input.Skill)
	if err := ValidateSkillName(name); err != nil {
		return tools.ErrorResult(err.Error()), nil
	}
	if t == nil || t.cfg.Registry == nil {
		return tools.ErrorResult("skill registry is not configured"), nil
	}
	if t.cfg.Provider == nil {
		return tools.ErrorResult("skill provider is not configured"), nil
	}

	if _, ok := t.cfg.Registry.Get(name); !ok {
		return tools.ErrorResult(fmt.Sprintf("skill %q not found", name)), nil
	}

	args := cloneNamedArgs(input.NamedArgs)
	if strings.TrimSpace(input.Args) != "" {
		args["arguments"] = input.Args
		args["ARGUMENTS"] = input.Args
	}

	result, err := t.cfg.Registry.Execute(ctx, ExecuteParams{
		SkillName:  name,
		Args:       args,
		Provider:   t.cfg.Provider,
		ToolReg:    registryWithoutTool(t.cfg.Tools, t.Name()),
		Model:      t.cfg.Model,
		Sink:       event.NopSink{},
		Permission: t.cfg.Permission,
		Hooks:      t.cfg.Hooks,
		Locale:     t.cfg.Locale,
	})
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("skill %q: %v", name, err)), nil
	}

	raw, err := json.Marshal(invocationOutput{
		Skill:  name,
		Status: "completed",
		Output: result.Output,
	})
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("skill %q: marshal output: %v", name, err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

func normalizeInvocationSkillName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")
	return strings.TrimSpace(name)
}

func cloneNamedArgs(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func registryWithoutTool(reg *tools.Registry, excludedName string) *tools.Registry {
	filtered := tools.NewRegistry()
	if reg == nil {
		return filtered
	}
	for _, tool := range reg.List() {
		if tool == nil || tool.Name() == excludedName {
			continue
		}
		filtered.Register(tool)
	}
	return filtered
}
