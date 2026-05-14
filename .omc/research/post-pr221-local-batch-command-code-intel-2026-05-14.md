# Post-PR221 local batch: command help guard and code definition lookup

Date: 2026-05-14
Branch: `codex/post-pr221-next`
Status: local batch, not PR-opened

## Why this batch exists

The user correctly flagged excessive PR/CI churn. After PR #219, #220, and
#221, the work mode is changed to local batch-first:

- no PR per tiny slice
- no CI until multiple related slices form one coherent milestone
- local focused and affected-package verification first
- one artifact for the batch

## Current main state

- HEAD: `c9880468760d76bd47d1423e90ce0e45a122a02f`
- HEAD equals `origin/main`: yes
- Open PRs: none
- Post-PR221 broad health already checked:
  - `go vet ./...`: PASS
  - `go test ./... -count=1`: PASS

## Slice 1: command help drift regression guard

### Problem

PR #219-#221 repaired command help drift, but future commands could regress by
falling back to top-level help or returning an unknown-subcommand error.

### Implementation

Added `TestVisibleCommandsHaveCommandSpecificHelp`.

The test iterates over `commandCatalog(false)` and asserts each visible command:

- accepts `--help`
- emits no stderr
- does not fall back to top-level help
- includes command-specific usage text

### Reference pattern

Claude Code and Hermes both derive help/menu/completion surfaces from a
command registry. Elnath keeps its Go-native registry but now has a broad guard
against visible help drift.

## Slice 2: Go-native code definition lookup

### Problem

`code_symbols` was useful for `document_symbols` and `workspace_symbols`, but
the remaining code-intelligence boundary was still too coarse. Claude Code's
LSP tool supports definition/reference/hover. Elnath should not claim full LSP,
but exact-name Go definition lookup is a small durable improvement.

### Implementation

Added `code_symbols` operation `definition`.

Behavior:

- requires `query`
- scans Go files under workspace or optional `path`
- honors existing path guard and gitignore filtering
- returns exact-name definition matches only
- supports receiver-qualified method names such as `Worker.Run`
- supports unqualified method lookup such as `Run` when the stored symbol is
  `Worker.Run`
- returns `not_found` when no symbol exists and no parse errors occur
- preserves structured receipt fields with `operation=definition`

Boundary:

- not full LSP
- not position-based
- no hover/reference/implementation lookup
- no language server lifecycle

## Slice 3: task output receipt bounds

### Problem

`task_output` and `task_monitor` returned bounded output metadata in their JSON
payloads, but the receipt did not carry the same `max_chars`, `total_chars`,
or `truncated` provenance. That makes downstream completion, learning, and
agentic receipts weaker than the task output body.

### Reference pattern

Claude Code's task output path explicitly formats truncated output for the
model with truncation context. Elnath does not have the same UI/file-output
model, so the Elnath-native fix is to make bounded output metadata receipt
backed and preserve it through completion, learning, and agentic conversion.

### Implementation

Added receipt fields:

- `max_chars`
- `total_chars`
- `truncated`

The fields now appear on daemon `task_output` and `task_monitor` receipts and
are preserved through:

- runtime completion receipt parsing
- learning outcome receipts
- agentic completion context receipts

## Slice 4: control-surface manifest/runtime registration guard

### Problem

`elnath explain control-surfaces` is manifest-backed and already checked
against ToolSearch routing metadata, but it did not have a direct guard that
every manifest-listed tool is actually registered in the runtime registry.

### Implementation

Added `TestExecutionRuntimeRegistersControlSurfaceManifestTools`.

This test creates an execution runtime and verifies every tool listed by
`controlSurfaceManifest()` is present in `rt.reg`.

## Changed files

- `cmd/elnath/commands_help_test.go`
- `cmd/elnath/runtime_test.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_observability_test.go`
- `internal/agentic/completion/gate.go`
- `internal/daemon/task_tools.go`
- `internal/daemon/task_tools_test.go`
- `internal/learning/outcome.go`
- `internal/tools/code_symbols.go`
- `internal/tools/code_symbols_test.go`
- `internal/tools/metadata_test.go`
- `.omc/research/post-pr221-local-batch-command-code-intel-2026-05-14.md`

## Verification

Focused TDD / regression checks:

- `go test ./cmd/elnath -run 'TestVisibleCommandsHaveCommandSpecificHelp|TestExecuteCommand_SubcommandHelpCoverage|TestExecuteCommand_CommandSpecificHelp' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.796s`
- `go test ./internal/tools -run 'TestCodeSymbolsToolDefinitionFindsExactGoSymbol|TestCodeSymbolsToolDefinitionRequiresQuery' -count=1`
  - FAIL before implementation: operation `definition` was unsupported.
