package prompt

import (
	"context"
	"fmt"
	"strings"

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
	b.WriteString("Available skills (invoke via /name):\n")
	for _, sk := range skills {
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
	return b.String(), nil
}
