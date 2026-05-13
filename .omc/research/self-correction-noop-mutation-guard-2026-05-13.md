# self-correction no-op mutation guard

Date: 2026-05-13
Branch: codex/self-correction-verification-guards

## Summary

Tightened completion observability so a mutating tool call is not automatically
treated as a successful edit when its result says no effective change occurred.

This improves bounded self-correction by keeping no-op edit attempts in the
existing `edit_intent_without_mutation` warning lane. That lane already maps to
the closed-enum `retry_smaller_scope` retry decision.

## Implemented

- Added no-op result detection for mutating tool outputs.
- `bash`/`worktree_run` mutation-looking commands with outputs such as
  `No changes.` no longer satisfy edit-observed evidence.
- `write_file`/`edit_file` style outputs such as `content already matches` or
  `old_string and new_string are identical` no longer satisfy edit-observed
  evidence when surfaced as non-error tool results.
- Preserved existing success detection for real mutating tool results.

## Claim boundary

Allowed claims:

- No-op mutating tool results are routed to `edit_intent_without_mutation`.
- That warning remains bounded and receipt-backed through the existing
  completion correction summary fields.

Not claimed:

- no full diff-based workspace verification
- no broad silent self-healing guarantee
- no unbounded retry
- no benchmark behavior change
- no v8 benchmark success claim

## Verification

Passed:

```text
go test ./cmd/elnath -run 'TestCompletionContractSummaryDoesNotCountNoopBashMutationAsMutation|TestCompletionContractSummaryDoesNotCountNoopWriteFileResultAsMutation|TestCompletionContractSummaryDetectsEditToolMutation|TestCompletionContractSummaryDoesNotCountFailedEditToolAsMutation|TestCompletionContractSummaryDetectsWorktreeRunMutation' -count=1
go test ./cmd/elnath -run 'TestCompletionRetryMarksUnresolvedWarningFailed|TestCompletionRetryRecordsFailedCorrectionAttempt|TestCompletionRetryRunsExplicitVerificationCommand|TestCompletionRetryRecordsFailedVerificationCommand' -count=1
go test ./cmd/elnath -run 'TestCompletionContractSummary|TestCompletionRetry' -count=1
go test ./internal/agentic/completion -count=1
git diff --check
go vet ./...
go test ./cmd/elnath ./internal/agentic/completion -count=1
```

## Next recommendation

Run broader completion observability and runtime retry tests, then decide whether
this guard is enough for the current self-correction milestone or should be
batched with one more verification guard.
