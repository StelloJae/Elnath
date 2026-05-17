# TypeScript/JavaScript Mutation Diagnostic Adapter Design

Date: 2026-05-17 KST

Branch: `codex/typescript-diagnostic-adapter`

Goal lane:

- Codex-Claude-Hermes convergence
- Product/runtime first
- Benchmark second
- Current slice: semantic diagnostics on write, TypeScript/JavaScript syntax
  adapter

## Summary

Elnath already records post-write mutation receipts and Go/Python syntax
diagnostic deltas. TypeScript/JavaScript currently reports only
`diagnostics_not_configured`.

This slice should add a conservative TypeScript-family syntax adapter without
claiming full LSP or project-wide semantic parity.

## References Inspected

Elnath:

- `internal/tools/mutation.go`
- `internal/tools/file.go`
- `internal/tools/file_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`
- `.omc/research/elnath-ultimate-goal-codex-claude-hermes-convergence-2026-05-17.md`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`

Claude Code source:

- `/Users/stello/claude-code-src/src/tools/FileWriteTool/FileWriteTool.ts`
- `/Users/stello/claude-code-src/src/services/diagnosticTracking.ts`

Hermes source/docs:

- `/Users/stello/.hermes/hermes-agent/website/docs/user-guide/features/lsp.md`
- `/Users/stello/.hermes/hermes-agent/agent/lsp/manager.py`

## Reference Lessons

Claude Code:

- captures a diagnostics baseline before edit;
- writes the file;
- notifies LSP/IDE;
- compares post-edit diagnostics against baseline;
- reports only newly introduced diagnostics.

Hermes:

- layers syntax checks before LSP diagnostics;
- gates LSP on real workspace detection;
- keeps LSP failures non-fatal to writes;
- records missing or flaky diagnostics as runtime policy, not edit failure;
- installs TypeScript LSP with `typescript` peer dependency in a Hermes-owned
  location.

Elnath-safe adaptation:

- keep Elnath's existing mutation receipt and diagnostic delta shape;
- do not spawn long-lived LSP in this slice;
- do not run broad `tsc --noEmit` by default because it is project-wide,
  config-dependent, slower, and can report unrelated diagnostics;
- use best-effort local TypeScript parser when available;
- fallback explicitly to `diagnostics_not_configured`;
- no benchmark run required.

## Chosen Design

Add a TypeScript-family syntax diagnostic adapter using:

- command: `node`
- parser: locally resolvable `typescript` module
- API: `typescript.transpileModule(..., reportDiagnostics: true)`
- scope: syntax/options diagnostics only
- timeout: existing `defaultMutationDiagnosticTimeout` = 2000 ms
- source handling: write source to a temp file outside repo, pass original
  display path as `fileName`
- result shape: reuse `codeSymbolError` and existing mutation delta comparator

The adapter runs for `.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`, and `.cjs`.

If `node` is unavailable or `require("typescript")` fails from the benchmark or
project base path, mutation diagnostics remain:

- language: `typescript` or `javascript`
- status: `diagnostics_not_configured`
- counts: zero

## Non-Goals

- no full TypeScript semantic diagnostics;
- no `typescript-language-server`;
- no auto-install;
- no `tsc --noEmit`;
- no JavaScript semantic adapter beyond the shared TypeScript parser syntax
  path;
- no benchmark rerun;
- no superiority claim.

## Acceptance

Focused tests should prove:

- invalid TypeScript/JavaScript can produce `new_diagnostics_found` when a local
  adapter is available;
- missing adapter produces `diagnostics_not_configured`;
- policy/explain output shows TypeScript/JavaScript as conditional rather than
  silently unsupported;
- Python and Go behavior remains unchanged.

## Claim Boundary

Allowed after this slice if verified:

- Elnath has a best-effort TypeScript/JavaScript mutation syntax diagnostic
  adapter.
- The adapter uses local TypeScript tooling only when available.
- Missing TypeScript tooling remains explicit and non-fatal.

Forbidden:

- full TypeScript LSP parity;
- semantic type-checking parity;
- broad JavaScript semantic diagnostics;
- benchmark readiness improvement claim without separate smoke evidence.

## Implementation Result

Changed files:

- `internal/tools/mutation.go`
- `internal/tools/file_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`

Behavior added:

- TypeScript and JavaScript mutation diagnostics now attempt a best-effort
  temp-file syntax diagnostic delta through `node` and a locally resolvable
  `typescript` module.
- The adapter keeps the existing 2000 ms mutation diagnostic timeout.
- The adapter uses the project/base path for module resolution and a temp file
  for source input.
- Missing `node`, missing `typescript`, command failure, timeout, or malformed
  output falls back to an explicit diagnostic policy status rather than breaking
  file writes.
- `explain control-surfaces` now reports TypeScript and JavaScript as
  conditional `typescript/transpileModule` adapters when `node` is present, or
  `diagnostics_not_configured` when unavailable.

Behavior not added:

- no `tsc --noEmit`;
- no TypeScript language server;
- no JavaScript semantic diagnostics;
- no auto-install;
- no benchmark run.

## Verification

Commands run from
`/Users/stello/elnath/.worktrees/typescript-diagnostic-adapter`:

- `go test ./internal/tools -run 'TestWriteToolRecords(TypeScript|JavaScript|NonGo|Python|Go)Diagnostic|TestEditToolRecordsMutationReceipt' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/tools 1.424s`
- `go test ./cmd/elnath -run 'TestExplainControlSurfaces' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.707s`
- `go test ./internal/tools ./cmd/elnath -count=1`
  - PASS: `internal/tools 40.439s`, `cmd/elnath 18.635s`
- `go test ./internal/... ./cmd/elnath -count=1`
  - PASS: all listed internal packages and `cmd/elnath`
- `go vet ./...`
  - PASS
- `git diff --check`
  - PASS

## Remaining Risk

- This is syntax/options-level diagnostics only.
- Real TypeScript/JavaScript diagnostics require the edited project to have a
  resolvable `typescript` module or equivalent local/global module.
- Full Hermes-style `typescript-language-server` lifecycle remains a later
  convergence milestone.

## Next Recommendation

Next structural blocker:

- turn this adapter into a committed milestone and PR-ready batch;
- then continue Lane 3 toward an explicit LSP/service design decision rather
  than widening benchmark runs.
