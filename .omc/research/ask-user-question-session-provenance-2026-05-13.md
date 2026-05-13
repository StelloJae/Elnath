# ask_user_question session provenance

Date: 2026-05-13
Branch: codex/ask-user-question-session

## Summary

Added a narrow session-provenance slice for `ask_user_question`.

When `ask_user_question` runs in a session-bound tool context, its structured
output and receipt now include `session_id`. Completion observability preserves
that field into learning and agentic completion receipts.

This is a wait/resume foundation only.

## Claim boundary

Allowed claims:

- `ask_user_question` emits `session_id` when the tool context is session-bound.
- `ask_user_question` omits `session_id` when no session is bound.
- Completion control receipts parse and trim `session_id`.
- Learning and agentic completion receipt conversions preserve `session_id`.

Not claimed:

- no user-response collection loop
- no daemon pause/resume implementation
- no automatic task wake after user reply
- no benchmark behavior change
- no production workflow success claim beyond receipt provenance

## Verification

Passed:

```text
go test ./internal/agent -run 'TestAskUserQuestionToolIncludesSessionIDWhenBound|TestAskUserQuestionToolOmitsSessionIDWhenUnbound|TestAskUserQuestionToolReturnsStructuredRequest' -count=1
go test ./cmd/elnath -run 'TestCompletionContractSummaryRecordsAskUserQuestionReceipt|TestDelegationControlReceiptsConvertToLearningAndAgentic' -count=1
go test ./internal/learning -run TestOutcomeRecordCompletionObservabilityJSONCompatibility -count=1
go test ./internal/agentic/completion -run TestCompletionGate_ReceiptSummaryIncludesOptionalCompletionContext -count=1
go test ./internal/agent ./cmd/elnath ./internal/learning ./internal/agentic/completion -count=1
go vet ./...
git diff --check
```

## Next recommendation

Implement the actual wait/resume bridge next:

- persist user-input-required questions in the session/control ledger
- expose a bounded answer/resume command or callable surface
- resume only the session/task tied to the recorded `session_id`
