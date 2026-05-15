# Product runtime milestone 2 closeout: user input and gateway UX

Date: 2026-05-15
Branch: codex/product-runtime-user-input
PR: none yet
Status: local milestone verified, PR not opened yet

## Purpose

Close the product/runtime control document's Milestone 2 locally before moving
to edit-aware diagnostics and code intelligence. This milestone focused on
operator-facing user input, not benchmark performance.

## Control document

Standing authority:

- `/Users/stello/elnath/.omc/research/elnath-product-runtime-100-control-2026-05-15.md`

Milestone 2 gate:

- User question lifecycle is test-covered from ask to answer, timeout, and
  cancel.
- Structured choices and free-text fallback both work.
- Operator-facing final messages are not duplicated.
- Receipts make answer source, timing, and status inspectable.

## Local commits in this milestone

- `b961586` - `feat(runtime): preserve structured user choices`
- `6b5235a` - `feat(runtime): cancel pending user questions`
- `9e9a739` - `feat(runtime): expire pending user questions`
- `037634d` - `feat(runtime): add telegram question gateway`

## Reference files inspected

Elnath:

- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/agent/user_question_tool.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/daemon/task_tools.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/learning/pending_questions.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/learning/user_question_tools.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/telegram/shell.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/telegram/sink.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/telegram/stream.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/cmd_task.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/cmd_explain.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/cmd_daemon.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/cmd_telegram.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/gateway/stream_consumer.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_telegram_clarify_buttons.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_pending_event_none.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_pending_drain_race.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_message_deduplicator.py`

Claude Code:

- `/Users/stello/claude-code-src/src/remote/RemoteSessionManager.ts`
- `/Users/stello/claude-code-src/src/remote/remotePermissionBridge.ts`
- `/Users/stello/claude-code-src/src/cli/structuredIO.ts`
- `/Users/stello/claude-code-src/src/cli/print.ts`

No proprietary source, prompts, or error strings were copied.

## Behavior now covered

- `ask_user_question` receipts preserve structured choices.
- Pending-question projections carry options and `allow_free_text`.
- `user_question_answer` rejects out-of-choice answers when free text is not
  allowed.
- `elnath explain pending-questions` shows answer handoff commands and choices.
- Pending questions can be cancelled with durable receipts.
- Pending questions expire from active state when `timeout_seconds` elapses.
- `user_question_wait` reports `cancelled` and `timed_out` states.
- `elnath task answer` rejects stale, cancelled, timed-out, or invalid-option
  answers.
- Telegram shell can list, answer, and cancel pending user questions.
- Telegram answer path reuses the same pending-state validator as the CLI path.
- Telegram answer acknowledgements avoid echoing answer content.

## Final-send duplication audit

Existing guards checked:

- `internal/telegram/shell.go`: completion polling stores
  `NotifiedCompletionIDs` and skips already notified task IDs.
- `internal/telegram/sink.go`: task completion sends a summary only when the
  stream consumer has not already sent streamed output.
- `internal/telegram/stream.go`: stream consumer deduplicates identical flushes.
- `internal/telegram/progress_reporter.go`: progress reporter deduplicates
  repeated tool lines.

Focused audit verification:

- `go test ./internal/telegram -run 'TestShellHandleUpdateApprovalsAndNotifyCompletions|TestSinkOnProgressSummaryRoutesToStream|TestStreamConsumerDedup|TestStreamConsumerAlreadySentFalseWhenNoData|TestChatResponderStreamsResponse|TestProgressReporterDedup' -count=1`
- Result: PASS
  - `ok github.com/stello/elnath/internal/telegram 2.916s`

Conclusion:

- The known task/shell/stream duplicate-final-message guards are working.
- No new duplicate-send patch was justified in this slice.

## Full milestone verification

Focused user-input tests:

- Structured choices:
  - `go test ./internal/agent ./internal/learning ./internal/daemon ./cmd/elnath -run 'TestAskUserQuestionToolReturnsStructuredRequest|TestPendingUserQuestionsCarriesStructuredChoices|TestUserQuestionAnswerTool(ValidatesPendingRequest|RejectsAnswerOutsideStructuredChoices)|TestCompletionContractSummaryRecordsAskUserQuestionReceipt|TestExplainPendingQuestionsTextShowsAnswerCommand' -count=1`
  - Result: PASS
- Cancel lifecycle:
  - `go test ./internal/learning ./cmd/elnath -run 'TestPendingUserQuestionsDropsCancelledQuestion|TestUserQuestionWaitToolReturnsCanceled|TestUserQuestionCancelToolCancelsPendingQuestion|TestCmdTaskCancelQuestionWithStoreRecordsOutcome|TestCmdTaskUsage|TestExecutionRuntimeRegistersUserQuestionCancelTool' -count=1`
  - Result: PASS
- Timeout lifecycle:
  - `go test ./internal/learning ./cmd/elnath -run 'TestPendingUserQuestionsDropsTimedOutQuestion|TestUserQuestionWaitToolReturnsTimedOutQuestion|TestCmdTaskAnswerWithQueueRejectsTimedOutRequest' -count=1`
  - Result: PASS
- Telegram gateway:
  - `go test ./internal/telegram -run 'TestShellQuestionsCommandShowsPendingUserChoices|TestShellAnswerCommandEnqueuesPendingQuestionAnswer|TestShellCancelQuestionCommandClosesPendingQuestion' -count=1`
  - Result: PASS

Package and runtime verification:

- `go test ./internal/telegram ./cmd/elnath -count=1`
- Result: PASS
  - `ok github.com/stello/elnath/internal/telegram 16.556s`
  - `ok github.com/stello/elnath/cmd/elnath 30.914s`
- `go test ./internal/... ./cmd/elnath -count=1`
- Result: PASS
  - includes `internal/agent`, `internal/daemon`, `internal/learning`,
    `internal/telegram`, `internal/tools`, and `cmd/elnath`
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

- Milestone 2 is locally verified for structured choices, answer validation,
  cancel, timeout, CLI handoff, Telegram text fallback, and existing duplicate
  final-send guards.

Not allowed:

- all product/runtime gates are 100% complete
- rich inline button rendering is complete
- Discord/WhatsApp gateway parity is complete
- v8 benchmark passed
- Elnath beats Claude Code or Codex

## Remaining risks

- Telegram inline button rendering remains intentionally outside this local
  slice; text fallback is now present.
- Discord/WhatsApp parity is not implemented in this milestone.
- This milestone is local and not yet in a PR or merged.

## Next autonomous action

Move to Milestone 3: edit-aware diagnostics and code intelligence. Start with a
narrow reference pass against Elnath diagnostic code, Hermes diff/LSP-related
changes, and Claude Code edit/diagnostic flow before implementing.
