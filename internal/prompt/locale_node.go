package prompt

import (
	"context"

	"github.com/stello/elnath/internal/locale"
)

type LocaleInstructionNode struct{}

func (n *LocaleInstructionNode) Name() string {
	return "locale_instruction"
}

func (n *LocaleInstructionNode) Priority() int {
	return 999
}

func (n *LocaleInstructionNode) Render(_ context.Context, state *RenderState) (string, error) {
	if state == nil {
		return "", nil
	}
	return locale.ResponseDirective(state.Locale), nil
}
