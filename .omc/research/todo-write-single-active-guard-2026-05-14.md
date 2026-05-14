# todo_write single active guard

Date: 2026-05-14
Branch: `codex/post-pr222-code-intel`
Status: local implementation slice

## Problem

Claude Code's `TodoWrite` reference guidance says exactly one task should be in progress during active work. Elnath's `todo_write` scratchpad already tracked counts and verification nudges, but it accepted multiple `in_progress` tasks.

That weakens supervisor clarity because a model can present several concurrent active tasks without a clear current execution focus.

## Reference inspected

- `/Users/stello/claude-code-src/src/tools/TodoWriteTool/prompt.ts`
- `/tmp/elnath-code-intel.HQHwyb/internal/tools/todo.go`
- `/tmp/elnath-code-intel.HQHwyb/internal/tools/todo_test.go`

Reference pattern used:

- keep a structured task list
- allow `pending`, `in_progress`, and `completed`
- keep exactly one active task during execution
- do not mark incomplete/error work as completed

Elnath-native choice:

- enforce at most one `in_progress` item at the tool boundary
- preserve all-completed and one-active valid behavior
- keep receipt and verification-nudge behavior unchanged

## Changed files

- `internal/tools/todo.go`
- `internal/tools/todo_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`

## Behavior added

- `todo_write` now rejects inputs with more than one `in_progress` todo.
- Error text: `todo_write: at most one in_progress todo is allowed`
- `elnath explain control-surfaces` now records the scratchpad single `in_progress` guard.

## Verification

TDD expected failure before implementation:

- `go test ./internal/tools -run 'TestTodoWriteTool_RejectsInvalidTodos' -count=1`
  - FAIL before code change: multiple `in_progress` todos were accepted.

Focused verification after implementation:

- `go test ./internal/tools -run 'TestTodoWriteTool_RejectsInvalidTodos|TestTodoWriteTool_SummarizesChecklist|TestTodoWriteTool_NudgesVerificationBeforeFinalClaim|TestTodoWriteTool_NoNudgeWhenVerificationTodoExists' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/tools 0.494s`
- `go test ./cmd/elnath -run 'TestExplainControlSurfacesJSON|TestExplainControlSurfacesText|TestControlSurfaceManifestMatchesToolSearchRouting' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.656s`

Affected package verification:

- `go test ./cmd/elnath ./internal/tools -count=1`
  - PASS:
    - `ok github.com/stello/elnath/cmd/elnath 19.327s`
    - `ok github.com/stello/elnath/internal/tools 38.592s`
- `go vet ./cmd/elnath ./internal/tools`
  - PASS
- `git diff --check`
  - PASS

## Benchmark / corpus / baseline

- Full v8 benchmark: no
- Baseline: no
- Codex CLI comparison: no
- Claude Code comparison: no
- Benchmark corpus mutation: no
- Baseline mutation: no

## Claim boundary

Allowed:

- Elnath now rejects multiple active todo states in `todo_write`.
- Scratchpad control-surface status reflects the new guard.

Not allowed:

- Elnath has full Claude Code TodoWrite parity.
- UI-level task list behavior is complete.
- Benchmark readiness or superiority is proven.

## Remaining risk

- Elnath still allows zero `in_progress` tasks for initial planning, all-pending lists, or all-completed lists.
- This guard is tool-boundary enforcement, not a full UI task-management workflow.

## Next autonomous action

Run affected package verification for `cmd/elnath` and `internal/tools`, then commit this as a second local batch slice. Do not open a PR until the local batch is intentionally ready.
