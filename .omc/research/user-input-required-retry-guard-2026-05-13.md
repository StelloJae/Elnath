# User Input Required Retry Guard

Date: 2026-05-13
Branch: codex/user-input-retry-guard
Lane: ccunpacked reference-parity implementation

## Summary

This slice prevents bounded completion correction from retrying while a
structured `ask_user_question` request is open.

The previous completion observability path could mark `user_input_required=true`
but still plan `retry_smaller_scope` if the final assistant text contained
phrases such as "still need" or "incomplete". That risks spending another model
turn when the correct next state is waiting for user input.

## Change

`completionRetryPlan` now returns no retry decision when
`summary.UserInputRequired` is true.

This preserves the completion warning for observability, but it does not start a
bounded auto-correction retry until user input exists.

## Claim Boundary

Allowed:

- Open `ask_user_question` requests suppress automatic completion correction
  retry planning.
- Completion summaries still record `user_input_required=true`.
- Incomplete final-response retry behavior remains intact when no user input is
  required.

Not claimed:

- No wait/resume implementation was added.
- No daemon pause/resume behavior changed.
- No user-response collection loop was added.
- No benchmark behavior changed.

## Verification

TDD:

```text
go test ./cmd/elnath -run TestCompletionContractSummaryDoesNotRetryWhenUserInputRequired -count=1
RED before implementation:
retry plan = "retry_smaller_scope"/"final_response_reports_incomplete", want empty while user input is required
```

Focused:

```text
go test ./cmd/elnath -run 'TestCompletionContractSummaryDoesNotRetryWhenUserInputRequired|TestCompletionContractSummaryRecordsAskUserQuestionReceipt|TestCompletionContractSummaryDetectsIncompleteFinalResponse' -count=1
PASS
```

Broader:

```text
go test ./cmd/elnath ./internal/learning ./internal/agentic/completion -count=1
PASS

go vet ./...
PASS

git diff --check
PASS
```

## Remaining Risk

This is a retry-planning guard only. Elnath still needs a later wait/resume
surface if the product should continue automatically after the user answers.
