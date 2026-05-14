# Process Timeout Monitor Receipts

Date: 2026-05-14
Lane: final completion program
Branch: `codex/completion-next-blocker`
Status: locally verified

## Problem

`process_start` is the correct surface for long-running commands. It already
has bounded timeout behavior, process status, terminal state, and follow-up
monitor receipts.

The timeout outcome is still less explicit than the foreground `bash` path:

- bash receipts expose `timed_out` and `timeout_ms`
- process monitor receipts expose `status:"timeout"`, but not an explicit
  `timed_out` boolean or the configured timeout bound

For a supervisor-first runtime, final receipts should make timeout evidence
unambiguous without forcing downstream code to infer it from status text.

## References Checked

- Elnath:
  - `internal/tools/process_tools.go`
  - `internal/tools/process_tools_test.go`
  - `cmd/elnath/runtime_completion_observability.go`
  - `.omc/research/command-execution-policy-intent-2026-05-14.md`
  - `.omc/research/process-control-command-intent-receipts-2026-05-14.md`
- Claude Code source:
  - `src/screens/REPL.tsx`
  - `src/utils/queryHelpers.ts`
- Hermes:
  - `cron/scheduler.py`
- claw-code:
  - `rust/crates/runtime/src/bash.rs`

## Chosen Design

Add process timeout metadata to process snapshots and receipts:

- `timed_out`
- `timeout_ms`

Behavior boundary:

- no process execution behavior change
- no timeout default change
- no retry policy change
- no benchmark run

## Verification Plan

Focused tests:

- a short-timeout process reaches terminal `timeout`
- monitor output includes `timed_out:true`
- monitor receipt includes `timed_out:true`
- monitor output/receipt include configured `timeout_ms`

Broader proportional checks:

- `go test ./internal/tools -run 'TestProcessToolsReportTimeoutMonitorReceipt|TestProcessTools(StartMonitorTerminalReceipt|ReportRunningMonitorFollowup|StopTerminatesRunningProcess)' -count=1`
- `go test ./internal/tools ./cmd/elnath ./internal/agentic/completion ./internal/learning -count=1`
- `git diff --check`

## Implementation

Changed files:

- `internal/tools/process_tools.go`
- `internal/tools/process_tools_test.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_observability_test.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `cmd/elnath/runtime.go`
- `internal/agentic/completion/gate.go`
- `internal/agentic/completion/gate_test.go`
- `internal/learning/outcome.go`
- `internal/learning/outcome_store_test.go`

Behavior added:

- process snapshots now include `timed_out` and `timeout_ms`
- process monitor receipts now include `timed_out` and `timeout_ms`
- process stop receipts include `timed_out` and `timeout_ms` when observing an
  already timed-out process
- completion control receipts preserve `timed_out`
- learning and agentic completion receipt conversions preserve `timed_out` and
  existing `timeout_ms`

No timeout default changed.
No process execution behavior changed.
No benchmark run was performed.
No corpus or baseline artifact was changed.

## Verification Results

Passed:

- `go test ./internal/tools -run 'TestProcessToolsReportTimeoutMonitorReceipt|TestProcessTools(StartMonitorTerminalReceipt|ReportRunningMonitorFollowup|StopTerminatesRunningProcess)' -count=1`
  - result: PASS
  - package time: `1.292s`
- `go test ./cmd/elnath -run 'TestCompletionContractSummaryRecordsProcessTimeoutReceipt|TestProcessControlReceiptsConvertToLearningAndAgentic' -count=1`
  - result: PASS
  - package time: `0.627s`
- `go test ./internal/agentic/completion -run TestCompletionGate_ReceiptSummaryIncludesOptionalCompletionContext -count=1`
  - result: PASS
  - package time: `0.574s`
- `go test ./internal/learning -run TestOutcomeRecordCompletionObservabilityJSON -count=1`
  - result: PASS
  - package time: `0.925s`
- `go test ./internal/tools ./cmd/elnath ./internal/agentic/completion ./internal/learning -count=1`
  - result: PASS
  - package times: `internal/tools 40.465s`, `cmd/elnath 23.205s`,
    `internal/agentic/completion 0.584s`, `internal/learning 1.709s`
- `git diff --check`
  - result: PASS

## Remaining Risks

- This slice makes timeout evidence explicit. It does not add streaming
  line-watch process monitoring.
- It does not change retry decisions for timed-out processes.

## Next Recommendation

Open one coherent PR for process timeout receipt clarity. After merge, continue
the final completion program with the next structural blocker. Do not resume
full v8 or baseline lanes yet.

## Claim Boundary

Allowed after verification:

- process timeout evidence is explicit in monitor output and receipts.

Forbidden:

- benchmark success claim
- full v8 readiness claim
- superiority claim versus Claude Code or Codex
- claim that process supervision is complete beyond timeout receipt clarity
