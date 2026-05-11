package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	claudeSkillSource = "claude-skill"
	codexSkillSource  = "codex-skill"
)

type CompatibleSkillRoot struct {
	Path   string
	Source string
}

var claudeToolNameMap = map[string]string{
	"bash":          "bash",
	"read":          "read_file",
	"write":         "write_file",
	"edit":          "edit_file",
	"multiedit":     "edit_file",
	"glob":          "glob",
	"grep":          "grep",
	"webfetch":      "web_fetch",
	"websearch":     "web_search",
	"todowrite":     "todo_write",
	"toolsearch":    "tool_search",
	"skill":         "skill",
	"taskcreate":    "task_create",
	"taskget":       "task_get",
	"tasklist":      "task_list",
	"taskoutput":    "task_output",
	"taskstop":      "task_stop",
	"taskupdate":    "task_update",
	"croncreate":    "schedule_create",
	"crondelete":    "schedule_delete",
	"cronlist":      "schedule_list",
	"enterplanmode": "enter_plan_mode",
	"exitplanmode":  "exit_plan_mode",
	"enterworktree": "enter_worktree",
	"exitworktree":  "exit_worktree",
}

type claudeSkillFrontmatter struct {
	Name                   string     `yaml:"name"`
	Description            string     `yaml:"description"`
	WhenToUse              string     `yaml:"when_to_use"`
	AllowedTools           stringList `yaml:"allowed-tools"`
	AllowedToolsUnderscore stringList `yaml:"allowed_tools"`
	RequiredTools          stringList `yaml:"required_tools"`
	Tools                  stringList `yaml:"tools"`
	Model                  string     `yaml:"model"`
	Effort                 string     `yaml:"effort"`
}

type stringList []string

func (l *stringList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.SequenceNode:
		var out []string
		for _, node := range value.Content {
			var item string
			if err := node.Decode(&item); err != nil {
				return err
			}
			out = append(out, item)
		}
		*l = out
		return nil
	case yaml.ScalarNode:
		var raw string
		if err := value.Decode(&raw); err != nil {
			return err
		}
		*l = splitStringList(raw)
		return nil
	default:
		return nil
	}
}

// LoadClaudeSkillDir loads Claude Code-style .claude/skills/**/SKILL.md files
// from a project root and converts them into Elnath skills.
func LoadClaudeSkillDir(projectRoot string) ([]*Skill, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return nil, nil
	}

	skillsRoot := filepath.Join(projectRoot, ".claude", "skills")
	return LoadCompatibleSkillRoot(CompatibleSkillRoot{Path: skillsRoot, Source: claudeSkillSource})
}

func DefaultCompatibleSkillRoots(projectRoot, homeDir string) []CompatibleSkillRoot {
	var roots []CompatibleSkillRoot
	homeDir = strings.TrimSpace(homeDir)
	if homeDir != "" {
		roots = append(roots,
			CompatibleSkillRoot{Path: filepath.Join(homeDir, ".claude", "skills"), Source: claudeSkillSource},
			CompatibleSkillRoot{Path: filepath.Join(homeDir, ".codex", "skills"), Source: codexSkillSource},
			CompatibleSkillRoot{Path: filepath.Join(homeDir, ".agents", "skills"), Source: codexSkillSource},
		)
	}
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot != "" {
		roots = append(roots,
			CompatibleSkillRoot{Path: filepath.Join(projectRoot, ".claude", "skills"), Source: claudeSkillSource},
			CompatibleSkillRoot{Path: filepath.Join(projectRoot, ".codex", "skills"), Source: codexSkillSource},
		)
	}
	return dedupeCompatibleSkillRoots(roots)
}

func dedupeCompatibleSkillRoots(roots []CompatibleSkillRoot) []CompatibleSkillRoot {
	seen := make(map[string]struct{}, len(roots))
	out := make([]CompatibleSkillRoot, 0, len(roots))
	for _, root := range roots {
		root.Path = strings.TrimSpace(root.Path)
		if root.Path == "" {
			continue
		}
		root.Source = strings.TrimSpace(root.Source)
		if root.Source == "" {
			root.Source = claudeSkillSource
		}
		clean := filepath.Clean(root.Path)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		root.Path = clean
		out = append(out, root)
	}
	return out
}

