# Ask user question answer handoff

Date: 2026-05-14
Branch: `codex/post-pr216-next`
Lane: final completion program / UI-level user-answer handoff slice
Status: implemented locally

## Problem

After PR #216, Elnath preserved user-question answer length bounds in receipts,
but the initial `ask_user_question` output still did not tell a human/operator
how to answer the pending request through Elnath's queue-backed CLI path.

Claude Code's ask-user-question flow has a UI permission component that returns
answers to the model as tool result content. Elnath does not have that UI layer
yet. Its Elnath-native bridge is:

- `ask_user_question` emits a structured request
- `elnath task answer` records the answer and enqueues session resume
- `user_question_wait` or task monitoring observes the answer/resume state

The missing piece was a clear answer handoff in the request output and receipt.

## References inspected

- Elnath: `internal/agent/user_question_tool.go`
- Elnath: `cmd/elnath/cmd_task.go`
- Elnath: `cmd/elnath/cmd_explain.go`
- Elnath: `cmd/elnath/runtime_completion_observability.go`
- Claude Code: `/Users/stello/claude-code-src/src/tools/AskUserQuestionTool/AskUserQuestionTool.tsx`
- Claude Code: `/Users/stello/claude-code-src/src/components/permissions/AskUserQuestionPermissionRequest/AskUserQuestionPermissionRequest.tsx`
- Hermes: `/Users/stello/.hermes/hermes-agent/tools/process_registry.py`

Claude Code was used as behavior reference only. Elnath keeps its CLI/queue
handoff design and does not copy Claude source, prompts, or errors.

## Design

For session-bound `ask_user_question` output:

- add `answerable: true`
- add `answer_command`
- add `pending_command`
- add receipt `followup_tool: user_question_wait`
- shell-quote command arguments and use `'ANSWER_TEXT'` placeholder instead of
  angle-bracket text that could be interpreted as shell redirection

For unbound output:

- keep `answerable: false`
- omit answer/pending command hints
- omit receipt `followup_tool`

This avoids pretending a UI exists while making the existing Elnath-native
answer path explicit and machine-readable.

## Changed files

- `internal/agent/user_question_tool.go`
- `internal/agent/user_question_tool_test.go`
- `cmd/elnath/runtime_completion_observability_test.go`
- `.omc/research/ask-user-question-answer-handoff-2026-05-14.md`

## Verification

- Initial TDD check:
  - `go test ./internal/agent -run 'TestAskUserQuestionToolIncludesSessionIDWhenBound|TestAskUserQuestionToolOmitsSessionIDWhenUnbound' -count=1`
  - FAIL as expected before implementation: `Answerable`, `AnswerCommand`, and `PendingCommand` fields were missing.
- Focused after implementation:
  - `go test ./cmd/elnath ./internal/agent -run 'TestCompletionContractSummaryRecordsAskUserQuestionReceipt|TestAskUserQuestionTool(IncludesSessionIDWhenBound|OmitsSessionIDWhenUnbound|ReturnsStructuredRequest)' -count=1`
  - PASS: `cmd/elnath 0.942s`, `internal/agent 0.395s`
- Broader affected packages:
  - `go test ./cmd/elnath ./internal/agent ./internal/learning ./internal/daemon -count=1`
  - PASS after final shell-quote polish: `cmd/elnath 22.046s`, `internal/agent 13.728s`, `internal/learning 0.917s`, `internal/daemon 35.804s`
- Vet:
  - `go vet ./cmd/elnath ./internal/agent ./internal/learning ./internal/daemon`
  - PASS
- Whitespace:
  - `git diff --check`
  - PASS

## Benchmark / corpus / baseline

- Full v8 benchmark: no
- Current-only smoke: no
- Baseline: no
- Codex CLI comparison: no
- Claude Code comparison: no
- Benchmark corpus mutation: no
- Baseline mutation: no

## Claim boundary

Allowed:

- Session-bound `ask_user_question` requests now expose Elnath-native answer and
  pending-question command hints.
- The ask-question receipt now points to `user_question_wait` as the follow-up
  tool when the request is answerable.

Forbidden:

- UI-level blocking answer collection is complete.
- Elnath matches Claude Code's ask-user-question UX.
- Elnath stores answer text in receipts.
- Benchmark success or superiority.

## Remaining risk

- This is a CLI/receipt handoff improvement, not a full TUI/desktop question UI.
- Commands are hints for session-bound requests only.

## Next autonomous action

Commit this user-answer handoff slice, open one coherent PR, and use CI as the
merge gate. Do not run benchmark lanes for this slice.
