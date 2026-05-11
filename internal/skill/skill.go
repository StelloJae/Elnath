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
	Model         string
	Effort        string
	Prompt        string
	Status        string
	Source        string
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
		Model:         extraString(page.Extra, "model"),
		Effort:        extraString(page.Extra, "effort"),
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
