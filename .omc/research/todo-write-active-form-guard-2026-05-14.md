# TodoWrite Active Form Guard - 2026-05-14

## Summary

Branch: `codex/post-pr223-registry-introspection`
PR: none
Commit: none yet

This local milestone tightens the `todo_write` control-surface contract after PR #223.
`in_progress` todos now require an active-form phrase, matching the intended Claude Code-style
working-state distinction between the user-facing task label and the currently active work phrase.

## References Inspected

- `/Users/stello/claude-code-src/src/tools/TodoWriteTool/prompt.ts`
- `/tmp/elnath-registry-introspection.dxxVoH/internal/tools/todo.go`
- `/tmp/elnath-registry-introspection.dxxVoH/internal/tools/todo_test.go`

No proprietary source text, prompt text, or error text was copied. The behavior was reimplemented
in Elnath's Go-native tool contract.

## Changed Files

- `internal/tools/todo.go`
- `internal/tools/todo_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`

## Behavior Added

- Rejects `todo_write` input where a todo has `status: "in_progress"` but omits both
  `active_form` and `activeForm`.
- Preserves camel-case compatibility through the existing `activeForm` fallback.
- Keeps the existing single-`in_progress` guard intact.
- Updates `elnath explain control-surfaces` scratchpad notes so the surfaced control contract
  mentions the new active-form guard.

## Verification

TDD probe before implementation:

```text
go test ./internal/tools -run 'TestTodoWriteTool_RejectsInvalidTodos|TestTodoWriteTool_SummarizesChecklist' -count=1
FAIL as expected: in_progress without active_form was accepted
```

Focused verification after implementation:

```text
gofmt -w internal/tools/todo.go internal/tools/todo_test.go
go test ./internal/tools -run 'TestTodoWriteTool_RejectsInvalidTodos|TestTodoWriteTool_SummarizesChecklist|TestTodoWriteTool_NudgesVerificationBeforeFinalClaim|TestTodoWriteTool_NoNudgeWhenVerificationTodoExists|TestTodoWriteToolMetadata' -count=1
PASS
```

Package verification:

```text
go test ./internal/tools -count=1
PASS (ok github.com/stello/elnath/internal/tools 40.865s)

go vet ./internal/tools
PASS

git diff --check
PASS
```

Control-surface note TDD probe:

```text
go test ./cmd/elnath -run TestExplainControlSurfacesJSON -count=1
FAIL as expected: scratchpad notes did not mention active_form
```

Control-surface focused verification:

```text
gofmt -w cmd/elnath/cmd_explain.go cmd/elnath/cmd_explain_test.go
go test ./cmd/elnath -run 'TestExplainControlSurfacesJSON|TestExplainControlSurfacesText|TestControlSurfaceManifestMatchesToolSearchRouting' -count=1
PASS
```

Combined package verification after control-surface update:

```text
go test ./internal/tools ./cmd/elnath -count=1
PASS (ok github.com/stello/elnath/internal/tools 41.297s; ok github.com/stello/elnath/cmd/elnath 24.458s)

go vet ./internal/tools ./cmd/elnath
PASS

git diff --check
PASS
```

## Benchmark / Baseline Boundary

- Full v8 benchmark: not run
- Baseline: not run
- Codex comparison: not run
- Claude comparison: not run
- Benchmark corpus changed: no
- Baseline artifact changed: no

## Claim Boundary

Allowed:

- `todo_write` now rejects active in-progress work without an active-form phrase.
- Focused `internal/tools` TodoWrite tests passed for this behavior.

Forbidden:

- Elnath completion is proven.
- Benchmark readiness is proven.
- Elnath is better than Claude Code or Codex.

## Remaining Risk

This is a narrow control-surface guard only. It does not yet complete registry introspection,
full supervisor loop correction, or benchmark-readiness validation.

## Next Recommendation

Commit this as part of the local post-PR223 control-surface batch. Do not open a PR yet unless
the next adjacent local slice is either blocked or would make the batch less reviewable.
