# Product runtime milestone 2: Telegram question gateway

Date: 2026-05-15
Branch: codex/product-runtime-user-input
PR: none yet
Status: local milestone verified

## Purpose

Advance the user input and gateway/operator UX gate from the product/runtime
100% control document. This slice adds a Telegram text fallback for pending
user questions so operator interaction is not CLI-only.

This is not a benchmark lane.

## Control document

Standing authority:

- `/Users/stello/elnath/.omc/research/elnath-product-runtime-100-control-2026-05-15.md`

Relevant gate:

- Milestone 2: user input and gateway/operator UX
- CLI and gateway answer paths must work.
- Structured choices and free-text fallback must be visible.
- Receipts must make answer status inspectable.

## Reference files inspected

Elnath:

- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/telegram/shell.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/telegram/shell_test.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/daemon/task_tools.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/learning/pending_questions.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/learning/user_question_tools.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/cmd_daemon.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/cmd_telegram.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_telegram_clarify_buttons.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_pending_event_none.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_pending_drain_race.py`
- `/Users/stello/.hermes/hermes-agent/gateway/stream_consumer.py`

Claude Code:

- `/Users/stello/claude-code-src/src/remote/RemoteSessionManager.ts`
- `/Users/stello/claude-code-src/src/cli/structuredIO.ts`

Reference conclusion:

- Gateway request state must be request-id anchored.
- Duplicate, stale, or orphaned responses must be rejected through the same
  pending-state validator as CLI/model-callable tools.
- Text fallback should exist even when richer button rendering is not present.

No proprietary source, prompts, or error strings were copied.

## Changed behavior

- Added `WithShellOutcomeStore` so Telegram shell commands can inspect durable
  user-question receipts.
- Added `/questions` and `/pending-questions` commands.
- Added `/answer <session_id> <request_id> <answer>` command.
- Added `/cancel-question <session_id> <request_id> [reason]` command.
- Telegram answers reuse the daemon `user_question_answer` tool with the same
  pending-question validator as the CLI path.
- Telegram answers append a durable `ControlToolReceipt` outcome with workflow
  `telegram_answer`.
- Telegram cancellations reuse the durable `user_question_cancel` tool.
- Daemon Telegram shell and standalone `elnath telegram shell` now wire the
  outcome store into the shell.
- Operator replies acknowledge status and character count, not the answer body.

## Changed files

- `internal/telegram/shell.go`
- `internal/telegram/shell_test.go`
- `cmd/elnath/cmd_daemon.go`
- `cmd/elnath/cmd_telegram.go`
- `.omc/research/product-runtime-telegram-question-gateway-2026-05-15.md`

## Verification

TDD check before implementation:

- `go test ./internal/telegram -run 'TestShellQuestionsCommandShowsPendingUserChoices|TestShellAnswerCommandEnqueuesPendingQuestionAnswer|TestShellCancelQuestionCommandClosesPendingQuestion' -count=1`
- Result: expected compile failure:
  - `undefined: WithShellOutcomeStore`

Focused acceptance:

- `go test ./internal/telegram -run 'TestShellQuestionsCommandShowsPendingUserChoices|TestShellAnswerCommandEnqueuesPendingQuestionAnswer|TestShellCancelQuestionCommandClosesPendingQuestion' -count=1`
- Result: PASS
  - `ok github.com/stello/elnath/internal/telegram 0.823s`

Package verification:

- `go test ./internal/telegram ./cmd/elnath -count=1`
- Result: PASS
  - `ok github.com/stello/elnath/internal/telegram 16.556s`
  - `ok github.com/stello/elnath/cmd/elnath 30.914s`

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

- Telegram shell can list pending user questions.
- Telegram shell can answer pending questions through the same validator used
  by the CLI/runtime path.
- Telegram shell can cancel pending questions through the durable cancel tool.

Not allowed:

- user input UX is 100% complete
- rich Telegram inline button rendering is complete
- Discord/WhatsApp gateway parity is complete
- v8 benchmark passed
- Elnath beats Claude Code or Codex

## Remaining risks

- This is a text fallback path, not inline button rendering.
- Telegram command output uses short acknowledgement messages; richer operator
  formatting remains a polish lane.
- Duplicate final-send suppression in the task sink already exists, but chat
  error/partial-stream behavior still needs a separate audit before declaring
  all gateway send-duplication risks closed.

## Next autonomous action

Continue Milestone 2 by auditing final-send duplication behavior. If no defect
is found, write a Milestone 2 closeout artifact and move to Milestone 3:
edit-aware diagnostics and code intelligence.
