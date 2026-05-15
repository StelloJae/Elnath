# Product runtime milestone 2: user question cancel lifecycle

Date: 2026-05-15
Branch: codex/product-runtime-user-input
PR: none yet
Status: local milestone verified

## Purpose

Advance the user input and gateway/operator UX gate from the product/runtime
100% control document. This slice addresses cancellation of pending
user-input requests. It is not a benchmark lane.

The structural gap addressed here: `ask_user_question` created durable pending
requests and `user_question_answer` could close them, but there was no durable
cancel path. A user/operator direction change could leave a stale pending
question visible indefinitely.

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
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/learning/user_question_tools.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/learning/outcome.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/cmd_task.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/runtime.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/runtime_completion_observability.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/tools/tool_search.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_telegram_clarify_buttons.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_pending_event_none.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_pending_drain_race.py`

Claude Code:

- `/Users/stello/claude-code-src/src/remote/RemoteSessionManager.ts`
- `/Users/stello/claude-code-src/src/cli/structuredIO.ts`
- `/Users/stello/claude-code-src/src/cli/print.ts`

Reference conclusion:

- Pending control requests must be closed explicitly by answer, cancel, or
  stale-resolution paths.
- Request IDs are the ownership boundary.
- Duplicate or stale responses should be rejected rather than treated as fresh
  user intent.

No proprietary source, prompts, or error strings were copied.

## Changed behavior

- Added `user_question_cancel` model-callable tool.
- Added `elnath task cancel-question --session ID --request ID [--reason TEXT]`.
- Cancellation appends a durable `ControlToolReceipt` with:
  - `tool=user_question_cancel`
  - `action=cancel`
  - `status=cancelled`
  - `terminal=true`
  - `found=true`
  - `reason`
  - `followup_tool=user_question_list`
- Pending-question projection removes cancelled questions.
- `user_question_wait` returns `status=cancelled` when a cancel receipt appears.
- ToolSearch routes `user_question_cancel` as `user_input/runtime`.
- Runtime completion observability preserves cancel receipts and reasons.
- Control-surface manifest now lists five user-input tools.

## Changed files

- `internal/learning/outcome.go`
- `internal/learning/pending_questions.go`
- `internal/learning/pending_questions_test.go`
- `internal/learning/user_question_tools.go`
- `internal/learning/user_question_tools_test.go`
- `cmd/elnath/cmd_task.go`
- `cmd/elnath/cmd_task_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_command_tool_test.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `cmd/elnath/runtime_test.go`
- `internal/agentic/completion/gate.go`
- `internal/tools/tool_search.go`
- `internal/tools/tool_search_test.go`
- `.omc/research/product-runtime-user-input-cancel-lifecycle-2026-05-15.md`

## Verification

TDD check before implementation:

- `go test ./internal/learning ./cmd/elnath -run 'TestPendingUserQuestionsDropsCancelledQuestion|TestUserQuestionWaitToolReturnsCanceled|TestUserQuestionCancelToolCancelsPendingQuestion|TestCmdTaskCancelQuestionWithStoreRecordsOutcome|TestCmdTaskUsage' -count=1`
- Result: expected compile failure:
  - `undefined: UserQuestionCancelToolName`
  - `undefined: NewUserQuestionCancelTool`
  - `undefined: userQuestionCancelToolOutput`
  - `undefined: cmdTaskCancelQuestionWithStore`

Focused acceptance:

- `go test ./internal/learning ./cmd/elnath -run 'TestPendingUserQuestionsDropsCancelledQuestion|TestUserQuestionWaitToolReturnsCanceled|TestUserQuestionCancelToolCancelsPendingQuestion|TestCmdTaskCancelQuestionWithStoreRecordsOutcome|TestCmdTaskUsage|TestExecutionRuntimeRegistersUserQuestionCancelTool' -count=1`
- Result: PASS
  - `ok github.com/stello/elnath/internal/learning 0.809s`
  - `ok github.com/stello/elnath/cmd/elnath 0.775s`

Proportional package verification:

- `go test ./internal/learning ./cmd/elnath ./internal/tools ./internal/agentic/completion -count=1`
- First run exposed stale fixed-count test:
  - `TestExplainControlSurfacesJSON` still expected four user-input tools.
- After test expectation update, result: PASS
  - `ok github.com/stello/elnath/internal/learning 1.205s`
  - `ok github.com/stello/elnath/cmd/elnath 26.693s`
  - `ok github.com/stello/elnath/internal/tools 44.269s`
  - `ok github.com/stello/elnath/internal/agentic/completion 0.821s`

Runtime sweep:

- `go test ./internal/... ./cmd/elnath -count=1`
- Result: PASS
  - includes `internal/agent`, `internal/agentic/completion`,
    `internal/daemon`, `internal/learning`, `internal/tools`, and
    `cmd/elnath`

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

- Elnath now has a durable cancel path for pending user-input requests.
- Cancelled questions are removed from pending-question listings.
- `user_question_wait` can report `cancelled`.
- CLI operators can cancel a pending user question with `elnath task
  cancel-question`.

Not allowed:

- user input UX is 100% complete
- timeout lifecycle is fully complete
- Telegram/other gateway button rendering is complete
- duplicate final-send suppression is fixed
- v8 benchmark passed
- Elnath beats Claude Code or Codex

## Remaining risks

- Timeout expiration from `timeout_seconds` is still only a request hint and
  wait-window behavior; it does not yet close pending questions automatically.
- Gateway-specific structured button rendering remains outside this slice.
- Duplicate final-send suppression remains a separate product-runtime blocker.

## Next autonomous action

Continue Milestone 2 with question timeout expiration or gateway final-send
deduplication, based on fresh code evidence. Do not return to benchmark loops.
