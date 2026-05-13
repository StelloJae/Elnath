# pending user question lookup

Date: 2026-05-13
Branch: codex/pending-user-questions

## Summary

Added a durable lookup layer for unanswered structured user-input requests.

This builds on PR #191:

- `ask_user_question` records `request_id`, `session_id`, and question text in
  the receipt.
- `user_question_answer` can carry the same `request_id`.
- `learning.PendingUserQuestions` scans outcome records and returns unanswered
  request ids, latest first.
- `elnath explain pending-questions` exposes the lookup in text and JSON form.
- `user_question_list` exposes the same lookup as a read-only model-callable
  tool.

## Claim boundary

Allowed claims:

- Elnath can derive pending user questions from completion outcome receipts.
- Pending lookup can filter by `session_id`.
- A later `user_question_answer` receipt with the same `request_id` closes the
  pending question in the lookup.
- `elnath explain pending-questions --json` exposes the current derived view.
- `user_question_list` lets the model inspect pending questions without loading
  every outcome record.

Not claimed:

- no dedicated persistent pending-question table
- no automatic wake after answer
- no UI answer collection
- no hard validation inside `user_question_answer` against the pending lookup
- no strict rejection of stale/unknown `request_id`
- no full managed pause/resume lifecycle
- no benchmark behavior change

## Verification

Passed:

```text
go test ./internal/learning -run 'TestPendingUserQuestionsListsUnansweredLatestFirst|TestPendingUserQuestionsFiltersSessionAndLimit|TestOutcomeRecordCompletionObservabilityJSONCompatibility' -count=1
go test ./cmd/elnath -run 'TestExplainPendingQuestionsJSON|TestExplainPendingQuestionsTextShowsNoPendingAfterAnswer|TestCompletionContractSummaryRecordsAskUserQuestionReceipt|TestDelegationControlReceiptsConvertToLearningAndAgentic' -count=1
go test ./internal/agent -run 'TestAskUserQuestionToolReturnsStructuredRequest' -count=1
go test ./internal/agentic/completion -run TestCompletionGate_ReceiptSummaryIncludesOptionalCompletionContext -count=1
go test ./internal/learning -run 'TestUserQuestionListToolListsPendingQuestions|TestUserQuestionListToolMetadataAndErrors|TestPendingUserQuestions' -count=1
go test ./cmd/elnath -run 'TestCompletionContractSummaryRecordsUserQuestionListReceipt|TestExecutionRuntimeRegistersDeferredControlSurfaceTools|TestExplainControlSurfacesJSON|TestExplainPendingQuestionsJSON' -count=1
go test ./internal/tools -run TestToolSearchReportsRoutingMetadata -count=1
go test ./internal/agent -run 'TestPlanModeAllowsOnlyReadOnlyTools|TestAcceptEditsAutoApprovesWithoutPrompter' -count=1
go test ./internal/agent ./cmd/elnath ./internal/learning ./internal/agentic/completion -count=1
go test ./internal/agent ./cmd/elnath ./internal/learning ./internal/tools ./internal/agentic/completion -count=1
go vet ./...
git diff --check
```

## Next recommendation

Bind `user_question_answer` to pending lookup evidence:

- optionally reject stale/unknown `request_id` in strict mode
- only then add automatic wake/resume behavior
