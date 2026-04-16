package skill

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/stello/elnath/internal/wiki"
)

var skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type Creator struct {
	store    *wiki.Store
	tracker  *Tracker
	registry *Registry
}

func NewCreator(store *wiki.Store, tracker *Tracker, registry *Registry) *Creator {
	return &Creator{store: store, tracker: tracker, registry: registry}
}

func (c *Creator) Create(params CreateParams) (*Skill, error) {
	if c == nil || c.store == nil {
		return nil, fmt.Errorf("skill creator requires a wiki store")
	}
	name := strings.TrimSpace(params.Name)
	if err := ValidateSkillName(name); err != nil {
		return nil, err
	}
	if params.Status == "" {
		params.Status = "active"
	}
	if params.Source == "" {
		params.Source = "user"
	}
	prompt := strings.TrimSpace(params.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("skill prompt must not be empty")
	}

	extra := map[string]any{
		"name":   name,
		"status": params.Status,
		"source": params.Source,
	}
	if desc := strings.TrimSpace(params.Description); desc != "" {
		extra["description"] = desc
	}
	if trigger := strings.TrimSpace(params.Trigger); trigger != "" {
		extra["trigger"] = trigger
	}
	if len(params.RequiredTools) > 0 {
		extra["required_tools"] = append([]string(nil), params.RequiredTools...)
	}
	if model := strings.TrimSpace(params.Model); model != "" {
		extra["model"] = model
	}
	if len(params.SourceSessions) > 0 {
		extra["source_sessions"] = append([]string(nil), params.SourceSessions...)
	}

	page := &wiki.Page{
		Path:    skillPagePath(name),
		Title:   name,
		Type:    wiki.PageTypeAnalysis,
		Tags:    []string{"skill"},
		Extra:   extra,
		Content: prompt,
	}
	if err := c.store.Create(page); err != nil {
		return nil, fmt.Errorf("create skill %q: %w", name, err)
	}

	sk := FromPage(page)
	if sk == nil {
		return nil, fmt.Errorf("created page did not parse as skill")
	}
	if sk.Status == "active" && c.registry != nil {
		c.registry.Add(sk)
	}
	return sk, nil
}

func (c *Creator) Delete(name string) error {
	if c == nil || c.store == nil {
		return fmt.Errorf("skill creator requires a wiki store")
	}
	name = strings.TrimSpace(name)
	if err := ValidateSkillName(name); err != nil {
		return err
	}
	return c.store.Delete(skillPagePath(name))
}

func (c *Creator) Promote(name string) error {
	if c == nil || c.store == nil {
		return fmt.Errorf("skill creator requires a wiki store")
	}
	name = strings.TrimSpace(name)
	if err := ValidateSkillName(name); err != nil {
		return err
	}
	page, err := c.store.Read(skillPagePath(name))
	if err != nil {
		return fmt.Errorf("promote skill %q: %w", name, err)
	}
	if page.Extra == nil {
		page.Extra = make(map[string]any)
	}
	page.Extra["status"] = "active"
	page.Extra["source"] = "promoted"
	if err := c.store.Update(page); err != nil {
		return fmt.Errorf("promote skill %q: %w", name, err)
	}
	if c.registry != nil {
		if sk := FromPage(page); sk != nil {
			c.registry.Add(sk)
		}
	}
	return nil
}

func skillPagePath(name string) string {
	return "skills/" + name + ".md"
}

func ValidateSkillName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("skill name must not be empty")
	}
	if !skillNamePattern.MatchString(name) {
		return fmt.Errorf("invalid skill name %q: use lowercase letters, numbers, and hyphens only", name)
	}
	return nil
}
