# Terminal User Question Interactive Answer Milestone

Date: 2026-05-18 KST

Branch:

- `codex/runtime-progress-status`

Goal lane:

- Codex-Claude-Hermes convergence
- Product/runtime first
- Benchmark second

## Summary

This milestone improves the terminal-native user-input path for pending
questions.

Before this change, Elnath had correct receipts and command-driven answer
flows:

- `ask_user_question`
- `elnath explain pending-questions`
- `elnath task answer --choice N`
- Telegram numeric choice and inline-button paths

The remaining terminal gap was that an operator still had to copy a command
instead of answering through a small interactive terminal prompt.

## References Inspected

Elnath:

- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`
- `cmd/elnath/cmd_task.go`
- `cmd/elnath/cmd_task_test.go`
- `internal/learning/pending_questions.go`
- `internal/daemon/task_tools.go`
- `internal/telegram/shell.go`

Claude Code:

- `/Users/stello/claude-code-src/src/components/CustomSelect`
- `/Users/stello/claude-code-src/src/tools/AskUserQuestionTool`
- `/Users/stello/claude-code-src/src/skills/bundled/skillify.ts`

Hermes:

- `/Users/stello/.hermes/hermes-agent/CONTRIBUTING.md`
- `/Users/stello/.hermes/hermes-agent/README.zh-CN.md`

## Changed Behavior

Added:

- `elnath task answer --interactive`
- `elnath task answer --interactive --session <id>`
- `elnath task answer --interactive --session <id> --request <id>`

Interactive mode:

- loads current pending user questions from outcome receipts;
- selects the only matching pending question automatically;
- asks the operator to choose a pending question when multiple are available;
- displays question text and numbered options;
- accepts a numeric answer or free text depending on existing question policy;
- reuses the existing `user_question_answer` validator and queue-backed resume
  path.

## Product Impact

Before:

- terminal operators needed to copy `elnath task answer ... --choice N`.

After:

- terminal operators can answer in-place:

```text
elnath task answer --interactive --session sess-123
Question: Which branch?
Choices:
  1. main
  2. new
Choice number:
```

This moves Elnath closer to Claude Code-style interactive choice UX and
Hermes-style clarify fallback while staying CLI-native.

## Changed Files

- `cmd/elnath/cmd_task.go`
- `cmd/elnath/cmd_task_test.go`
- `.omc/research/terminal-user-question-interactive-answer-2026-05-18.md`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`

## Verification

Focused verification:

- `go test ./cmd/elnath -run TestCmdTaskAnswerWithQueueInteractiveChoice -count=1`
- Result: PASS

Additional verification:

- `go test ./cmd/elnath -run 'TestCmdTaskAnswerWithQueue(InteractiveChoice|AcceptsChoiceFlag|EnqueuesBoundAnswer|RejectsStaleRequest|RejectsTimedOutRequest)' -count=1`
- Result: PASS

- `go test ./cmd/elnath -count=1`
- Result: PASS

- `go vet ./...`
- Result: PASS

- `git diff --check`
- Result: PASS

## Benchmark Boundary

No benchmark lane was run.

- Full v8 benchmark: not run
- Baseline: not run
- Codex comparison: not run
- Claude comparison: not run
- Corpus mutation: none
- Baseline mutation: none

## Claim Boundary

Allowed:

- Elnath terminal operators can answer pending user questions through an
  interactive CLI prompt.
- The interactive prompt reuses existing receipt-backed answer validation and
  queue resume behavior.

Not claimed:

- full Claude Code TUI modal parity;
- multi-select question support;
- signed remote callback tokens;
- benchmark success;
- Codex/Claude/Hermes superiority.

## Remaining Risk

- This is a simple stdin/stdout prompt, not a full TUI modal.
- Multiple pending questions are selectable, but no fuzzy search/filtering is
  implemented.
- Multi-select remains outside scope.

## Next Recommendation

Continue product/runtime completion with session handoff/resume recap polish or
operator timeline/status view, then batch the branch into one coherent PR.
