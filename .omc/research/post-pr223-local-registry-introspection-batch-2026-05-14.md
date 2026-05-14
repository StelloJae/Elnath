# Post-PR223 Local Registry / Control-Surface Batch - 2026-05-14

## Summary

Branch: `codex/post-pr223-registry-introspection`
PR: none

Local commits:

- `7d9f557` `fix(tools): require active todo phrasing`
- `a0ff7fa` `feat(runtime): report tool registry status`
- `b3ac800` `docs(tools): surface active todo requirement`
- `a97831a` `feat(tools): add go code diagnostics`

This is a local-only batch after PR #223. It does not open a PR yet and does not run CI.
The batch tightens control-surface reliability in three areas:

- `todo_write` active work state discipline
- runtime registry/control-surface status introspection
- Go-native code diagnostics through `code_symbols`

## Changed Areas

- `internal/tools/todo.go`
- `internal/tools/todo_test.go`
- `internal/tools/code_symbols.go`
- `internal/tools/code_symbols_test.go`
- `internal/tools/tool_search_test.go`
- `cmd/elnath/runtime_status.go`
- `cmd/elnath/runtime_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`
- `.omc/research/todo-write-active-form-guard-2026-05-14.md`
- `.omc/research/runtime-status-registry-introspection-2026-05-14.md`
- `.omc/research/code-symbols-go-diagnostics-2026-05-14.md`

## References Inspected

- `/Users/stello/claude-code-src/src/tools/TodoWriteTool/TodoWriteTool.ts`
- `/Users/stello/claude-code-src/src/tools/TodoWriteTool/prompt.ts`
- `/Users/stello/claude-code-src/src/Tool.ts`
- `/Users/stello/claude-code-src/src/utils/toolSearch.ts`
- `/Users/stello/claude-code-src/src/services/lsp/LSPDiagnosticRegistry.ts`
- `/Users/stello/claude-code-src/src/services/lsp/passiveFeedback.ts`
- Elnath runtime/tool/control-surface code listed above

## Verification

Focused verification is recorded in the three per-slice artifacts.

Batch verification:

```text
go test ./internal/tools ./cmd/elnath -count=1
PASS (ok github.com/stello/elnath/internal/tools 38.206s; ok github.com/stello/elnath/cmd/elnath 21.939s)

go vet ./internal/tools ./cmd/elnath
PASS

git diff --check origin/main..HEAD
PASS
```

Broader pre-PR local verification:

```text
go test ./... -count=1
PASS

go vet ./...
PASS

git diff --check origin/main..HEAD
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

- Local post-PR223 control-surface batch passed focused and package verification.
- Runtime `/status` reports tool registry and control-surface coverage.
- `todo_write` now enforces and advertises active-form requirements for in-progress work.
- `code_symbols diagnostics` reports Go parser diagnostics.

Forbidden:

- Elnath completion is proven.
- Full LSP lifecycle exists.
- Full UI-level answer collection exists.
- Full async streaming monitor exists.
- v8 benchmark passed.
- Elnath is better than Claude Code or Codex.

## Remaining Risk

This batch improves runtime/tool self-observation but does not close every product boundary.
The remaining large boundaries are still:

- UI-level answer collection outside the runtime
- richer async streaming process monitor
- full multi-language LSP lifecycle
- benchmark-readiness validation after structural work

## Next Recommendation

Open one coherent PR for this local batch. Do not split it into tiny PRs.
