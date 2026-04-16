package skill

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/locale"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

type Registry struct {
	skills map[string]*Skill
}

func NewRegistry() *Registry {
	return &Registry{skills: make(map[string]*Skill)}
}

func (r *Registry) Add(s *Skill) {
	if r == nil || s == nil || s.Name == "" {
		return
	}
	if r.skills == nil {
		r.skills = make(map[string]*Skill)
	}
	r.skills[s.Name] = s
}

func (r *Registry) Load(store *wiki.Store) error {
	pages, err := store.List()
	if err != nil {
		return err
	}

	if r.skills == nil {
		r.skills = make(map[string]*Skill)
	}

	for _, page := range pages {
		skill := FromPage(page)
		if skill == nil {
			continue
		}
		if skill.Status == "draft" {
			continue
		}
		if _, exists := r.skills[skill.Name]; exists {
			slog.Warn("duplicate skill definition", "name", skill.Name, "path", page.Path)
		}
		r.skills[skill.Name] = skill
	}

	return nil
}

func (r *Registry) Get(name string) (*Skill, bool) {
	if r == nil {
		return nil, false
	}
	skill, ok := r.skills[name]
	return skill, ok
}

func (r *Registry) List() []*Skill {
	if r == nil {
		return nil
	}

	out := make([]*Skill, 0, len(r.skills))
	for _, skill := range r.skills {
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}

	out := make([]string, 0, len(r.skills))
	for name := range r.skills {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func FilterRegistry(full *tools.Registry, allowList []string) *tools.Registry {
	if len(allowList) == 0 {
		return full
	}

	filtered := tools.NewRegistry()
	if full == nil {
		return filtered
	}

	for _, name := range allowList {
		tool, ok := full.Get(name)
		if !ok {
			slog.Warn("skill requested unknown tool", "name", name)
			continue
		}
		filtered.Register(tool)
	}
	return filtered
}

type ExecuteParams struct {
	SkillName  string
	Args       map[string]string
	Provider   llm.Provider
	ToolReg    *tools.Registry
	Model      string
	OnText     func(string)
	Permission *agent.Permission
	Hooks      *agent.HookRegistry
	// Locale is the resolved response locale (e.g. "ko", "ja", "zh").
	// Empty or "en" leaves the skill system prompt unchanged. Non-English
	// locales append locale.ResponseDirective so skill output honors the
	// user's language preference without re-running the prompt builder.
	Locale string
}

type ExecuteResult struct {
	Output   string
	Messages []llm.Message
	Usage    llm.UsageStats
}

func (r *Registry) Execute(ctx context.Context, params ExecuteParams) (*ExecuteResult, error) {
	skill, ok := r.Get(params.SkillName)
	if !ok {
		return nil, fmt.Errorf("skill %q not found", params.SkillName)
	}

	rendered := skill.RenderPrompt(params.Args)
	if directive := locale.ResponseDirective(params.Locale); directive != "" {
		rendered = rendered + "\n\n" + directive
	}
	filteredReg := FilterRegistry(params.ToolReg, skill.RequiredTools)
	model := params.Model
	if skill.Model != "" {
		model = skill.Model
	}

	options := []agent.Option{
		agent.WithSystemPrompt(rendered),
		agent.WithModel(model),
		agent.WithMaxIterations(30),
	}
	if params.Permission != nil {
		options = append(options, agent.WithPermission(params.Permission))
	}
	if params.Hooks != nil {
		options = append(options, agent.WithHooks(params.Hooks))
	}

	ag := agent.New(params.Provider, filteredReg, options...)
	result, err := ag.Run(ctx, []llm.Message{llm.NewUserMessage("Execute this skill.")}, params.OnText)
	if err != nil {
		return nil, err
	}

	output := ""
	for i := len(result.Messages) - 1; i >= 0; i-- {
		if result.Messages[i].Role == llm.RoleAssistant {
			output = result.Messages[i].Text()
			break
		}
	}

	return &ExecuteResult{
		Output:   output,
		Messages: result.Messages,
		Usage:    result.Usage,
	}, nil
}
