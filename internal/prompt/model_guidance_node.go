package prompt

import (
	"context"
	"strings"
)

type ModelGuidanceNode struct {
	priority int
}

func NewModelGuidanceNode(priority int) *ModelGuidanceNode {
	return &ModelGuidanceNode{priority: priority}
}

func (n *ModelGuidanceNode) Name() string {
	return "model_guidance"
}

func (n *ModelGuidanceNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *ModelGuidanceNode) Render(_ context.Context, state *RenderState) (string, error) {
	if n == nil || state == nil {
		return "", nil
	}

	provider := strings.ToLower(strings.TrimSpace(state.Provider))
	model := strings.ToLower(strings.TrimSpace(state.Model))

	switch {
	case provider == "anthropic" || strings.Contains(model, "claude"):
		return "Model guidance: Use short XML-style tags when they make structure or boundaries clearer.", nil
	case provider == "ollama" || strings.Contains(model, "llama"):
		return "Model guidance: Keep responses concise and direct on local Ollama-style models.", nil
	case provider == "openai" || provider == "openai-responses" || provider == "codex" || strings.Contains(model, "gpt"):
		return "Model guidance: Prefer structured responses and valid JSON when the task explicitly asks for structured output.", nil
	default:
		return "", nil
	}
}
