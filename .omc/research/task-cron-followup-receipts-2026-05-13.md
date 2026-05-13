# Task Cron Follow-up Receipts - 2026-05-13

## Summary

Branch: `codex/task-cron-receipt-polish`

This slice strengthens Elnath's task/cron callable control surface by preserving
explicit follow-up guidance in receipts.

## Change

- `task_create` receipts now include `followup_tool: task_monitor`.
- `schedule_create` and `schedule_delete` receipts now include
  `followup_tool: schedule_list`.
- Control-tool completion receipts now parse `followup_tool`.
- Learning outcome receipts preserve `followup_tool`.
- Agentic completion gate receipts preserve `followup_tool`.

## TDD Evidence

RED:

- `go test ./internal/daemon -run TestTaskCreateToolEnqueuesPendingTask -count=1`
  failed because `task_create` had no `followup_tool`.
- `go test ./internal/scheduler -run TestScheduleCreateListDeleteTools -count=1`
  failed because schedule receipts had no `followup_tool`.
- `go test ./cmd/elnath -run 'TestCompletionContractSummaryRecordsControlToolReceipts|TestDelegationControlReceiptsConvertToLearningAndAgentic' -count=1`
  failed because completion/control receipt types did not preserve
  `followup_tool`.

GREEN:

- `go test ./internal/daemon -run TestTaskCreateToolEnqueuesPendingTask -count=1`
  passed.
- `go test ./internal/scheduler -run TestScheduleCreateListDeleteTools -count=1`
  passed.
- `go test ./cmd/elnath -run 'TestCompletionContractSummaryRecordsControlToolReceipts|TestDelegationControlReceiptsConvertToLearningAndAgentic' -count=1`
  passed.
- `go test ./internal/learning -run TestOutcomeRecordCompletionObservabilityJSONCompatibility -count=1`
  passed.
- `go test ./internal/agentic/completion -run TestCompletionGate_ReceiptSummaryIncludesOptionalCompletionContext -count=1`
  passed.
- `go test ./internal/daemon ./internal/scheduler ./cmd/elnath ./internal/learning ./internal/agentic/completion -count=1`
  passed.
- `go vet ./...`
  passed.
- `git diff --check`
  passed.

## Claim Boundary

Allowed:

- Task and schedule mutation receipts now provide clearer next-tool guidance.
- Follow-up guidance survives completion, learning, and agentic completion gate
  receipt propagation.

Not claimed:

- No new task execution behavior.
- No scheduler hot reload.
- No new cron expression support.
- No v8 benchmark, baseline, Codex CLI comparison, or Claude Code comparison.

## Next Recommendation

Open one small PR for the task/cron receipt polish slice, then continue to
Plan/Worktree callable surface polish.
