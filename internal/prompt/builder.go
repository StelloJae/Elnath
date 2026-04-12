package prompt

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type renderedNode struct {
	index    int
	name     string
	priority int
	body     string
	keep     bool
}

// Builder assembles a prompt from a registered list of nodes.
type Builder struct {
	nodes []Node
}

func NewBuilder() *Builder {
	return &Builder{nodes: make([]Node, 0, 8)}
}

// Register appends a node. Registration order defines render order.
func (b *Builder) Register(n Node) {
	if b == nil || n == nil {
		return
	}
	b.nodes = append(b.nodes, n)
}

func (b *Builder) Build(ctx context.Context, state *RenderState) (string, error) {
	if b == nil || len(b.nodes) == 0 {
		return "", nil
	}

	rendered := make([]renderedNode, 0, len(b.nodes))
	for i, node := range b.nodes {
		body, err := node.Render(ctx, state)
		if err != nil {
			return "", fmt.Errorf("prompt: node %q render failed: %w", node.Name(), err)
		}
		rendered = append(rendered, renderedNode{
			index:    i,
			name:     node.Name(),
			priority: node.Priority(),
			body:     body,
			keep:     true,
		})
	}

	budget := 0
	if state != nil {
		budget = state.TokenBudget
	}
	if budget > 0 {
		applyBudget(rendered, budget)
	}

	parts := make([]string, 0, len(rendered))
	for _, item := range rendered {
		if !item.keep || item.body == "" {
			continue
		}
		parts = append(parts, item.body)
	}
	return strings.Join(parts, "\n\n"), nil
}

func applyBudget(rendered []renderedNode, budget int) {
	if joinedLength(rendered) <= budget {
		return
	}

	candidates := make([]int, 0, len(rendered))
	for i, item := range rendered {
		if item.body == "" {
			continue
		}
		candidates = append(candidates, i)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		left := rendered[candidates[i]]
		right := rendered[candidates[j]]
		if left.priority != right.priority {
			return left.priority < right.priority
		}
		return left.index < right.index
	})

	remaining := countKeptNonEmpty(rendered)
	for _, idx := range candidates {
		if joinedLength(rendered) <= budget || remaining <= 1 {
			break
		}
		if !rendered[idx].keep || rendered[idx].body == "" {
			continue
		}
		rendered[idx].keep = false
		remaining--
	}
}

func countKeptNonEmpty(rendered []renderedNode) int {
	total := 0
	for _, item := range rendered {
		if item.keep && item.body != "" {
			total++
		}
	}
	return total
}

func joinedLength(rendered []renderedNode) int {
	parts := 0
	total := 0
	for _, item := range rendered {
		if !item.keep || item.body == "" {
			continue
		}
		if parts > 0 {
			total += len("\n\n")
		}
		total += len(item.body)
		parts++
	}
	return total
}
