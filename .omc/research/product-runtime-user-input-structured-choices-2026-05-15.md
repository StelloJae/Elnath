# Product runtime milestone 2: structured user choices

Date: 2026-05-15
Branch: codex/product-runtime-user-input
PR: none yet
Status: local milestone verified

## Purpose

Advance the product/runtime completion lane by improving the user input and
operator UX surface. This is not a benchmark lane.

The gap addressed here: `ask_user_question` could emit structured choices in
the immediate tool output, but those choices were not preserved through durable
control receipts, pending-question state, answer validation, or operator-facing
`elnath explain pending-questions` text output.

## Control document

Standing authority:

- `/Users/stello/elnath/.omc/research/elnath-product-runtime-100-control-2026-05-15.md`

Relevant gate:

- Milestone 2: user input and gateway/operator UX
- Structured choices and free-text fallback must work.
- Receipts must make answer status inspectable.

## Reference files inspected

Elnath:

- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/agent/user_question_tool.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/agent/user_question_tool_test.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/daemon/task_tools.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/daemon/task_tools_test.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/learning/outcome.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/learning/pending_questions.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/internal/learning/user_question_tools.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/cmd_explain.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/runtime.go`
- `/Users/stello/elnath-worktrees/product-runtime-user-input/cmd/elnath/runtime_completion_observability.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/gateway/stream_consumer.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_pending_drain_race.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_pending_event_none.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_stream_consumer.py`

Claude Code:

- `/Users/stello/claude-code-src/src/remote/RemoteSessionManager.ts`
- `/Users/stello/claude-code-src/src/remote/remotePermissionBridge.ts`
- `/Users/stello/claude-code-src/src/cli/print.ts`

Reference conclusion:

- User-visible question or permission state should survive beyond immediate
  model/tool output.
- Pending request state must carry enough structured information to reject
  orphaned or invalid answers.
- Operator-facing surfaces should show the available choices rather than forcing
  users or agents to inspect raw JSON.

No proprietary source, prompts, or error strings were copied.

## Changed behavior

- `ask_user_question` receipts now preserve normalized structured options.
- Structured options are trimmed and exact duplicates are dropped.
- Control outcomes preserve question options.
- Pending-question projection carries `options` and `allow_free_text`.
- `user_question_answer` rejects answers outside the structured choices when
  `allow_free_text=false`.
- `elnath explain pending-questions` text output shows choices as quoted option
  labels.
- Completion observability records `ask_user_question` receipt options.

## Changed files

- `internal/agent/user_question_tool.go`
- `internal/agent/user_question_tool_test.go`
- `internal/learning/outcome.go`
- `internal/learning/pending_questions.go`
- `internal/learning/pending_questions_test.go`
- `internal/daemon/task_tools.go`
- `internal/daemon/task_tools_test.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_observability_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`
- `.omc/research/product-runtime-user-input-structured-choices-2026-05-15.md`

## Verification

TDD check before implementation:

- `go test ./internal/agent ./internal/learning ./internal/daemon ./cmd/elnath -run 'TestAskUserQuestionToolReturnsStructuredRequest|TestPendingUserQuestionsCarriesStructuredChoices|TestUserQuestionAnswerTool(ValidatesPendingRequest|RejectsAnswerOutsideStructuredChoices)|TestCompletionContractSummaryRecordsAskUserQuestionReceipt' -count=1`
- Result: expected compile/test failure because `Options` and
  `AllowFreeText` were not yet preserved through the durable receipt and answer
  validation path.

Focused acceptance:

- `go test ./internal/agent ./internal/learning ./internal/daemon ./cmd/elnath -run 'TestAskUserQuestionToolReturnsStructuredRequest|TestPendingUserQuestionsCarriesStructuredChoices|TestUserQuestionAnswerTool(ValidatesPendingRequest|RejectsAnswerOutsideStructuredChoices)|TestCompletionContractSummaryRecordsAskUserQuestionReceipt|TestExplainPendingQuestionsTextShowsAnswerCommand' -count=1`
- Result: PASS
  - `ok github.com/stello/elnath/internal/agent`
  - `ok github.com/stello/elnath/internal/learning`
  - `ok github.com/stello/elnath/internal/daemon`
  - `ok github.com/stello/elnath/cmd/elnath`

Broader proportional check:

- `go test ./internal/agent ./internal/learning ./internal/daemon ./cmd/elnath -count=1`
- Result: PASS
  - `ok github.com/stello/elnath/internal/agent 12.825s`
  - `ok github.com/stello/elnath/internal/learning 1.451s`
  - `ok github.com/stello/elnath/internal/daemon 35.422s`
  - `ok github.com/stello/elnath/cmd/elnath 26.200s`

Runtime package sweep:

- `go test ./internal/... ./cmd/elnath -count=1`
- Result: PASS
  - includes `internal/agent`, `internal/daemon`, `internal/learning`,
    `internal/tools`, `internal/wiki`, and `cmd/elnath`

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

- Elnath now preserves structured user-question choices through durable
  receipts and pending-question state.
- `user_question_answer` can reject out-of-choice answers when free text is not
  allowed.
- `elnath explain pending-questions` text output shows structured choices.

Not allowed:

- user input UX is 100% complete
- gateway final-send duplication is fixed
- timeout/cancel lifecycle is complete
- v8 benchmark passed
- Elnath beats Claude Code or Codex

## Remaining risks

- Timeout and cancel lifecycle for user questions still needs a separate
  product-runtime slice.
- Duplicate final-send suppression is not addressed in this milestone.
- Gateway-specific rendering beyond CLI/explain/runtime receipt paths remains
  to be validated.
- Choice matching is exact after trimming. Synonym or case-insensitive matching
  is intentionally not added here.

## Next autonomous action

Continue Milestone 2 with the next smallest structural blocker:

1. user-question timeout/cancel lifecycle receipts, or
2. duplicate final-send suppression if current code evidence shows that path is
   more urgent.

Do not return to benchmark loops.
