# Budget edit-intent completion warning

Date: 2026-05-13
Branch: `codex/budget-edit-intent-completion-warning`
Base HEAD: `3aca1cdc7a9ad4144dcd8d5a43794940a4ff2c4f`
Status: local implementation slice

## Purpose

Stop widening benchmark lanes after repeated `V8-MIX-BF-001` incomplete patch
evidence, and repair the underlying runtime completion-contract weakness.

The benchmark symptom:

- `V8-MIX-BF-001` verification passed
- changed files were only production code plus `go.work.sum`
- Elnath said it would add a regression test
- recovery spent budget without leaving the promised test diff
- wrapper correctly failed closed as `incomplete_patch`

This is runtime evidence, not a benchmark-score goal.

## Root Cause

`internal/agent.Agent.Run` uses `budget_exceeded` when the agent reaches
`maxIterations`.

Before this slice, `cmd/elnath.summarizeCompletionContract` detected:

- incomplete final response
- verification failure
- unsupported verification success claim
- edit intent without mutation

It did not detect this shape:

- user requested a code fix
- mutation was observed
- agent still ended with `FinishReason=budget_exceeded`
- final assistant text indicated remaining edit intent

That let a partially edited, budget-exhausted run look too clean at the
completion-contract layer.

## Change

Runtime completion observability now emits:

- `CompletionWarning=budget_exceeded_after_edit_intent`
- `RetryDecision=retry_smaller_scope`
- `RetryReason=budget_exceeded_after_edit_intent`

This warning is only used when no higher-priority warning already exists.
Existing warnings such as `edit_intent_without_mutation` keep priority.

Changed files:

- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_observability_test.go`
- `.omc/research/elnath-final-autonomous-runtime-goal-2026-05-13.md`

## Verification

Red test before implementation:

```bash
go test ./cmd/elnath -run TestCompletionContractSummaryDetectsBudgetExceededAfterEditIntent -count=1
```

Result before fix:

- FAIL
- `CompletionWarning = "", want budget_exceeded_after_edit_intent`

Focused checks after implementation:

```bash
go test ./cmd/elnath -run 'TestCompletionContractSummaryDetectsBudgetExceededAfterEditIntent|TestCompletionContractSummaryDetectsEditIntentWithoutMutation|TestCompletionContractSummaryDetectsEditToolMutation' -count=1
go test ./cmd/elnath -run TestCompletionContractSummary -count=1
go test ./cmd/elnath -count=1
go test ./internal/agent ./internal/orchestrator -count=1
git diff --check
go vet ./...
```

Results:

- PASS: focused budget/edit-intent tests
- PASS: all `TestCompletionContractSummary*` tests
- PASS: `go test ./cmd/elnath -count=1`
- PASS: `go test ./internal/agent ./internal/orchestrator -count=1`
- PASS: `git diff --check`
- PASS: `go vet ./...`

## Claim Boundary

Allowed:

- Elnath now classifies `budget_exceeded` after edit intent as a completion
  warning.
- The warning routes to bounded `retry_smaller_scope`.
- This is a runtime completion-contract repair motivated by retained
  benchmark evidence.

Forbidden:

- expanded 10-task smoke passed.
- full v8 benchmark passed.
- baseline/comparison completed.
- Elnath beats Claude Code.
- Elnath beats Codex.
- broad public benchmark superiority.

## Next Action

Keep benchmark widening paused until this runtime slice is reviewed/merged.

After merge, rerun the smallest meaningful current-only lane:

1. one-task `V8-MIX-BF-001` retained current-only retry
2. if clean, 4-task control smoke
3. only then reconsider larger selected current-only smoke
