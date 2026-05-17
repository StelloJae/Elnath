# Diagnostic Repair Hints Runtime Milestone

Date: 2026-05-18 KST

Branch:

- `codex/diagnostic-repair-hints`

Status:

- local implementation verified
- PR not opened yet

## Purpose

Product/runtime completion first, benchmark second.

This milestone closes the remaining gap from the code-intelligence status CLI
slice: diagnostics were visible to operators, but runtime retry/receipt paths did
not yet carry concrete repair hints.

## References Inspected

Elnath:

- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_retry.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `cmd/elnath/runtime.go`
- `internal/learning/outcome.go`
- `internal/agentic/completion/gate.go`
- `internal/tools/code_symbols.go`
- `internal/tools/mutation.go`
- `internal/agent/mutation_footer.go`

Claude Code source:

- `/Users/stello/claude-code-src/src/services/diagnosticTracking.ts`

Hermes source:

- `/Users/stello/.hermes/hermes-agent/agent/lsp/reporter.py`

Control documents:

- `/Users/stello/elnath/.omc/research/elnath-ultimate-goal-codex-claude-hermes-convergence-2026-05-17.md`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`

## Behavior Added

- Added structured `diagnostic_repair_hints` to completion summaries.
- Extracts hints from:
  - `code_symbols diagnostics_delta` JSON output;
  - filesystem mutation verifier footer `new_diag_N=...`;
  - structured `tools.FileMutation.NewDiagnostics`.
- Each hint records:
  - file path;
  - line and column;
  - diagnostic source;
  - error text;
  - source tool;
  - suggested bounded repair tools;
  - stop condition `diagnostic_delta_clean_or_no_new_diagnostics`.
- Retry prompt for `new_diagnostics_found` now includes concrete top diagnostic
  locations instead of only generic guidance.
- Learning outcome records persist `diagnostic_repair_hints`.
- Agentic completion gate context and summary payload include
  `diagnostic_repair_hints`.

## Changed Files

- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_retry.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_completion_observability_test.go`
- `internal/learning/outcome.go`
- `internal/agentic/completion/gate.go`
- `internal/agentic/completion/gate_test.go`
- `.omc/research/diagnostic-repair-hints-runtime-2026-05-18.md`

## Verification

Passed:

- `go test ./cmd/elnath -run 'TestCompletionContractSummaryDetects(NewDiagnosticDelta|MutationVerifierNewDiagnostics|StructuredMutationNewDiagnostics)|TestCompletionRetryPromptGuidesNewDiagnosticDelta|TestRecordOutcomePersistsCompletionObservability|TestCompletionGateContextProviderConsumesRuntimeSummary|TestCompletionGateReceiptSummaryIncludesRuntimeContext' -count=1`
- `go test ./internal/agentic/completion -run TestCompletionGate_ReceiptSummaryIncludesOptionalCompletionContext -count=1`
- `go test ./cmd/elnath -count=1`
- `go test ./internal/learning ./internal/agentic/completion -count=1`
- `go vet ./...`
- `git diff --check`

## Benchmark / Corpus Boundary

- Benchmark run: no
- Full v8: no
- Baseline: no
- Codex/Claude comparison: no
- Corpus changed: no
- Baseline artifact changed: no

## Claim Boundary

Allowed:

- Elnath runtime now carries concrete diagnostic repair hints for newly
  introduced diagnostics when available.
- `new_diagnostics_found` retry guidance can point to the first concrete
  introduced diagnostic.
- The hints are receipt-backed through learning outcomes and agentic completion
  gate summaries.

Forbidden:

- Full multi-language LSP parity.
- IDE-grade diagnostic parity with Claude Code.
- Hermes v0.14 LSP parity.
- Benchmark success.
- Elnath superiority over Codex, Claude Code, or Hermes.

## Remaining Risk

- The current strong path is still adapter-driven. Go is strongest; Python,
  TypeScript, and JavaScript depend on available local syntax adapters rather
  than a full always-on language server lifecycle.
- Retry remains bounded. This does not claim broad silent self-healing.

## Next Recommendation

Next structural blocker should stay product/runtime-first:

1. widen diagnostic adapter lifecycle only where local tools are reliable, or
2. move to session handoff/resume recap if daily-driver continuity is higher
   leverage.

Do not return to full benchmark loops from this milestone.