func LoadCompatibleSkillRoot(root CompatibleSkillRoot) ([]*Skill, error) {
	skillsRoot := strings.TrimSpace(root.Path)
	if skillsRoot == "" {
		return nil, nil
	}
	source := strings.TrimSpace(root.Source)
	if source == "" {
		source = claudeSkillSource
	}
	if info, err := os.Stat(skillsRoot); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("claude skills: stat %q: %w", skillsRoot, err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("claude skills: %q is not a directory", skillsRoot)
	}

	var skills []*Skill
	err := filepath.WalkDir(skillsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		nameHint := filepath.Base(filepath.Dir(path))
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %q: %w", path, err)
		}
		sk, err := parseClaudeSkillWithSource(nameHint, data, source)
		if err != nil {
			return fmt.Errorf("parse %q: %w", path, err)
		}
		skills = append(skills, sk)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return skills, nil
}

// LoadClaudeSkills adds Claude Code-style project skills to the registry.
func (r *Registry) LoadClaudeSkills(projectRoot string) error {
	skills, err := LoadClaudeSkillDir(projectRoot)
	if err != nil {
		return err
	}
	for _, sk := range skills {
		if sk == nil || sk.Status == "draft" {
			continue
		}
		r.Add(sk)
	}
	return nil
}

func (r *Registry) LoadCompatibleSkillRoots(roots []CompatibleSkillRoot) error {
	for _, root := range roots {
		skills, err := LoadCompatibleSkillRoot(root)
		if err != nil {
			return err
		}
		for _, sk := range skills {
			if sk == nil || sk.Status == "draft" {
				continue
			}
			r.Add(sk)
		}
	}
	return nil
}

func parseClaudeSkill(nameHint string, raw []byte) (*Skill, error) {
	return parseClaudeSkillWithSource(nameHint, raw, claudeSkillSource)
}

func parseClaudeSkillWithSource(nameHint string, raw []byte, source string) (*Skill, error) {
	yamlBlock, body, err := splitClaudeSkillFrontmatter(raw)
	if err != nil {
		return nil, err
	}

	var fm claudeSkillFrontmatter
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return nil, fmt.Errorf("parse frontmatter yaml: %w", err)
	}

	name := strings.TrimSpace(fm.Name)
	if name == "" {
		name = strings.TrimSpace(nameHint)
	}
	if err := ValidateSkillName(name); err != nil {
		return nil, err
	}

	prompt := strings.TrimSpace(body)
	if prompt == "" {
		return nil, fmt.Errorf("skill prompt must not be empty")
	}

	return &Skill{
		Name:          name,
		Description:   buildClaudeSkillDescription(fm.Description, fm.WhenToUse),
		Trigger:       "/" + name,
		RequiredTools: collectClaudeSkillTools(fm),
		Model:         strings.TrimSpace(fm.Model),
		Effort:        strings.TrimSpace(fm.Effort),
		Prompt:        prompt,
		Status:        "active",
		Source:        strings.TrimSpace(source),
	}, nil
}

func splitClaudeSkillFrontmatter(raw []byte) (yamlBlock, body string, err error) {
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return "", "", fmt.Errorf("missing opening frontmatter delimiter")
	}
	rest := content[4:]
	closingIdx := strings.Index(rest, "\n---\n")
	if closingIdx == -1 {
		if strings.HasSuffix(rest, "\n---") {
			closingIdx = len(rest) - 4
		} else {
			return "", "", fmt.Errorf("missing closing frontmatter delimiter")
		}
	}
	yamlBlock = rest[:closingIdx]
	endDelimPos := closingIdx + len("\n---\n")
	if endDelimPos <= len(rest) {
		body = strings.TrimPrefix(rest[endDelimPos:], "\n")
	}
	return yamlBlock, body, nil
}

func buildClaudeSkillDescription(description, whenToUse string) string {
	description = strings.TrimSpace(description)
	whenToUse = strings.TrimSpace(whenToUse)
	switch {
	case description != "" && whenToUse != "":
		return description + " - " + whenToUse
	case description != "":
		return description
	default:
		return whenToUse
	}
}

func collectClaudeSkillTools(fm claudeSkillFrontmatter) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, values := range []stringList{
		fm.AllowedTools,
		fm.AllowedToolsUnderscore,
		fm.RequiredTools,
		fm.Tools,
	} {
		for _, value := range values {
			value = normalizeClaudeToolName(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	return out
}

func normalizeClaudeToolName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, "("); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	if strings.Contains(value, "__") {
		return value
	}
	if strings.Contains(value, "_") {
		return strings.ToLower(value)
	}
	key := strings.ToLower(strings.ReplaceAll(value, "-", ""))
	if mapped, ok := claudeToolNameMap[key]; ok {
		return mapped
	}
	return strings.ToLower(value)
}

func splitStringList(raw string) []string {
	var out []string
	for _, chunk := range strings.Split(raw, ",") {
		for _, value := range strings.Fields(chunk) {
			value = strings.TrimSpace(value)
			if value != "" {
				out = append(out, value)
			}
		}
	}
	return out
}
