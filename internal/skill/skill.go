package skill

import (
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/wiki"
)

// Skill represents a wiki-defined skill.
type Skill struct {
	Name          string
	Description   string
	Trigger       string
	RequiredTools []string
	Paths         []string
	Model         string
	Effort        string
	BaseDir       string
	Prompt        string
	Status        string
	Source        string
}

func (s *Skill) TrustLevel() string {
	if s == nil {
		return ""
	}
	return TrustLevelForSource(s.Source)
}

func (s *Skill) External() bool {
	if s == nil {
		return false
	}
	return SkillSourceIsExternal(s.Source)
}

func TrustLevelForSource(source string) string {
	switch strings.TrimSpace(source) {
	case codexPluginSkillSource:
		return "plugin_cache"
	case claudeSkillSource, claudeCommandSkillSource, codexSkillSource:
		return "local_compatible"
	case "":
		return "wiki"
	default:
		return "declared"
	}
}

func SkillSourceIsExternal(source string) bool {
	return strings.TrimSpace(source) == codexPluginSkillSource
}

func FromPage(page *wiki.Page) *Skill {
	if page == nil || !hasTag(page.Tags, "skill") {
		return nil
	}

	name, ok := stringExtra(page.Extra, "name")
	if !ok || name == "" {
		return nil
	}
	status := extraString(page.Extra, "status")
	if status == "" {
		status = "active"
	}

	return &Skill{
		Name:          name,
		Description:   extraString(page.Extra, "description"),
		Trigger:       extraString(page.Extra, "trigger"),
		RequiredTools: extraStrings(page.Extra, "required_tools"),
		Paths:         normalizeSkillPaths(extraStrings(page.Extra, "paths")),
		Model:         extraString(page.Extra, "model"),
		Effort:        extraString(page.Extra, "effort"),
		BaseDir:       extraString(page.Extra, "base_dir"),
		Prompt:        page.Content,
		Status:        status,
		Source:        extraString(page.Extra, "source"),
	}
}

func (s *Skill) RenderPrompt(args map[string]string) string {
	if s == nil {
		return ""
	}

	result := s.Prompt
	if arguments := firstNonEmptyArg(args, "ARGUMENTS", "arguments", "args"); arguments != "" {
		result = strings.ReplaceAll(result, "$ARGUMENTS", arguments)
		result = strings.ReplaceAll(result, "{arguments}", arguments)
		result = strings.ReplaceAll(result, "{args}", arguments)
	}
	for key, value := range args {
		result = strings.ReplaceAll(result, "{"+key+"}", value)
	}
	return result
}

func firstNonEmptyArg(args map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(args[key]); value != "" {
			return value
		}
	}
	return ""
}

func hasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

func extraString(extra map[string]any, key string) string {
	value, _ := stringExtra(extra, key)
	return value
}

func stringExtra(extra map[string]any, key string) (string, bool) {
	if extra == nil {
		return "", false
	}
	value, ok := extra[key].(string)
	return value, ok
}

func extraStrings(extra map[string]any, key string) []string {
	if extra == nil {
		return nil
	}

	raw, ok := extra[key]
	if !ok {
		return nil
	}

	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			out = append(out, fmt.Sprintf("%v", value))
		}
		return out
	default:
		return nil
	}
}

func normalizeSkillPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || path == "**" {
			continue
		}
		path = strings.TrimSuffix(path, "/**")
		if path == "" || path == "**" {
			continue
		}
		out = append(out, path)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
