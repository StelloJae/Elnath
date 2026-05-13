# user_question_answer resume bridge

Date: 2026-05-13
Branch: codex/ask-user-question-session

## Summary

Added a narrow answer-to-resume bridge for structured user-input requests.

`ask_user_question` can now provide a `session_id` when session-bound, and
all question requests carry a `request_id`. `user_question_answer` can enqueue a
daemon follow-up task against that same session and optional request id. The
daemon's existing task runner then resumes the session through the normal
`TaskPayload.SessionID` path.

## Implemented

- Added model-callable `user_question_answer`.
- Requires `session_id` and `answer`.
- Accepts optional `request_id`, `question`, `surface`, and `idempotency_key`.
- Enqueues a daemon task with a continuation prompt and the exact session id.
- Emits a queue-backed receipt:
  - `tool=user_question_answer`
  - `action=answer`
  - `execution_policy=daemon_queue_user_answer_resume`
  - `task_id`
  - `request_id` when supplied
  - `session_id`
  - `followup_tool=task_monitor`
- Registered the tool in the runtime registry.
- Added ToolSearch routing metadata as `category=user_input`, `surface=runtime`.
- Added completion observability extraction for the receipt.
- Updated `elnath explain control-surfaces` to show `user_input` as partial.

## Claim boundary

Allowed claims:

- Elnath has a receipt-backed user-answer enqueue bridge for session-bound
  `ask_user_question` flows.
- The answer bridge creates a pending daemon task scoped to the supplied
  `session_id`.
- `request_id` can be carried from question request to answer enqueue receipt.
- The bridge is ToolSearch-discoverable and completion-observable.

Not claimed:

- no blocking wait state
- no automatic wake after the user answer arrives
- no validation that the supplied answer corresponds to the most recent pending
  question or to a persisted pending-question ledger
- no UI-level answer collection
- no full managed pause/resume lifecycle
- no benchmark behavior change

## Verification

Passed:

```text
go test ./internal/daemon -run 'TestUserQuestionAnswerToolEnqueuesSessionBoundFollowUp|TestUserQuestionAnswerToolRejectsMissingRequiredFields|TestTaskCreateToolEnqueuesPendingTask|TestTaskToolsMetadata' -count=1
go test ./cmd/elnath -run 'TestCompletionContractSummaryRecordsUserQuestionAnswerReceipt|TestCompletionContractSummaryRecordsAskUserQuestionReceipt|TestExecutionRuntimeRegistersDeferredControlSurfaceTools|TestExplainControlSurfacesJSON|TestExplainControlSurfacesText' -count=1
go test ./internal/tools -run 'TestToolSearchReportsRoutingMetadata|TestToolSearchFiltersByCategoryAndSurface' -count=1
go test ./internal/agent -run 'TestPlanModeAllowsOnlyReadOnlyTools|TestAcceptEditsAutoApprovesWithoutPrompter' -count=1
go test ./internal/agent ./internal/daemon ./cmd/elnath ./internal/tools ./internal/learning ./internal/agentic/completion -count=1
go vet ./...
git diff --check
go run ./cmd/elnath explain control-surfaces --json
```

## Next recommendation

Implement a durable pending-question lookup before adding automatic wake:

- record pending question metadata with a stable question id
- expose latest/list pending questions for a session
- make `user_question_answer` optionally bind to question id
- then add automatic wake or UI collection on top
