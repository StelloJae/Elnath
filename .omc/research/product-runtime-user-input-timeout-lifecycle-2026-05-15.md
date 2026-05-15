# Product runtime milestone 2: user question timeout lifecycle

Date: 2026-05-15
Branch: codex/product-runtime-user-input
PR: none yet
Status: local milestone verified

## Purpose

Advance the user input and gateway/operator UX gate from the product/runtime
100% control document. This slice turns `timeout_seconds` from a passive hint
into an answerability boundary for pending user questions.

This is not a benchmark lane.

## Control document

Standing authority:

- `/Users/stello/elnath/.omc/research/elnath-product-runtime-100-control-2026-05-15.md`

Relevant gate:

- Milestone 2: user input and gateway/operator UX
- User question lifecycle must cover ask, answer, timeout, and cancel.
- Receipts must make status inspectable.

## Reference files inspected

Elnath:

- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/learning/pending_questions.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/learning/pending_questions_test.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/learning/user_question_tools.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/learning/user_question_tools_test.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/cmd_task.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/cmd_task_test.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_pending_event_none.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_pending_drain_race.py`
- `/Users/stello/.hermes/hermes-agent/gateway/stream_consumer.py`

Claude Code:

- `/Users/stello/claude-code-src/src/remote/RemoteSessionManager.ts`
- `/Users/stello/claude-code-src/src/cli/structuredIO.ts`

Reference conclusion:

- Pending user-visible request state must have a clear lifetime.
- Stale request IDs should stop being actionable instead of accepting late
  operator input.
- Timeout should be surfaced as state, not hidden as a generic wait timeout.

No proprietary source, prompts, or error strings were copied.

## Changed behavior

- `PendingUserQuestions` now derives active pending questions against the
  current clock.
- Added `PendingUserQuestionsAt` so timeout behavior can be tested
  deterministically.
- Questions with `timeout_seconds` and an expired ask timestamp are removed
  from pending-question listings.
- `user_question_wait` reports `status=timed_out` for an expired request
  instead of waiting until `wait_ms` expires.
- Timed-out wait receipts include a reason and `followup_tool=user_question_list`.
- `elnath task answer` rejects answers to timed-out requests because they are
  no longer pending.

## Changed files

- `internal/learning/pending_questions.go`
- `internal/learning/pending_questions_test.go`
- `internal/learning/user_question_tools.go`
- `internal/learning/user_question_tools_test.go`
- `cmd/elnath/cmd_task_test.go`
- `.omc/research/product-runtime-user-input-timeout-lifecycle-2026-05-15.md`

## Verification

TDD check before implementation:

- `go test ./internal/learning ./cmd/elnath -run 'TestPendingUserQuestionsDropsTimedOutQuestion|TestUserQuestionWaitToolReturnsTimedOutQuestion|TestCmdTaskAnswerWithQueueRejectsTimedOutRequest' -count=1`
- Result: expected failure:
  - `undefined: PendingUserQuestionsAt`
  - timed-out answer path still enqueued an answer task

Focused acceptance:

- `go test ./internal/learning ./cmd/elnath -run 'TestPendingUserQuestionsDropsTimedOutQuestion|TestUserQuestionWaitToolReturnsTimedOutQuestion|TestCmdTaskAnswerWithQueueRejectsTimedOutRequest' -count=1`
- Result: PASS
  - `ok github.com/stello/elnath/internal/learning 0.735s`
  - `ok github.com/stello/elnath/cmd/elnath 0.668s`

Package verification:

- `go test ./internal/learning ./cmd/elnath -count=1`
- Result: PASS
  - `ok github.com/stello/elnath/internal/learning 1.036s`
  - `ok github.com/stello/elnath/cmd/elnath 30.929s`

Runtime sweep:

- `go test ./internal/... ./cmd/elnath -count=1`
- Result: PASS
  - includes `internal/agent`, `internal/daemon`, `internal/learning`,
    `internal/telegram`, `internal/tools`, and `cmd/elnath`

Whitespace:

- `git diff --check`
- Result: PASS

## Benchmark boundary

Not run:

- full v8 benchmark
- baseline
- Codex comparison
- Claude Code comparison
- benchmark smoke

No benchmark corpus or baseline artifacts were changed.

## Claim boundary

Allowed:

- Elnath now treats timed-out user-question requests as non-pending.
- `user_question_wait` can report `timed_out`.
- `elnath task answer` rejects answers to timed-out requests.

Not allowed:

- user input UX is 100% complete
- gateway button rendering is complete
- duplicate final-send suppression is fixed
- timeout expiration appends a new durable terminal record automatically
- v8 benchmark passed
- Elnath beats Claude Code or Codex

## Remaining risks

- Timeout state is derived from the original ask timestamp and timeout value;
  this slice does not append a separate terminal timeout receipt in the outcome
  log.
- Legacy ask records with missing timestamps or no `timeout_seconds` do not
  expire automatically.
- Gateway-specific button rendering and duplicate final-send suppression remain
  separate product-runtime blockers.

## Next autonomous action

Continue Milestone 2 with duplicate final-send suppression and gateway rendering
validation. Do not return to benchmark loops.
