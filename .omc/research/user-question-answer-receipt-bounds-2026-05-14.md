# User question answer receipt bounds

Date: 2026-05-14
Branch: `codex/post-pr215-next`
Lane: final completion program / user-input control surface
Status: implemented locally

## Problem

`user_question_answer` returned `answer_chars` in its tool output, but the
receipt path did not preserve that bound through completion, learning, agentic
gate context, or `user_question_wait`.

Claude Code's ask-user-question flow returns selected answers to the model as
tool result content. Elnath intentionally uses a queue-resume design instead of
storing answer text in receipts, but the receipt should still preserve a
non-sensitive answer-length bound. Without it, answer evidence is weaker than
the tool output.

## References inspected

- Elnath: `internal/daemon/task_tools.go`
- Elnath: `internal/learning/user_question_tools.go`
- Elnath: `cmd/elnath/runtime_completion_observability.go`
- Elnath: `cmd/elnath/runtime.go`
- Elnath: `cmd/elnath/runtime_completion_gate_context.go`
- Claude Code: `/Users/stello/claude-code-src/src/tools/AskUserQuestionTool/AskUserQuestionTool.tsx`
- Hermes: `/Users/stello/.hermes/hermes-agent/tools/process_registry.py`

Claude Code was used as behavior reference only. Elnath keeps its own
queue-backed design and does not copy Claude source, prompt, or errors.

## Design

Add `answer_chars` to receipt-only control paths, not answer text:

- `user_question_answer` receipt records `answer_chars`
- completion control receipts parse and preserve it
- learning outcome receipts preserve it
- agentic completion gate receipts preserve it
- `user_question_wait` returns and receipts the answer length when the request is answered

This improves auditability without putting user answer content into persistent
receipt metadata.

## Changed files

- `internal/daemon/task_tools.go`
- `internal/daemon/task_tools_test.go`
- `internal/learning/outcome.go`
- `internal/learning/user_question_tools.go`
- `internal/learning/user_question_tools_test.go`
- `internal/agentic/completion/gate.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_observability_test.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `.omc/research/user-question-answer-receipt-bounds-2026-05-14.md`

## Verification

- Initial TDD check:
  - `go test ./internal/daemon ./internal/learning ./cmd/elnath -run 'TestUserQuestionAnswerToolEnqueuesSessionBoundFollowUp|TestUserQuestionWaitToolReturnsAnsweredWhenAnswerArrives|TestCompletionContractSummaryRecordsUserQuestion(Answer|Wait)Receipt' -count=1`
  - FAIL as expected before implementation: `AnswerChars` fields were missing.
- Focused after implementation:
  - same command
  - PASS: `internal/daemon 0.650s`, `internal/learning 1.121s`, `cmd/elnath 0.660s`
- Broader affected packages:
  - `go test ./cmd/elnath ./internal/daemon ./internal/learning ./internal/agentic/completion -count=1`
  - PASS: `cmd/elnath 21.859s`, `internal/daemon 35.508s`, `internal/learning 0.779s`, `internal/agentic/completion 1.719s`
- Vet:
  - `go vet ./cmd/elnath ./internal/daemon ./internal/learning ./internal/agentic/completion`
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

- User-question answer receipts now preserve `answer_chars` through the
  queue-backed completion, learning, wait, and agentic receipt paths.

Forbidden:

- Elnath stores answer text in receipts.
- UI-level blocking answer collection is complete.
- Elnath matches Claude Code's ask-user-question UX.
- Benchmark success or superiority.

## Remaining risk

- The answer body remains in the queued follow-up task payload, not the receipt.
- Desktop/UI-level answer collection remains a separate product boundary.

## Next autonomous action

Commit this user-input control-surface slice as one coherent milestone. If
clean, open one PR and let CI decide. Do not run benchmark lanes for this slice.
