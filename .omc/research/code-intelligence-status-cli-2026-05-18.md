# Code Intelligence Status CLI Milestone

Date: 2026-05-18 KST
Branch: `codex/code-intelligence-status`

## Summary

This milestone adds an operator-facing code-intelligence status surface:

- `elnath explain code-intelligence`
- `elnath explain code-intelligence --json`
- `elnath explain code-intelligence --path PATH`
- `elnath explain code-intelligence --max-results N`

The goal is not full multi-language LSP parity. The goal is to make Elnath's existing Go-native code intelligence and mutation diagnostic adapter boundary visible and verifiable from the CLI.

## Reference Files Inspected

Elnath:

- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`
- `internal/tools/code_symbols.go`
- `internal/tools/mutation.go`
- `internal/tools/file.go`
- `cmd/elnath/runtime_completion_observability.go`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`
- `.omc/research/elnath-ultimate-goal-codex-claude-hermes-convergence-2026-05-17.md`

Claude Code source:

- `/Users/stello/claude-code-src/src/services/diagnosticTracking.ts`
- `/Users/stello/claude-code-src/src/services/lsp/LSPDiagnosticRegistry.ts`
- `/Users/stello/claude-code-src/src/tools/FileWriteTool/FileWriteTool.ts`
- `/Users/stello/claude-code-src/src/tools/FileEditTool/FileEditTool.ts`

Hermes source:

- `/Users/stello/.hermes/hermes-agent/agent/lsp/manager.py`
- `/Users/stello/.hermes/hermes-agent/agent/lsp/reporter.py`
- `/Users/stello/.hermes/hermes-agent/tests/agent/lsp/test_diagnostics_field.py`

## Reference Takeaways

- Claude Code tracks diagnostic baselines before edits and delivers only new diagnostics through a separate diagnostic channel.
- Claude Code also clears delivered diagnostics when a file changes so fresh diagnostics can be surfaced again.
- Hermes keeps LSP diagnostics as a separate result field, not folded into syntax lint output.
- Hermes exposes LSP status/diagnostic behavior as an inspectable runtime boundary.
- Elnath already has structured mutation diagnostic receipts and `code_symbols` diagnostics, but the operator-visible status surface was weaker than the model-callable surface.

## Implementation

Added `explain code-intelligence` under `cmd/elnath/cmd_explain.go`.

The command returns:

- product boundary for code intelligence;
- replacement path for the excluded full LSP lifecycle;
- diagnostic adapter policies for Go, Python, TypeScript, and JavaScript;
- live Go diagnostics for a selected path through the existing `code_symbols diagnostics` implementation;
- top repair hints for diagnostic locations with suggested model-callable tools and stop condition;
- a read-only receipt showing that diagnostics were checked.

## Verification

Focused TDD:

- Initial focused test failed with `undefined: explainCodeIntelligence`.
- After implementation:
  - `go test ./cmd/elnath -run 'TestCmdExplainCodeIntelligenceJSONRunsGoDiagnostics|TestExplainCodeIntelligenceText' -count=1` passed.

Related focused checks:

- `go test ./cmd/elnath -run 'Test(CmdExplainCodeIntelligenceJSONRunsGoDiagnostics|ExplainCodeIntelligenceText|ExplainControlSurfaces|ControlSurfaceManifestMatchesToolSearchRouting)' -count=1` passed.

Manual CLI check:

- `go run ./cmd/elnath explain code-intelligence --json --path cmd/elnath --max-results 5` passed.
- Result showed:
  - product boundary present;
  - diagnostic adapters listed;
  - `go_diagnostics.status = success`;
  - `go_diagnostics.count = 0`;
  - `receipt.tool = explain_code_intelligence`;
  - `receipt.read_only = true`;
  - `receipt.diagnostics_checked = true`.

## Claim Boundary

Allowed:

- Elnath now has an operator-facing code-intelligence status CLI.
- The CLI can run live Go diagnostics through the existing code_symbols path.
- The CLI exposes diagnostic adapter policy and product boundaries.
- When diagnostics exist, the CLI can surface bounded repair hints for the top diagnostic locations.

Forbidden:

- Do not claim full multi-language LSP parity.
- Do not claim IDE-grade diagnostic parity with Claude Code.
- Do not claim Hermes LSP parity.
- Do not claim benchmark improvement from this milestone.

## Benchmark Boundary

- Full v8 benchmark: not run.
- Baseline: not run.
- Codex comparison: not run.
- Claude Code comparison: not run.
- Benchmark corpus mutation: no.
- Baseline artifact update: no.

## Remaining Risks

- This adds operator visibility, not a full language-server lifecycle.
- Non-Go diagnostics remain mutation-adapter syntax checks, not always-on semantic LSP.
- The command checks diagnostics on demand and returns repair hints, but it does not change the runtime retry policy.

## Next Recommendation

Continue product/runtime completion with a code-intelligence-to-repair-guidance runtime slice:

- when diagnostics are found inside actual runtime turns, surface the most relevant file/symbol hints into bounded retry receipts;
- keep it closed-enum and receipt-backed;
- do not start benchmark widening from this milestone.
