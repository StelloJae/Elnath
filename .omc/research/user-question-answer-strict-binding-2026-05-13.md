# user_question_answer strict pending-question binding

Date: 2026-05-13
Branch: codex/user-question-answer-strict-binding

## Summary

`user_question_answer` now has a runtime strict-binding path.

The daemon tool keeps its queue-only constructor for low-level tests and legacy
composition, but runtime registration now attaches a pending-question validator
backed by the learning outcome store. In the runtime path, answers must carry a
`request_id` that is still pending for the supplied `session_id`.

## Implemented

- Added `learning.FindPendingUserQuestion`.
- Added optional `daemon.UserQuestionAnswerValidator`.
- Added `daemon.NewUserQuestionAnswerToolWithValidator`.
- Runtime registration uses the validator with the existing outcome store.
- Unknown, stale, missing, or cross-session `request_id` values are rejected
  before a daemon task is enqueued.
- When the caller omits `question`, the validated pending question text is used
  in the resume prompt for provenance.
- Added `elnath task answer --session ID --request ID --answer TEXT` as a
  CLI answer submission surface backed by the same validator and queue enqueue.
- CLI-submitted answers append a `user_question_answer` outcome receipt so the
  derived pending-question view closes the answered `request_id`.
- Updated `elnath task` usage and `elnath explain control-surfaces` notes.

## Claim boundary

Allowed claims:

- Runtime `user_question_answer` rejects answers that are not bound to a
  currently pending `request_id` for the supplied `session_id`.
- Rejected unbound answers do not enqueue daemon tasks.
- Valid pending answers still enqueue the existing session-bound daemon follow-up.
- Operators can submit a pending answer through `elnath task answer`; accepted
  answers create a pending daemon resume task and print the monitor command.
- CLI answer receipts close the pending request in `PendingUserQuestions`.

Not claimed:

- no dedicated pending-question table
- no standalone UI answer collection
- no broad automatic pause/resume lifecycle beyond the strict CLI/model-callable
  queue-backed follow-up task
- no benchmark behavior change
- no v8 benchmark success claim
- no parity or superiority claim over Codex or Claude Code

## Verification

Passed:

```text
go test ./internal/daemon -run 'TestUserQuestionAnswerToolValidatesPendingRequest|TestUserQuestionAnswerToolRejectsUnboundRequestWhenValidatorConfigured|TestUserQuestionAnswerToolEnqueuesSessionBoundFollowUp|TestUserQuestionAnswerToolRejectsMissingRequiredFields' -count=1
go test ./internal/learning -run 'TestFindPendingUserQuestionRequiresMatchingPendingRequest|TestPendingUserQuestions' -count=1
go test ./cmd/elnath -run TestExecutionRuntimeUserQuestionAnswerRequiresPendingRequest -count=1
go test ./cmd/elnath -run 'TestCmdTaskUsage|TestCmdTaskAnswerWithQueueEnqueuesBoundAnswer|TestCmdTaskAnswerWithQueueRejectsStaleRequest|TestExecutionRuntimeUserQuestionAnswerRequiresPendingRequest' -count=1
go test ./internal/daemon -run 'TestUserQuestionAnswerToolValidatesPendingRequest|TestUserQuestionAnswerToolRejectsUnboundRequestWhenValidatorConfigured' -count=1
go test ./internal/learning -run TestFindPendingUserQuestionRequiresMatchingPendingRequest -count=1
go test ./cmd/elnath -run 'TestExplainControlSurfacesJSON|TestExplainControlSurfacesText|TestCmdTaskUsage|TestCmdTaskAnswerWithQueueEnqueuesBoundAnswer|TestCmdTaskAnswerWithQueueRejectsStaleRequest|TestExecutionRuntimeUserQuestionAnswerRequiresPendingRequest' -count=1
go run ./cmd/elnath explain control-surfaces --json
go test ./cmd/elnath -run 'TestCmdTaskAnswerWithQueueEnqueuesBoundAnswer|TestCmdTaskAnswerWithQueueRejectsStaleRequest|TestExecutionRuntimeUserQuestionAnswerRequiresPendingRequest' -count=1
go test ./internal/learning -run 'TestFindPendingUserQuestionRequiresMatchingPendingRequest|TestPendingUserQuestions' -count=1
go test ./internal/daemon ./internal/learning ./cmd/elnath ./internal/tools ./internal/agent ./internal/agentic/completion -count=1
go vet ./...
git diff --check
```

## Next recommendation

Next gap is UI-level answer collection or a blocking wait state; the CLI/model
surfaces now cover strict queue-backed answer resume.
