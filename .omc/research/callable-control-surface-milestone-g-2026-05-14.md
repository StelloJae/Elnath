# Callable control surface milestone G

Date: 2026-05-14
Branch: codex/supervisor-scope-drift-guard
Commit at write time: uncommitted local milestone
Lane: Elnath completion program

## Summary

Milestone G closes a small but real control-surface reporting gap.

Elnath already had `tool_search`, `todo_write`, and `code_symbols`, but
`elnath explain control-surfaces` did not expose discovery, scratchpad, or code
intelligence as first-class callable surfaces. Also, `todo_write` and
`code_symbols` returned structured data but no explicit tool receipt.

This patch makes those surfaces visible and receipt-backed without claiming full
Claude Code parity, broad self-healing, full LSP, or benchmark readiness.

## References inspected

- Elnath control document:
  - `.omc/research/elnath-completion-program-control-2026-05-14.md`
- Elnath parity artifacts:
  - `.omc/research/ccunpacked-reference-parity-closeout-boundary-2026-05-13.md`
  - `.omc/research/control-surface-manifest-backed-status-2026-05-13.md`
  - `.omc/research/ccunpacked-parity-refresh-2026-05-12.md`
- Elnath code:
  - `cmd/elnath/cmd_explain.go`
  - `internal/tools/tool_search.go`
  - `internal/tools/todo.go`
  - `internal/tools/code_symbols.go`
  - `internal/tools/process_tools.go`
- Claude Code reference:
  - `/Users/stello/claude-code-src/src/tools/ToolSearchTool/prompt.ts`
  - `/Users/stello/claude-code-src/src/tools/TodoWriteTool/TodoWriteTool.ts`
  - `/Users/stello/claude-code-src/src/constants/tools.ts`
- Hermes reference:
  - `/Users/stello/.hermes/hermes-agent/tools/todo_tool.py`
  - `/Users/stello/.hermes/hermes-agent/environments/agent_loop.py`

## Changed files

- `cmd/elnath/cmd_explain.go`
  - adds `discovery`, `scratchpad`, and `code_intelligence` to the
    control-surface manifest.
  - keeps `code_intelligence` honest as `partial` because Elnath has
    `code_symbols`, not a full LSP lifecycle.
  - adds a remaining-gap boundary for streaming process watch and full LSP.
- `cmd/elnath/cmd_explain_test.go`
  - verifies the new manifest entries and keeps partial status for
    `code_intelligence`.
- `internal/tools/todo.go`
  - adds a `receipt` object to successful `todo_write` output.
  - records action, read/write flags, execution policy, status counts, and
    verification-nudge availability.
- `internal/tools/todo_test.go`
  - verifies `todo_write` receipt fields and counts.
- `internal/tools/code_symbols.go`
  - adds a `receipt` object to successful `code_symbols` output.
  - records operation, status, language, count, truncation, and error count.
- `internal/tools/code_symbols_test.go`
  - verifies success and partial-success receipts.
- `internal/tools/tool_search.go`
  - routes `todo_write` as `scratchpad` instead of overloading `plan`.

## Verification

- RED:
  - `go test ./cmd/elnath -run 'TestExplainControlSurfacesJSON|TestExplainControlSurfacesText|TestControlSurfaceManifestMatchesToolSearchRouting' -count=1`
    - failed because `discovery`, `scratchpad`, and `code_intelligence` were
      absent from the manifest.
  - `go test ./internal/tools -run 'TestTodoWriteTool_SummarizesChecklist|TestCodeSymbolsToolDocumentSymbols|TestCodeSymbolsToolWorkspaceSymbolsReportsPartialParseErrors' -count=1`
    - failed because `todo_write` and `code_symbols` had no receipt fields.
- GREEN:
  - `go test ./cmd/elnath -run 'TestExplainControlSurfacesJSON|TestExplainControlSurfacesText|TestControlSurfaceManifestMatchesToolSearchRouting' -count=1`
    - PASS
  - `go test ./internal/tools -run 'TestTodoWriteTool_SummarizesChecklist|TestCodeSymbolsToolDocumentSymbols|TestCodeSymbolsToolWorkspaceSymbolsReportsPartialParseErrors|TestToolSearchFindsCodeSymbolsAsDeferredCodeIntelligence' -count=1`
    - PASS
  - `go test ./cmd/elnath ./internal/tools -count=1`
    - PASS (`cmd/elnath` 44.599s, `internal/tools` 63.431s)
  - `go test ./internal/agent -run 'TestBuildToolDefsSearchFirstDefersControlSurfaceTools|TestAgentSearchFirstLoadsSelectedDeferredToolNextTurn|TestPermissionWithActualToolNames' -count=1`
    - PASS
  - `git diff --check`
    - PASS

## Benchmark / corpus / baseline

- Benchmark run: no
- Full v8: no
- Baseline: no
- Codex CLI comparison: no
- Claude Code comparison: no
- Corpus mutation: no
- Baseline mutation: no

## Claim boundary

Allowed:

- `elnath explain control-surfaces` now exposes discovery, scratchpad, and
  code-intelligence surfaces.
- `todo_write` and `code_symbols` now return explicit structured receipts.
- `todo_write` is classified as scratchpad instead of plan metadata.

Not allowed:

- Full Claude Code parity.
- Full LSP lifecycle.
- Streaming line-watch process monitor.
- Broad silent self-healing.
- v8 benchmark success.
- Elnath better-than-Claude-Code or better-than-Codex claims.

## Unrelated dirty files excluded

- `.omc/research/elnath-final-autonomous-runtime-goal-2026-05-13.md`
- `scripts/run_current_benchmark_wrapper.sh`
- `scripts/test_benchmark_wrapper_v8_task_guidance.sh`
- `scripts/test_current_benchmark_wrapper_completion_guards.sh`
- `.claude/`
- `docs/superpowers/plans/2026-04-30-elnath-local-managed-runtime.md`

## Remaining risk

- `code_intelligence` is intentionally partial. `code_symbols` is useful but
  does not replace definition/reference/hover or language-server lifecycle.
- `todo_write` receipt is tool-result evidence, not a full persisted UI task
  store.
- `tool_search` is still a discovery bootstrap and is not itself discovered by
  a self-query.

## Next milestone recommendation

Milestone H should target the next real structural blocker, not benchmark
symptoms. Current best candidate: runtime registry introspection or user-input
pause/resume boundary, chosen only after re-reading current code and reference
surfaces.
