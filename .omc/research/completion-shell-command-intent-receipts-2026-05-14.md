# Completion Shell Command Intent Receipts

Date: 2026-05-14
Lane: final completion program
Branch: `codex/completion-next-structural`
Status: locally verified

## Problem

PR #206 made shell/process execution intent explicit at the tool layer:

- `execution_policy`
- `command_intent`
- `intent_source`

The completion observability layer still records only heuristic
`command_class` for bash receipts. That means the final completion summary,
agentic completion gate context, and learning outcome can lose the model's
declared execution intent even though the tool result already contains it.

This is a structural receipt gap, not a benchmark-task issue. If final claims
and retry decisions are supposed to be receipt-backed, execution intent must
survive from tool result to completion summary and persisted outcome.

## References Checked

- Elnath:
  - `internal/tools/bash.go`
  - `internal/tools/bash_output.go`
  - `internal/tools/command_intent.go`
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

## Reference Pattern

Claude Code tracks bash-tool usage from message history and carries it into
later UI/session behavior instead of treating command use as throwaway output.
Hermes records long-running agent activity and timeout state as operational
diagnostics. claw-code's bash output model exposes structured command-result
fields such as background status, interruption, sandbox status, timeout, and
return-code interpretation.

Elnath already has a stronger structured `BASH RESULT` header. The missing part
is propagation into the completion receipt stack.

## Chosen Design

Add the new tool-level fields to completion shell command receipts:

- `execution_policy`
- `command_intent`
- `intent_source`

Behavior boundary:

- Keep existing heuristic `command_class` for compatibility.
- Preserve legacy BASH RESULT parsing when the new fields are absent.
- Do not change retry policy in this slice.
- Do not run benchmark lanes for this slice.

## Verification Plan

Focused tests:

- completion summary records shell command intent metadata from BASH RESULT
- agentic completion gate conversion preserves the new fields
- learning outcome persistence preserves the new fields

Broader proportional checks:

- `go test ./cmd/elnath ./internal/agentic/completion ./internal/learning -count=1`
- `git diff --check`

## Implementation

Changed files:

- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_observability_test.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `cmd/elnath/runtime.go`
- `internal/agentic/completion/gate.go`
- `internal/learning/outcome.go`

Behavior added:

- completion shell command receipts now include `execution_policy`,
  `command_intent`, and `intent_source`
- parser reads these fields from `BASH RESULT`
- parser falls back to explicit tool input `intent` when the result header is
  legacy or unavailable
- timeout receipt can use the tool input timeout or the `BASH RESULT`
  `timeout_ms` header
- agentic completion gate context preserves the new fields
- learning outcome records preserve the new fields

No benchmark run was performed.
No corpus or baseline artifact was changed.

## Verification Results

Passed:

- `go test ./cmd/elnath -run 'TestCompletionContractSummaryRecordsShellCommandReceipts|TestRecordOutcomePersistsCompletionObservability|TestCompletionGateContextProviderConsumesRuntimeSummary' -count=1`
  - result: PASS
  - package time: `0.885s`
- `go test ./cmd/elnath ./internal/agentic/completion ./internal/learning -count=1`
  - result: PASS
  - package times: `cmd/elnath 20.421s`, `internal/agentic/completion 0.572s`, `internal/learning 1.634s`
- `git diff --check`
  - result: PASS

## Remaining Risks

- This slice preserves command intent after shell execution, but does not yet
  make retry policy depend on explicit intent.
- `process_start` intent is already present in process receipts, but this slice
  only addressed bash completion receipts.

## Next Recommendation

Open a coherent PR for this receipt propagation slice after final local status
check. After merge, continue the final completion program with the next
structural blocker rather than running full v8 or baselines.

## Claim Boundary

Allowed after passing verification:

- shell command intent metadata survives completion summary, agentic gate
  context, and learning outcome persistence.

Forbidden:

- benchmark success claim
- full v8 readiness claim
- superiority claim versus Claude Code or Codex
- claim that automatic self-correction is complete
