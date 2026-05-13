package prompt

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/stello/elnath/internal/skill"
)

type SkillCatalogNode struct {
	priority int
	registry *skill.Registry
}

func NewSkillCatalogNode(priority int, registry *skill.Registry) *SkillCatalogNode {
	return &SkillCatalogNode{priority: priority, registry: registry}
}

func (n *SkillCatalogNode) Name() string {
	return "skill_catalog"
}

// CacheBoundary classifies skill catalog as volatile: the skill set
// can shift within a session as new skills land on disk.
func (n *SkillCatalogNode) CacheBoundary() CacheBoundary { return CacheBoundaryVolatile }

func (n *SkillCatalogNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *SkillCatalogNode) Render(_ context.Context, state *RenderState) (string, error) {
	if n == nil || n.registry == nil {
		return "", nil
	}
	if state != nil && state.BenchmarkMode {
		return "", nil
	}
	skills := n.registry.List()
	if len(skills) == 0 {
		return "", nil
	}
	var b strings.Builder
	var conditionalCount int
	for _, sk := range skills {
		if sk != nil && len(sk.Paths) > 0 {
			conditionalCount++
		}
	}
	unconditionalCount := len(skills) - conditionalCount
	if unconditionalCount > 0 {
		b.WriteString("Available skills (invoke via /name):\n")
	}
	for _, sk := range skills {
		if sk == nil || len(sk.Paths) > 0 {
			continue
		}
		fmt.Fprintf(&b, "\n- /%s", sk.Name)
		if sk.Trigger != "" {
			parts := strings.SplitN(sk.Trigger, " ", 2)
			if len(parts) > 1 {
				b.WriteString(" ")
				b.WriteString(parts[1])
			}
		}
		if sk.Description != "" {
			b.WriteString(" — ")
			b.WriteString(sk.Description)
		}
	}
	if conditionalCount > 0 {
		matches := n.conditionalMatchesFromState(state)
		if len(matches) > 0 {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString("Matched conditional skills (invoke via /name or skill tool):")
			for _, match := range matches {
				sk, ok := n.registry.Get(match.SkillName)
				if !ok || sk == nil {
					continue
				}
				fmt.Fprintf(&b, "\n- /%s", sk.Name)
				if sk.Trigger != "" {
					parts := strings.SplitN(sk.Trigger, " ", 2)
					if len(parts) > 1 {
						b.WriteString(" ")
						b.WriteString(parts[1])
					}
				}
				if sk.Description != "" {
					b.WriteString(" — ")
					b.WriteString(sk.Description)
				}
				fmt.Fprintf(&b, " (matched %s)", match.Path)
			}
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Conditional skills are hidden from the static prompt; use skill_catalog match_paths with touched file paths to discover matching skills.")
	}
	return b.String(), nil
}

func (n *SkillCatalogNode) conditionalMatchesFromState(state *RenderState) []skill.ConditionalSkillMatch {
	if n == nil || n.registry == nil || state == nil {
		return nil
	}
	paths := skillCatalogCandidatePaths(state.UserInput)
	if len(paths) == 0 {
		return nil
	}
	return n.registry.ConditionalMatchesForPaths(paths, state.WorkDir)
}

func skillCatalogCandidatePaths(input string) []string {
	fields := strings.Fields(input)
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{})
	for _, field := range fields {
		path := cleanSkillCatalogPathToken(field)
		if path == "" || !looksLikeSkillCatalogPath(path) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func cleanSkillCatalogPathToken(token string) string {
	token = strings.TrimFunc(strings.TrimSpace(token), func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("`'\"“”‘’()[]{}<>.,;:", r)
	})
	token = filepath.ToSlash(filepath.Clean(token))
	if token == "." || token == "/" {
		return ""
	}
	return strings.TrimPrefix(token, "./")
}

func looksLikeSkillCatalogPath(path string) bool {
	if strings.HasPrefix(path, "../") || path == ".." {
		return false
	}
	if !strings.Contains(path, "/") {
		return false
	}
	return strings.Contains(filepath.Base(path), ".")
}
