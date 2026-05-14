# User question pending answer handoff

Date: 2026-05-14
Branch: `codex/user-input-ui-collection`
Status: implemented locally

## Goal

Reduce the remaining UI-level user-answer collection gap without pretending a
desktop/modal UI exists.

The runtime already had `ask_user_question`, `user_question_list`,
`user_question_wait`, and strict `user_question_answer`. The missing local
handoff was that pending-question views did not carry the exact answer command
an operator can use to resume the session.

## References inspected

- Elnath:
  - `internal/agent/user_question_tool.go`
  - `internal/learning/pending_questions.go`
  - `internal/learning/user_question_tools.go`
  - `cmd/elnath/cmd_explain.go`
  - `internal/daemon/task_tools.go`
- Claude Code:
  - `/Users/stello/claude-code-src/src/tools/AskUserQuestionTool/AskUserQuestionTool.tsx`
  - `/Users/stello/claude-code-src/src/components/permissions/AskUserQuestionPermissionRequest/AskUserQuestionPermissionRequest.tsx`
  - `/Users/stello/claude-code-src/src/services/tools/toolHooks.ts`
  - `/Users/stello/claude-code-src/src/utils/messages.ts`
- Hermes:
  - `/Users/stello/.hermes/hermes-agent/tools/clarify_tool.py`
  - `/Users/stello/.hermes/hermes-agent/run_agent.py`
  - `/Users/stello/.hermes/hermes-agent/cli.py`
- claw-code:
  - `/Users/stello/claw-code/rust/crates/tools/src/lib.rs`
  - `/Users/stello/claw-code/PARITY.md`
  - `/Users/stello/claw-code/rust/PARITY.md`

## Reference interpretation

Claude Code treats user questions as a real interactive permission/UI flow:
the tool requires user interaction, the permission component collects answers,
and the answer is returned as tool input/result.

Hermes keeps a thinner tool schema and delegates actual user interaction to a
platform callback. claw-code has a direct stdin-based implementation in the
Rust tool layer, while its parity notes still call full interactive UI wiring a
limited/stubbed area.

Elnath's current shape is daemon/CLI-first, not a TUI/modal app. The durable
fix for this milestone is therefore not to claim full UI parity, but to make
the existing receipt-backed CLI handoff actionable from every pending-question
view.

## Changed files

- `internal/learning/pending_questions.go`
- `internal/learning/pending_questions_test.go`
- `internal/agent/user_question_tool.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`

## Behavior added

- `PendingUserQuestion` now includes:
  - `answerable`
  - `answer_command`
  - `pending_command`
- Pending questions with a session-bound request now expose:
  - `elnath task answer --session '...' --request '...' --answer 'ANSWER_TEXT'`
  - `elnath explain pending-questions --session '...'`
- Unbound pending questions remain not answerable.
- `ask_user_question` and pending-question lookup now share the same command
  builder.
- `elnath explain pending-questions` text output shows the answer command.
- `elnath explain control-surfaces` gap wording now distinguishes UI modal
  collection from implemented pending-list answer commands.

## Verification

- `go test ./internal/learning -run 'TestPendingUserQuestions|TestFindPendingUserQuestion' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/learning 0.609s`
- `go test ./internal/agent -run 'TestAskUserQuestionTool' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/agent 0.669s`
- `go test ./cmd/elnath -run 'TestExplainPendingQuestions|TestExplainControlSurfaces' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.765s`
- `go test ./cmd/elnath ./internal/agent ./internal/learning -count=1`
  - PASS:
    - `ok github.com/stello/elnath/cmd/elnath 33.226s`
    - `ok github.com/stello/elnath/internal/agent 13.130s`
    - `ok github.com/stello/elnath/internal/learning 1.888s`
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

- Pending user-question views now include actionable answer commands.
- Runtime/CLI user-input handoff is stronger and receipt-backed.

Forbidden:

- Full UI-level modal answer collection is complete.
- Elnath is complete as a public product.
- Elnath is better than Claude Code or Codex.
- v8 benchmark passed.

## Remaining risk

- This does not add a desktop/TUI modal or Codex-App-like answer selector.
- Multi-question/multi-select UX remains outside the current Elnath runtime
  shape.
- This is a local milestone, not yet PR/CI-verified.

## Next autonomous action

Keep batching locally. Inspect the next remaining structural blocker from the
control document, likely either streaming line-watch monitor polish or final
control-surface closeout wording, before opening a single coherent PR.
