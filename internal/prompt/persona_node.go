package prompt

import (
	"context"
	"strings"
)

type PersonaNode struct {
	priority int
}

func NewPersonaNode(priority int) *PersonaNode {
	return &PersonaNode{priority: priority}
}

func (n *PersonaNode) Name() string {
	return "persona"
}

func (n *PersonaNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *PersonaNode) Render(_ context.Context, state *RenderState) (string, error) {
	if n == nil || state == nil {
		return "", nil
	}
	return strings.TrimSpace(state.PersonaExtra), nil
}
