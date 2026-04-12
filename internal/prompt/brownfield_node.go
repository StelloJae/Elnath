package prompt

import (
	"context"
	"strings"
)

type BrownfieldNode struct {
	priority int
}

func NewBrownfieldNode(priority int) *BrownfieldNode {
	return &BrownfieldNode{priority: priority}
}

func (n *BrownfieldNode) Name() string {
	return "brownfield"
}

func (n *BrownfieldNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *BrownfieldNode) Render(_ context.Context, state *RenderState) (string, error) {
	if n == nil || state == nil || !state.ExistingCode {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("Brownfield execution guidance:\n")
	b.WriteString("- Inspect existing files, tests, and nearby patterns before editing.\n")
	b.WriteString("- Keep scope bounded to the smallest correct change.\n")
	b.WriteString("- Prefer repo-native verification commands and reuse existing abstractions.\n")
	b.WriteString("- Ask the user only when missing information would materially change the outcome or the decision is costly to reverse.")
	if state.VerifyHint {
		b.WriteString("\n- This task explicitly emphasizes verification or regression safety; prioritize proving the change with tests or repo-native checks.")
	}
	return b.String(), nil
}