- `go test ./internal/tools -run 'TestCodeSymbolsToolDefinitionFindsExactGoSymbol|TestCodeSymbolsToolDefinitionRequiresQuery|TestCodeSymbolsToolDocumentSymbols|TestCodeSymbolsToolWorkspaceSymbolsFiltersQueryAndCaps|TestCodeSymbolsToolMetadata' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/tools 0.802s`
- `go test ./internal/tools -run 'TestBuiltinToolMetadata|TestCodeSymbolsToolDefinitionFindsExactGoSymbol|TestCodeSymbolsToolDefinitionRequiresQuery|TestCodeSymbolsToolMetadata' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/tools 0.716s`
- `go test ./internal/tools -run 'TestCodeSymbolsToolDefinitionFindsMethodByUnqualifiedName' -count=1`
  - FAIL before fallback: `Run` did not match stored `Worker.Run`.
- `go test ./internal/tools -run 'TestCodeSymbolsToolDefinitionFindsExactGoSymbol|TestCodeSymbolsToolDefinitionFindsMethodByUnqualifiedName|TestCodeSymbolsToolDefinitionRequiresQuery|TestBuiltinToolMetadata|TestCodeSymbolsToolMetadata' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/tools 0.691s`
- `go test ./internal/daemon -run 'TestTaskOutputToolReturnsBoundedResultTail|TestTaskOutputToolBlocksUntilTaskCompletes' -count=1`
  - FAIL before implementation: receipt did not expose `MaxChars`,
    `TotalChars`, or `Truncated`.
- `go test ./cmd/elnath -run 'TestTaskOutputControlReceiptFieldsConvertToLearningAndAgentic' -count=1`
  - FAIL before implementation: completion, learning, and agentic receipt
    types did not expose output bound fields.
- `go test ./internal/daemon -run 'TestTaskOutputToolReturnsBoundedResultTail|TestTaskOutputToolBlocksUntilTaskCompletes' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/daemon 0.710s`
- `go test ./cmd/elnath -run 'TestTaskOutputControlReceiptFieldsConvertToLearningAndAgentic' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.591s`
- `go test ./internal/daemon -run 'TestTaskOutputToolReturnsBoundedResultTail|TestTaskOutputToolBlocksUntilTaskCompletes|TestTaskMonitorToolReturnsTerminalResultTail' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/daemon 0.601s`
- `go test ./cmd/elnath -run 'TestExecutionRuntimeRegistersControlSurfaceManifestTools|TestExecutionRuntimeRegistersDeferredControlSurfaceTools|TestControlSurfaceManifestMatchesToolSearchRouting' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.779s`

Affected package checks:

- `go test ./cmd/elnath ./internal/tools -count=1`
  - PASS: `cmd/elnath 19.478s`, `internal/tools 38.199s`
- `go test ./cmd/elnath ./internal/daemon ./internal/agentic/completion ./internal/learning ./internal/tools -count=1`
  - PASS before slice 4: `cmd/elnath 21.550s`, `internal/daemon 35.434s`,
    `internal/agentic/completion 1.013s`, `internal/learning 1.916s`,
    `internal/tools 40.664s`
- `go test ./cmd/elnath ./internal/daemon ./internal/agentic/completion ./internal/learning ./internal/tools -count=1`
  - PASS after slice 4: `cmd/elnath 26.735s`, `internal/daemon 34.525s`,
    `internal/agentic/completion 1.612s`, `internal/learning 1.699s`,
    `internal/tools 42.801s`
- `go vet ./cmd/elnath ./internal/tools`
  - PASS
- `go vet ./cmd/elnath ./internal/daemon ./internal/agentic/completion ./internal/learning ./internal/tools`
  - PASS
- `git diff --check`
  - PASS
- `go test ./... -count=1`
  - PASS
- `go vet ./...`
  - PASS
- `git diff --check HEAD`
  - PASS

## Benchmark / corpus / baseline

- Full v8 benchmark: no
- Current-only smoke: no
- Baseline: no
- Codex CLI comparison: no
- Claude Code comparison: no
- Benchmark corpus mutation: no
- Baseline mutation: no

## Claim boundary

Allowed:

- Visible command help now has a broad regression guard.
- `code_symbols` now supports exact-name Go definition lookup.
- `code_symbols definition` now accepts unqualified method-name lookup for
  receiver methods.
- `task_output` and `task_monitor` output-bound metadata is now receipt-backed
  through runtime, learning, and agentic conversion.
- The control-surface manifest now has a direct runtime-registration drift
  guard.

Forbidden:

- Elnath is complete as a public product.
- Elnath is better than Claude Code or Codex.
- v8 benchmark passed.
- Full LSP lifecycle exists.
- Position-based definition/reference/hover exists.
- UI-level answer collection is complete.

## Remaining risk

- `definition` duplicates some workspace scan logic. A later cleanup can factor
  shared file-walk code if more code-intelligence operations are added.
- This is still symbol-name based, not cursor-position based LSP.
- UI-level answer collection remains outside the current runtime; this batch
  improved runtime provenance, not desktop UX.

## Next autonomous action

Continue local batch mode. Do not open a PR yet. Next likely slice:

- inspect UI-level answer collection boundary and decide if a runtime-only
  improvement exists without desktop/app work.
