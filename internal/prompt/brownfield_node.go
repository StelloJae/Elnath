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

// CacheBoundary classifies brownfield context as stable.
//
// Stability contract: this classification is defensible only while
// RenderState.ExistingCode and RenderState.TaskLanguage are treated as
// session-level immutables. The node's Render output branches on both
// fields (see Render below: the Go branch vs TypeScript branch), so
// flipping either mid-session would be a prompt-cache break vector that
// this metadata says does not exist. Callers constructing RenderState
// must set these fields once at session start and leave them alone;
// new sessions should be started to switch project posture.
// Flagged in the Phase 8.1.7 critic verdict §4 OBSERVATION for future
// plan-§5 tightening.
func (n *BrownfieldNode) CacheBoundary() CacheBoundary { return CacheBoundaryStable }

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
	b.WriteString("- If you notice the request is based on a misconception, or spot a bug adjacent to what was asked, say so.\n\n")
	b.WriteString("## Bugfix Discipline\n")
	b.WriteString("- Find the SINGLE root cause. If your fix touches more than 2 production files (excluding tests), you likely misidentified the bug.\n")
	b.WriteString("- Look for TODO comments, FIXME markers, and unused/underscored parameters — they often mark the exact location of an unfinished fix.\n")
	b.WriteString("- If your fix breaks tests that previously passed, your change is too broad. Revert to a smaller approach rather than patching the collateral damage.")

	switch state.TaskLanguage {
	case "go":
		b.WriteString("\n\nGo-specific:\n")
		b.WriteString("- Run `go test ./...` FIRST to establish baseline before any edit.\n")
		b.WriteString("- Preserve existing API surface — do not rename exported types or functions.\n")
		b.WriteString("- Prefer the smallest diff: one function change > new file.\n")
		b.WriteString("- Use existing error handling patterns from adjacent code.\n")
		b.WriteString("- CRITICAL: Before changing any function/method signature, grep for ALL callers across the entire codebase (`grep -rn 'functionName' . --include='*.go'`) and update every call site. Missing a caller causes a build failure.\n")
		b.WriteString("- If `go test` or `go build` shows 'not enough arguments' or 'too many arguments', you missed a call site. Search for the function name, fix ALL remaining callers, then re-run tests.\n")
		b.WriteString("- When threading a new parameter (e.g. context.Context) through callers that don't have one available, use context.TODO() as the safe placeholder — never invent field accesses on structs you haven't verified.\n")
		b.WriteString("- For config reload or file-watcher bugs, the root cause is almost always stale state in the watcher callback (a field not updated before a re-read), NOT in public API methods like SetConfigFile or getConfigFile.\n")
		b.WriteString("- Do NOT modify error types, Unwrap methods, or stable public setter/getter APIs to fix a runtime/watcher bug — these are downstream of the root cause.")
	case "typescript":
		b.WriteString("\n\nTypeScript-specific:\n")
		b.WriteString("- Check existing test command (npm test / pnpm test) FIRST.\n")
		b.WriteString("- Follow existing import style (relative vs alias).\n")
		b.WriteString("- Do not add new dependencies unless the task explicitly requires them.\n")
		b.WriteString("- Look for unused parameters (prefixed with _) in function signatures — they often mark declared-but-unimplemented functionality that IS the bug.\n")
		b.WriteString("- Do NOT add cache-busting query parameters (?ts=...) to import() or require() calls — Jest and most test runners cannot resolve file paths with query strings, and all ESM import tests will break.\n")
		b.WriteString("- When fixing config-reload or file-watcher issues, prefer implementing already-declared-but-unused parameters over inventing new cache invalidation mechanisms.")
	}
	return b.String(), nil
}
