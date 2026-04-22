package prompt

import (
	"context"
	"sort"
	"strings"
)

type ToolCatalogNode struct {
	priority int
}

func NewToolCatalogNode(priority int) *ToolCatalogNode {
	return &ToolCatalogNode{priority: priority}
}

func (n *ToolCatalogNode) Name() string {
	return "tool_catalog"
}

// CacheBoundary classifies tool catalog as volatile: ToolNames
// varies with feature gating and available surfaces.
func (n *ToolCatalogNode) CacheBoundary() CacheBoundary { return CacheBoundaryVolatile }

func (n *ToolCatalogNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *ToolCatalogNode) Render(_ context.Context, state *RenderState) (string, error) {
	if n == nil {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("You have access to tools for reading and writing files, executing shell commands, searching the web, and interacting with git repositories.")

	if state == nil || len(state.ToolNames) == 0 {
		return b.String(), nil
	}

	names := append([]string(nil), state.ToolNames...)
	sort.Strings(names)
	b.WriteString("\nAvailable tools: ")
	b.WriteString(strings.Join(names, ", "))
	return b.String(), nil
}
