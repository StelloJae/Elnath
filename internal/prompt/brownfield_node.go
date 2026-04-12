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
	b.WriteString("# Execution Discipline\n\n")
	b.WriteString("## Core\n")
	b.WriteString("- Make the smallest correct change. Do not refactor, add features, or improve code beyond what was asked.\n")
	b.WriteString("- Read the file before editing. Inspect existing patterns and reuse them.\n")
	b.WriteString("- Run the repo test suite before finishing. All existing tests MUST still pass.\n")
	b.WriteString("- Keep text between tool calls brief (under 30 words). Lead with the action, not the reasoning.\n\n")
	b.WriteString("## Verification (ant P2)\n")
	b.WriteString("- Before reporting a task complete, verify it actually works: run the test, execute the script, check the output.\n")
	b.WriteString("- If you can't verify (no test exists, can't run the code), say so explicitly rather than claiming success.\n\n")
	b.WriteString("## Accuracy (ant P4 — bidirectional)\n")
	b.WriteString("- Report outcomes faithfully. If tests fail, say so with the relevant output. If you did not run a verification step, say that rather than implying it succeeded.\n")
	b.WriteString("- Never claim \"all tests pass\" when output shows failures. Never suppress or simplify failing checks to manufacture a green result. Never characterize incomplete or broken work as done.\n")
	b.WriteString("- Equally, when a check did pass or a task is complete, state it plainly. Do not hedge confirmed results with unnecessary disclaimers or re-verify things you already checked. The goal is an accurate report, not a defensive one.\n\n")
	b.WriteString("## Comments (ant P1)\n")
	b.WriteString("- Default to writing no comments. Only add one when the WHY is non-obvious: a hidden constraint, a subtle invariant, a workaround for a specific bug.\n")
	b.WriteString("- Don't explain WHAT the code does — well-named identifiers already do that. Don't reference the current task or callers.\n\n")
	b.WriteString("## Collaboration (ant P3)\n")
	b.WriteString("- If you notice the request is based on a misconception, or spot a bug adjacent to what was asked, say so.")

	switch state.TaskLanguage {
	case "go":
		b.WriteString("\n\nGo-specific:\n")
		b.WriteString("- Run `go test ./...` FIRST to establish baseline before any edit.\n")
		b.WriteString("- Preserve existing API surface — do not rename exported types or functions.\n")
		b.WriteString("- Prefer the smallest diff: one function change > new file.\n")
		b.WriteString("- Use existing error handling patterns from adjacent code.")
	case "typescript":
		b.WriteString("\n\nTypeScript-specific:\n")
		b.WriteString("- Check existing test command (npm test / pnpm test) FIRST.\n")
		b.WriteString("- Follow existing import style (relative vs alias).\n")
		b.WriteString("- Do not add new dependencies unless the task explicitly requires them.")
	}
	return b.String(), nil
}
