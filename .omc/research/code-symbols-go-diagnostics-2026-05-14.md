# Code Symbols Go Diagnostics - 2026-05-14

## Summary

Branch: `codex/post-pr223-registry-introspection`
PR: none
Commit: none yet

This milestone adds a small Go-native diagnostics operation to `code_symbols`.
It does not claim full LSP lifecycle parity. It gives Elnath a cheap code-intelligence
self-check for Go syntax errors through the existing read-only deferred tool surface.

## References Inspected

- `/Users/stello/claude-code-src/src/services/lsp/LSPDiagnosticRegistry.ts`
- `/Users/stello/claude-code-src/src/services/lsp/passiveFeedback.ts`
- `/tmp/elnath-registry-introspection.dxxVoH/internal/tools/code_symbols.go`
- `/tmp/elnath-registry-introspection.dxxVoH/internal/tools/code_symbols_test.go`
- `/tmp/elnath-registry-introspection.dxxVoH/cmd/elnath/cmd_explain.go`

Claude Code uses an optional LSP diagnostic registry and async delivery path. Elnath does not
implement that full lifecycle here. This slice adapts the useful behavior at Elnath's current
level: direct Go parser diagnostics with receipt-backed output.

## Changed Files

- `internal/tools/code_symbols.go`
- `internal/tools/code_symbols_test.go`
- `internal/tools/tool_search_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`

## Behavior Added

- Adds `code_symbols` operation `diagnostics`.
- Supports workspace diagnostics through `path` or file-specific diagnostics through `file_path`.
- Reports Go parser diagnostics with `file_path`, `line`, `column`, and `error`.
- Keeps the tool read-only, deferred, and receipt-backed.
- Updates ToolSearch and control-surface metadata so the capability is discoverable.

## Verification

TDD probe before implementation:

```text
go test ./internal/tools ./cmd/elnath -run 'TestCodeSymbolsToolDiagnosticsReportsGoParseErrors|TestToolSearchFindsCodeSymbolsAsDeferredCodeIntelligence|TestExplainControlSurfacesJSON' -count=1
FAIL as expected: diagnostics operation and control-surface note were missing
```

Focused verification after implementation:

```text
gofmt -w internal/tools/code_symbols.go internal/tools/code_symbols_test.go internal/tools/tool_search_test.go cmd/elnath/cmd_explain.go cmd/elnath/cmd_explain_test.go
go test ./internal/tools ./cmd/elnath -run 'TestCodeSymbolsToolDiagnosticsReportsGoParseErrors|TestToolSearchFindsCodeSymbolsAsDeferredCodeIntelligence|TestExplainControlSurfacesJSON' -count=1
PASS
```

Package verification:

```text
go test ./internal/tools ./cmd/elnath -count=1
PASS (ok github.com/stello/elnath/internal/tools 45.112s; ok github.com/stello/elnath/cmd/elnath 27.877s)

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

- `code_symbols diagnostics` reports Go parser diagnostics.
- ToolSearch and control-surface metadata now expose the diagnostics capability.

Forbidden:

- Full LSP lifecycle exists.
- Async diagnostic delivery exists.
- Elnath completion is proven.
- Benchmark readiness is proven.
- Elnath is better than Claude Code or Codex.

## Remaining Risk

This is syntax-level Go diagnostics only. It is not semantic type checking, multi-language LSP,
or cross-turn async diagnostic delivery.

## Next Recommendation

Commit this as the next local code-intelligence slice in the current batch.
