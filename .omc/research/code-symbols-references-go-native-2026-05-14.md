# Go-native code_symbols references slice

Date: 2026-05-14
Branch: `codex/post-pr222-code-intel`
Status: local implementation slice

## Problem

The final completion control document lists code intelligence as a remaining high-ROI boundary.

After PR #222, Elnath had:

- `code_symbols document_symbols`
- `code_symbols workspace_symbols`
- `code_symbols definition`

But it still lacked a reference lookup surface. That leaves the agent dependent on broader shell/grep habits for a common navigation task.

## Reference inspected

Claude Code source inspected:

- `/Users/stello/claude-code-src/src/tools/LSPTool/schemas.ts`
- `/Users/stello/claude-code-src/src/tools/LSPTool/prompt.ts`
- `/Users/stello/claude-code-src/src/tools/LSPTool/LSPTool.ts`
- `/Users/stello/claude-code-src/src/tools/LSPTool/formatters.ts`

Reference pattern used:

- expose code-intelligence operations as read-only, deferred tools
- include `findReferences` alongside definition and symbol lookup
- return bounded, file/line-oriented locations

Elnath-native choice:

- do not claim full LSP lifecycle
- keep `code_symbols` Go-native and read-only
- add a query-based `references` operation using Go AST identifier positions
- preserve existing JSON/receipt shape

## Changed files

- `internal/tools/code_symbols.go`
- `internal/tools/code_symbols_test.go`
- `internal/tools/tool_search_test.go`
- `internal/tools/metadata_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`

## Behavior added

- `code_symbols` schema now accepts `operation: "references"`.
- `references` requires `query`.
- Query `Worker.Run` maps to identifier `Run`, so method calls such as `worker.Run()` can be found without full type-aware LSP.
- When `query` is omitted, `definition` and `references` can derive the target identifier from `file_path`, `line`, and `column`.
- `code_symbols` schema now accepts `operation: "hover"`.
- `hover` reuses definition lookup to return Go-native signature/location information for a cursor-derived or query-derived symbol.
- Results are bounded by `max_results`.
- Results preserve:
  - `operation`
  - `status`
  - `path`
  - `query`
  - `count`
  - `truncated`
  - read-only receipt metadata
- Gitignored Go files continue to be skipped through the existing candidate filter.
- Parse errors remain `partial_success`.
- `elnath explain control-surfaces` now names symbols/definitions/references/hover instead of stale generic symbol lookup wording.
- `tool_search` regression coverage now proves `code_symbols` advertises `references` and `hover` through deferred schema preview.

## Verification

TDD expected failure before implementation:

- `go test ./internal/tools -run 'TestCodeSymbolsToolReferencesFindsGoIdentifierUses|TestCodeSymbolsToolReferencesRequiresQuery' -count=1`
  - FAIL before code change: `operation must be document_symbols, workspace_symbols, or definition`

Focused verification after implementation:

- `go test ./internal/tools -run 'TestCodeSymbolsToolReferencesFindsGoIdentifierUses|TestCodeSymbolsToolReferencesRequiresQuery|TestCodeSymbolsToolDefinitionFindsExactGoSymbol|TestCodeSymbolsToolDefinitionFindsMethodByUnqualifiedName|TestCodeSymbolsToolMetadata' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/tools 0.660s`
- `go test ./internal/tools -run 'TestToolSearchFindsCodeSymbolsAsDeferredCodeIntelligence|TestCodeSymbolsToolReferencesFindsGoIdentifierUses|TestCodeSymbolsToolReferencesRequiresQuery' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/tools 0.617s`
- `go test ./internal/tools -run 'TestCodeSymbolsToolReferencesDerivesQueryFromCursor|TestCodeSymbolsToolDefinitionDerivesQueryFromCursor|TestCodeSymbolsToolReferencesFindsGoIdentifierUses|TestCodeSymbolsToolReferencesRequiresQuery|TestCodeSymbolsToolDefinitionRequiresQuery|TestToolSearchFindsCodeSymbolsAsDeferredCodeIntelligence' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/tools 0.599s`
- `go test ./internal/tools -run 'TestCodeSymbolsToolHoverReturnsDefinitionSignatureFromCursor|TestCodeSymbolsToolReferencesDerivesQueryFromCursor|TestCodeSymbolsToolDefinitionDerivesQueryFromCursor|TestCodeSymbolsToolDefinitionRequiresQuery|TestCodeSymbolsToolReferencesRequiresQuery|TestToolSearchFindsCodeSymbolsAsDeferredCodeIntelligence' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/tools 0.648s`

Affected package verification:

- `go test ./internal/tools -count=1`
  - PASS: `ok github.com/stello/elnath/internal/tools 38.060s`
- `go vet ./internal/tools`
  - PASS
- `git diff --check`
  - PASS

Control-surface wording verification:

- `go test ./cmd/elnath -run 'TestExplainControlSurfacesJSON|TestExplainControlSurfacesText|TestControlSurfaceManifestMatchesToolSearchRouting' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.555s`
- `go test ./cmd/elnath -run 'TestExplainControlSurfacesJSON|TestExplainControlSurfacesText|TestControlSurfaceManifestMatchesToolSearchRouting' -count=1 && go test ./internal/tools -run 'TestCodeSymbolsToolHoverReturnsDefinitionSignatureFromCursor|TestToolSearchFindsCodeSymbolsAsDeferredCodeIntelligence' -count=1`
  - PASS:
    - `ok github.com/stello/elnath/cmd/elnath 0.662s`
    - `ok github.com/stello/elnath/internal/tools 0.517s`
- `go test ./internal/tools -run 'TestBuiltinToolMetadata|TestCodeSymbolsToolHoverReturnsDefinitionSignatureFromCursor|TestToolSearchFindsCodeSymbolsAsDeferredCodeIntelligence' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/tools 0.639s`

Regression found and fixed during affected package verification:

- `TestBuiltinToolMetadata/code_symbols_unknown_operation_falls_back` used `hover` as the unknown operation.
- `hover` is now a supported operation, so the test fixture was updated to use `call_hierarchy` as the explicit unsupported operation sample.

Affected package verification after all local edits:

- `go test ./cmd/elnath ./internal/tools -count=1`
  - PASS:
    - `ok github.com/stello/elnath/cmd/elnath 19.312s`
    - `ok github.com/stello/elnath/internal/tools 36.812s`
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

- Elnath now has a small Go-native `code_symbols references` operation for identifier locations.
- This improves code-intelligence coverage after PR #222.

Not allowed:

- Full LSP lifecycle is complete.
- Elnath has Claude Code-equivalent LSP behavior.
- Benchmark readiness or superiority is proven.

## Remaining risk

- `references` is AST identifier based, not type-resolved.
- `Worker.Run` maps to `Run`, so unrelated `Run` identifiers can appear in large workspaces.
- Cursor-derived query also extracts the visible identifier only; it does not resolve receiver type.
- `hover` is signature/location hover, not documentation or type-inference hover.
- No diagnostics, implementation, or call hierarchy support is added.

## Next autonomous action

Continue local batch-first. Candidate next slice:

- add a small registry/status check that exposes `code_symbols references` in `tool_search`/control-surface evidence, or
- add another code-intelligence operation only if current code evidence shows clear value.

Do not open a PR until the local code-intelligence/control-surface batch is coherent.
