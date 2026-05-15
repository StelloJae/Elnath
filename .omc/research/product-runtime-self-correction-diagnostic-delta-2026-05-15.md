# Product Runtime Self-Correction Diagnostic Delta Milestone - 2026-05-15

## Summary

Milestone: product/runtime bounded self-correction, diagnostic-delta gap.

Branch: `codex/product-runtime-self-correction`

Base: `44921cdd7701219dd191b3c7f94cecb9cd764348`

PR: not opened yet.

Benchmark: not run.

Corpus/baseline mutation: none.

## Control Documents

- `/Users/stello/elnath/.omc/research/elnath-product-runtime-100-control-2026-05-15.md`
- `/Users/stello/elnath/.omc/research/product-runtime-doctor-install-hardening-2026-05-15.md`

## Reference Files Inspected

Elnath:

- `/Users/stello/elnath-worktrees/product-runtime-self-correction/cmd/elnath/runtime_completion_observability.go`
- `/Users/stello/elnath-worktrees/product-runtime-self-correction/cmd/elnath/runtime_completion_retry.go`
- `/Users/stello/elnath-worktrees/product-runtime-self-correction/cmd/elnath/runtime_completion_gate_context.go`
- `/Users/stello/elnath-worktrees/product-runtime-self-correction/internal/agentic/completion/gate.go`
- `/Users/stello/elnath-worktrees/product-runtime-self-correction/internal/tools/code_symbols.go`

Claude Code source flow reference:

- `/Users/stello/claude-code-src/src/services/diagnosticTracking.ts`
- `/Users/stello/claude-code-src/src/tools/FileWriteTool/FileWriteTool.ts`
- `/Users/stello/claude-code-src/src/tools/FileEditTool/FileEditTool.ts`
- `/Users/stello/claude-code-src/src/utils/attachments.ts`

Hermes source flow reference:

- `/Users/stello/.hermes/hermes-agent/tools/file_operations.py`
- `/Users/stello/.hermes/hermes-agent/tests/agent/lsp/test_diagnostics_field.py`

Claw-code source flow reference:

- `/Users/stello/claw-code/rust/crates/runtime/src/lsp_client.rs`
- `/Users/stello/claw-code/rust/crates/tools/src/lib.rs`
- `/Users/stello/claw-code/rust/PARITY.md`

## Finding

Elnath already had strong bounded self-correction:

- no-op edit detection
- incomplete final response detection
- verification-not-run detection
- verification failure classification
- scope-drift fail-closed behavior
- closed-enum retry decisions
- max retry attempts
- correction attempt receipts

Remaining gap:

- `code_symbols diagnostics_delta` could report `new_diagnostics_found`, but completion observability did not treat that as a completion warning or retry reason.
- This meant a model could introduce new Go diagnostics, see the `code_symbols` result, and still end with a success-style final answer without the completion gate carrying the diagnostic-delta warning.

## Implementation

Added diagnostic delta receipt flow:

- completion summary now records `diagnostic_delta_receipts`.
- `code_symbols diagnostics_delta` result with `new_diagnostics_found` or `new_diagnostic_count > 0` sets completion warning `new_diagnostics_found`.
- `new_diagnostics_found` maps to closed-enum retry decision `retry_smaller_scope`.
- retry prompt gives narrow guidance: inspect diagnostic delta, patch only introduced issue, rerun focused diagnostic or verification.
- completion context, agentic completion gate summary, and learning outcome record now persist diagnostic delta receipts.

Changed files:

- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_retry.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_completion_observability_test.go`
- `internal/agentic/completion/gate.go`
- `internal/agentic/completion/gate_test.go`
- `internal/learning/outcome.go`
- `internal/learning/outcome_store_test.go`

## Verification

Focused checks:

- `go test ./cmd/elnath -run 'TestCompletionContractSummaryDetectsNewDiagnosticDelta|TestCompletionRetryPromptGuidesNewDiagnosticDelta|TestCompletionGateContextProviderConsumesRuntimeSummary|TestExecutionRuntimeRecordsCompletionOutcome|TestCompletionRetryEscalatesAutoEffort|TestCompletionRetryFailsClosedOnScopeDrift|TestCompletionRetryRunsExplicitVerificationCommand' -count=1` -> PASS
- `go test ./internal/agentic/completion -run 'TestCompletionGate_ReceiptSummaryIncludesOptionalCompletionContext' -count=1` -> PASS
- `go test ./internal/learning -run 'TestOutcomeRecordCompletionObservabilityJSONCompatibility|TestOutcomeStoreAppend_PreservesExtendedFields' -count=1` -> PASS

Broader proportional checks:

- `go test ./cmd/elnath ./internal/agentic/completion ./internal/learning ./internal/tools -count=1` -> PASS
- `git diff --check` -> PASS
- `go test ./internal/... ./cmd/elnath -count=1` -> PASS
- `go vet ./...` -> PASS

## Claim Boundary

Allowed:

- Elnath now detects `code_symbols diagnostics_delta` new diagnostics as a completion warning.
- Elnath now routes that warning into bounded `retry_smaller_scope` self-correction.
- Completion gate and learning outcome receipts now preserve diagnostic delta evidence.

Forbidden:

- This does not claim full v8 benchmark success.
- This does not claim Elnath beats Claude Code, Codex, or Hermes.
- This does not claim full LSP parity.
- This does not run or mutate benchmark corpus/baseline.

## Remaining Risk

- Diagnostic delta detection depends on the model/tool flow invoking `code_symbols diagnostics_delta`.
- Full automatic pre-edit baseline capture for every write/edit is still not implemented; this milestone closes the completion-gate signal path for existing diagnostic-delta tool results.
- Non-Go diagnostics remain outside current `code_symbols` coverage unless future code-intelligence milestone adds broader language adapters.

## Next Recommendation

Finish this milestone with broad proportional checks, then batch into one PR if clean. After merge, continue product/runtime completion with the next structural blocker from the control document, likely product readiness closeout or code-intelligence expansion depending on audit results.
