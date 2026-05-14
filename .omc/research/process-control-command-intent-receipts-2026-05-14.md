# Process Control Command Intent Receipts

Date: 2026-05-14
Lane: final completion program
Branch: `codex/final-followup-structural`
Status: locally verified

## Problem

PR #206 made `process_start`, `process_monitor`, and `process_stop` receipts
carry command intent metadata:

- `command_intent`
- `intent_source`

PR #207 preserved the same intent metadata for bash completion receipts. The
process control-tool receipt path still drops the fields when completion
observability reads tool results into `ControlToolReceipts`.

This creates a receipt gap for long-running commands. A process can be started
as `focused_verify`, `background`, or another closed enum intent, but the final
completion gate and learning outcome cannot see that intent.

## References Checked

- Elnath:
  - `internal/tools/process_tools.go`
  - `cmd/elnath/runtime_completion_observability.go`
  - `cmd/elnath/runtime_completion_gate_context.go`
  - `cmd/elnath/runtime.go`
  - `internal/agentic/completion/gate.go`
  - `internal/learning/outcome.go`
- Claude Code source:
  - `src/utils/queryHelpers.ts`
  - `src/screens/REPL.tsx`
- Hermes:
  - `cron/scheduler.py`
- claw-code:
  - `rust/crates/runtime/src/bash.rs`
  - `PARITY.md`

## Chosen Design

Add process command intent metadata to the generic completion control receipt:

- `command_intent`
- `intent_source`

Behavior boundary:

- no retry policy change
- no process execution behavior change
- no benchmark run
- preserve existing JSON field names already emitted by `process_tools.go`

## Verification Plan

Focused tests:

- completion summary records process command intent metadata
- learning conversion preserves process command intent metadata
- agentic conversion preserves process command intent metadata

Broader proportional checks:

- `go test ./cmd/elnath -run 'TestCompletionContractSummaryRecordsProcessToolReceipts|TestProcessControlReceiptsConvertToLearningAndAgentic' -count=1`
- `go test ./cmd/elnath ./internal/agentic/completion ./internal/learning -count=1`
- `git diff --check`

## Implementation

Changed files:

- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_observability_test.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `cmd/elnath/runtime.go`
- `internal/agentic/completion/gate.go`
- `internal/agentic/completion/gate_test.go`
- `internal/learning/outcome.go`
- `internal/learning/outcome_store_test.go`

Behavior added:

- completion control tool receipts now preserve `command_intent` and
  `intent_source`
- process receipt parsing trims and keeps the fields already emitted by
  `process_start`, `process_monitor`, and `process_stop`
- learning outcome conversion preserves the fields
- agentic completion gate conversion preserves the fields
- agentic completion gate receipt-summary JSON includes the fields
- learning outcome JSON includes the fields

No process execution behavior changed.
No benchmark run was performed.
No corpus or baseline artifact was changed.

## Verification Results

Passed:

- `go test ./cmd/elnath -run 'TestCompletionContractSummaryRecordsProcessToolReceipts|TestProcessControlReceiptsConvertToLearningAndAgentic' -count=1`
  - result: PASS
  - package time: `0.826s`
- `go test ./internal/agentic/completion -run TestCompletionGate_ReceiptSummaryIncludesOptionalCompletionContext -count=1`
  - result: PASS
  - package time: `0.300s`
- `go test ./internal/learning -run TestOutcomeRecordCompletionObservabilityJSON -count=1`
  - result: PASS
  - package time: `1.085s`
- `go test ./cmd/elnath ./internal/agentic/completion ./internal/learning -count=1`
  - result: PASS
  - package times: `cmd/elnath 21.400s`, `internal/agentic/completion 1.148s`, `internal/learning 0.874s`
- `git diff --check`
  - result: PASS

## Remaining Risks

- This slice preserves process intent metadata, but does not add new retry
  decisions for long-running command failures.
- Long-running process timeout/abort behavior is tested elsewhere; this slice
  only covers completion receipt propagation.

## Next Recommendation

Open one coherent PR for this process receipt propagation slice. After merge,
continue the final completion program with the next structural blocker. Do not
resume full v8 or baseline lanes yet.

## Claim Boundary

Allowed after verification:

- process command intent metadata survives completion summary, agentic gate
  context, and learning outcome conversion.

Forbidden:

- benchmark success claim
- full v8 readiness claim
- superiority claim versus Claude Code or Codex
- claim that long-running command supervision is complete
