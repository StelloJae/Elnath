# Product runtime code diagnostics delta milestone - 2026-05-15

## Summary

Branch: `codex/product-runtime-code-intel`

This milestone advances the product/runtime 100% program's code-intelligence
gate. It adds an Elnath-native, Go-parser based diagnostic delta path instead
of running benchmark loops to discover patch mistakes.

The implementation does not claim full LSP parity. It gives Elnath a
receipt-backed way to compare pre-edit and post-edit Go diagnostics, account
for line shifts, and distinguish existing, new, and resolved diagnostics.

## Control document

- `/Users/stello/elnath/.omc/research/elnath-product-runtime-100-control-2026-05-15.md`

Relevant gate:

- Milestone 3 - edit-aware diagnostics and code intelligence

## References inspected

Elnath:

- `/Users/stello/elnath-worktrees/product-runtime-code-intel/internal/tools/code_symbols.go`
- `/Users/stello/elnath-worktrees/product-runtime-code-intel/internal/tools/code_symbols_test.go`
- `/Users/stello/elnath-worktrees/product-runtime-code-intel/cmd/elnath/cmd_explain.go`
- `/Users/stello/elnath/.omc/research/code-symbols-go-diagnostics-2026-05-14.md`
- `/Users/stello/elnath/.omc/research/code-symbols-references-go-native-2026-05-14.md`
- `/Users/stello/elnath/.omc/research/post-pr221-local-batch-command-code-intel-2026-05-14.md`

Claude Code reference:

- `/Users/stello/claude-code-src/src/services/diagnosticTracking.ts`
- `/Users/stello/claude-code-src/src/services/lsp/LSPDiagnosticRegistry.ts`
- `/Users/stello/claude-code-src/src/tools/LSPTool/LSPTool.ts`

Hermes reference:

- `/Users/stello/elnath/.omc/research/hermes-agent-update-delta-2026-05-15.md`
- `/Users/stello/.hermes/hermes-agent/agent/lsp/range_shift.py` from `origin/main`

## Behavior added

Changed files:

- `internal/tools/code_symbols.go`
- `internal/tools/code_symbols_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`

Added to `code_symbols`:

- operation: `diagnostics_delta`
- input: `baseline_file_path` and current `file_path`
- Go parse diagnostics now include:
  - `severity`
  - `source`
  - `line_text`
  - stable short `fingerprint`
- line-shift mapping from baseline source to current source
- diagnostic classification:
  - `existing_shifted`
  - `new`
  - `resolved`
- output field: `diagnostic_delta`
- receipt counts:
  - `new_diagnostic_count`
  - `existing_diagnostic_count`
  - `resolved_diagnostic_count`

Operator surface update:

- `elnath explain control-surfaces --json` now advertises edit-aware diagnostic
  deltas in the `code_intelligence` note.

## Design boundary

This is Go-native. It does not start or manage an LSP server.

The chosen product path for this slice:

- use `code_symbols diagnostics` for current syntax diagnostics;
- use `code_symbols diagnostics_delta` when a pre-edit baseline file is
  available;
- keep full multi-language LSP lifecycle deferred until explicitly designed or
  excluded by product decision.

## Verification

TDD RED:

```bash
go test ./internal/tools -run 'TestCodeSymbolsToolDiagnosticsDelta' -count=1
```

Result: FAIL as expected. The test referenced the new `DiagnosticDelta` output
and receipt fields before implementation existed.

Focused GREEN:

```bash
go test ./internal/tools -run 'TestCodeSymbolsToolDiagnosticsDelta|TestCodeSymbolsToolDiagnosticsReportsGoParseErrors' -count=1
```

Result: PASS.

Package checks:

```bash
go test ./internal/tools -count=1
```

Result: PASS (`ok github.com/stello/elnath/internal/tools 44.406s`).

```bash
go test ./cmd/elnath -run 'TestExplainControlSurfaces' -count=1
```

Result: PASS (`ok github.com/stello/elnath/cmd/elnath 0.698s`).

```bash
go test ./internal/tools ./cmd/elnath -count=1
```

Result: PASS.

- `internal/tools`: PASS (`39.520s`)
- `cmd/elnath`: PASS (`20.847s`)

Broad proportional checks:

```bash
git diff --check
```

Result: PASS.

```bash
go test ./internal/... ./cmd/elnath -count=1
```

Result: PASS.

```bash
go vet ./...
```

Result: PASS.

## Benchmark boundary

No benchmark run was performed.

No full v8 benchmark.
No baseline.
No Codex comparison.
No Claude Code comparison.
No benchmark superiority claim.
No benchmark corpus mutation.
No baseline artifact mutation.

## Active PR state observed

- PR #226: OPEN / CLEAN; Bubblewrap PASS; Seatbelt PASS.
- PR #227: OPEN / UNSTABLE; Seatbelt PASS; Bubblewrap FAIL.

PR #227 should be fixed before opening another product-runtime PR.

## Remaining risk

- `diagnostics_delta` requires a captured baseline file. The runtime still needs
  a higher-level integration that captures pre-edit snapshots automatically
  before write/edit operations.
- The line-shift path is Go-native and source-text based. It is not semantic
  type checking and not multi-language LSP.
- Large files fall back to prefix/suffix line-shift behavior to avoid excessive
  memory use.

## Next autonomous action

Fix PR #227 Bubblewrap CI first, because it is already open and failing.

After PR #227 is clean, continue this code-intelligence lane by wiring
diagnostic delta capture into bounded self-correction receipts, or batch this
slice with that follow-up before opening a new PR.
