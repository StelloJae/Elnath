package prompt

import (
	"context"
	"fmt"

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
	if state == nil || state.Locale == "" || state.Locale == "en" {
		return "", nil
	}
	name := locale.LangToEnglishName(state.Locale)
	if name == "" {
		return "", nil
	}
	return fmt.Sprintf("Respond in %s.", name), nil
}
