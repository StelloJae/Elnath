package skill

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/stello/elnath/internal/wiki"
)

var (
	skillDollarNamePattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	skillNumericArgumentPattern = regexp.MustCompile(`^\d+$`)
)

// Skill represents a wiki-defined skill.
type Skill struct {
	Name          string
	Description   string
	Trigger       string
	RequiredTools []string
	Paths         []string
	ArgumentNames []string
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
		ArgumentNames: normalizeSkillArgumentNames(extraStrings(page.Extra, "arguments")),
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
		result = replaceDollarNamePlaceholder(result, key, value)
	}
	return result
}

func (s *Skill) RenderPromptWithRuntime(args map[string]string, skillDir, sessionID string) string {
	if s == nil {
		return ""
	}
	clone := *s
	clone.Prompt = renderRuntimePlaceholders(clone.Prompt, skillDir, sessionID)
	return clone.RenderPrompt(args)
}

func renderRuntimePlaceholders(input, skillDir, sessionID string) string {
	if skillDir != "" {
		input = strings.ReplaceAll(input, "${CLAUDE_SKILL_DIR}", skillDir)
		input = strings.ReplaceAll(input, "${ELNATH_SKILL_DIR}", skillDir)
	}
	if sessionID != "" {
		input = strings.ReplaceAll(input, "${CLAUDE_SESSION_ID}", sessionID)
		input = strings.ReplaceAll(input, "${ELNATH_SESSION_ID}", sessionID)
	}
	return input
}

func replaceDollarNamePlaceholder(input, key, value string) string {
	if !skillDollarNamePattern.MatchString(key) {
		return input
	}
	token := "$" + key
	re := regexp.MustCompile(`\$` + regexp.QuoteMeta(key) + `([^A-Za-z0-9_]|$)`)
	return re.ReplaceAllStringFunc(input, func(match string) string {
		if match == token {
			return value
		}
		return value + match[len(token):]
	})
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

func normalizeSkillArgumentNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		name = strings.TrimPrefix(name, "$")
		name = strings.TrimPrefix(strings.TrimSuffix(name, ">"), "<")
		name = strings.TrimPrefix(strings.TrimSuffix(name, "}"), "{")
		if name == "" || skillNumericArgumentPattern.MatchString(name) {
			continue
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
