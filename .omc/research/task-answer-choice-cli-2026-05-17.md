# Task Answer Choice CLI

Date: 2026-05-17 KST

Branch:

- `codex/user-input-operator-ux`

## Goal

Make terminal user-input answers clearer for structured choices.

The concrete gap:

- Numeric structured choices already worked through `--answer 2`.
- That was behaviorally correct but semantically vague for operators.
- A terminal operator should be able to say "choose option 2" explicitly.

## References Inspected

Elnath:

- `cmd/elnath/cmd_task.go`
- `cmd/elnath/cmd_task_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`
- `internal/daemon/task_tools.go`
- `internal/learning/pending_questions.go`

Reference intent:

- Claude Code AskUserQuestion and Hermes clarify flows make structured choices
  obvious to the operator.
- Elnath should keep the same receipt-backed answer path while improving the
  command shape.

## Change

`elnath task answer` now accepts:

```bash
elnath task answer --session sess-1 --request req-1 --choice 2
```

Behavior:

- `--choice N` is parsed as the answer value;
- existing validator-backed normalization turns `2` into the selected option;
- `--answer TEXT` remains supported for free text or exact option text;
- using both `--answer` and `--choice` returns a clear error;
- `elnath explain pending-questions` now renders numeric choices using
  `--choice N` rather than `--answer 'N'`.

## Changed Files

- `cmd/elnath/cmd_task.go`
- `cmd/elnath/cmd_task_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`

## Verification

Red first:

- `go test ./cmd/elnath -run 'TestCmdTaskAnswerWithQueueAcceptsChoiceFlag|TestExplainPendingQuestionsTextShowsAnswerCommand' -count=1`
  failed before implementation because `--choice` was unknown and
  `explain pending-questions` still suggested `--answer 'N'`.

Green:

- `go test ./cmd/elnath -run 'TestCmdTaskAnswerWithQueue(AcceptsChoiceFlag|EnqueuesBoundAnswer|RejectsStaleRequest)|TestExplainPendingQuestionsTextShowsAnswerCommand' -count=1`
  passed.
- `go test ./cmd/elnath -count=1` passed.
- `go test ./internal/daemon -run 'TestUserQuestionAnswerTool' -count=1`
  passed.
- `git diff --check -- cmd/elnath/cmd_task.go cmd/elnath/cmd_task_test.go cmd/elnath/cmd_explain.go cmd/elnath/cmd_explain_test.go`
  passed.

## Benchmark / Corpus Boundary

- No benchmark run.
- No baseline run.
- No corpus mutation.
- No public superiority claim.

## Claim Boundary

Allowed:

- Terminal operators can answer structured pending questions with `--choice N`.
- Pending-question explanation now advertises explicit choice commands.
- The underlying answer receipt and daemon queue path remain unchanged.

Forbidden:

- Do not claim terminal modal UI parity.
- Do not claim Claude Code AskUserQuestion parity.
- Do not claim product completion from this small slice.

## Remaining Risk

- This is still command-driven, not an interactive picker.
- Multi-select and rich terminal modal UX remain outside scope.

## Next Recommendation

This branch now has a coherent operator UX batch. Before more feature work,
consider either:

1. open one draft PR for the batched local UX milestones; or
2. continue with one more closely related gateway/handoff UX slice if PR #254
   remains parked.
