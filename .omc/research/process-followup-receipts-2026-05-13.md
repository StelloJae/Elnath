# Process follow-up receipts

Date: 2026-05-13
Branch: `codex/process-followup-receipts`

## Claim

Elnath process control tools now emit compact follow-up hints in their structured receipts where a next observation is expected.

This is a control-loop observability change only. It does not change process execution, timeout, stop, or monitoring semantics.

## Scope

Changed:

- `process_start` receipt now includes `followup_tool: process_monitor`.
- running `process_monitor` receipt now includes `followup_tool: process_monitor`.
- terminal `process_monitor` receipt omits `followup_tool`.
- successful `process_stop` receipt now includes `followup_tool: process_monitor`.
- completion observability conversion tests cover process follow-up propagation into learning and agentic receipt conversion.

Not changed:

- process timeout defaults
- process stop behavior
- process output tail limits
- process registry/tool discovery behavior
- benchmark corpus, baselines, or v8 evidence

## Evidence

TDD red before implementation:

- `go test ./internal/tools -run 'TestProcessToolsStartMonitorTerminalReceipt|TestProcessToolsReportRunningMonitorFollowup|TestProcessStopTerminatesRunningProcess' -count=1`
- Result: FAIL as expected; process receipts lacked `followup_tool`.

Focused verification after implementation:

- `go test ./internal/tools -run 'TestProcessToolsStartMonitorTerminalReceipt|TestProcessToolsReportRunningMonitorFollowup|TestProcessStopTerminatesRunningProcess' -count=1`
- Result: PASS

- `go test ./cmd/elnath -run 'TestCompletionContractSummaryRecordsProcessToolReceipts|TestProcessControlReceiptsConvertToLearningAndAgentic' -count=1`
- Result: PASS

Broader verification:

- `go test ./internal/tools ./cmd/elnath -count=1`
- Result: PASS

- `go vet ./...`
- Result: PASS

- `git diff --check`
- Result: PASS

## Claim boundary

Allowed:

- process control receipts now guide model-visible follow-up monitoring.
- receipt propagation preserves process follow-up hints.

Not allowed:

- no claim that Elnath fully matches Claude Code process management.
- no claim that process timeout policy changed.
- no claim that automatic self-correction is complete.
- no v8 benchmark claim.
