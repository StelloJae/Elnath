package prompt

import (
	"context"

	"github.com/stello/elnath/internal/locale"
)

type LocaleInstructionNode struct{}

func (n *LocaleInstructionNode) Name() string {
	return "locale_instruction"
}

// CacheBoundary classifies locale as stable: RenderState.Locale is
// session config and does not vary between turns.
func (n *LocaleInstructionNode) CacheBoundary() CacheBoundary { return CacheBoundaryStable }

func (n *LocaleInstructionNode) Priority() int {
	return 999
}

func (n *LocaleInstructionNode) Render(_ context.Context, state *RenderState) (string, error) {
	if state == nil {
		return "", nil
	}
	return locale.ResponseDirective(state.Locale), nil
}
