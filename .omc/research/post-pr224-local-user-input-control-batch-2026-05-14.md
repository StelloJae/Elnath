# Post-PR224 local user-input/control batch

Date: 2026-05-14
Branch: `codex/user-input-ui-collection`
Status: local batch, PR-ready after focused verification

## Summary

This is a local-first batch after PR #224. It avoids PR-per-slice churn and
keeps the work focused on structural completion gaps rather than benchmark
loops.

Local commits:

- `e747541b144f3d734aae74074faae8460ba1e4ec`
  - `feat(runtime): expose pending user answer handoff`
- `7908b8100385384fa4d49d85e789b7d5df941326`
  - `docs(control): refresh completion state after pr224`

## Why this batch exists

After PR #224, the remaining completion-program risks were no longer centered
on benchmark execution. Two practical gaps remained:

1. Pending user-question views were not actionable enough for a CLI/daemon
   operator.
2. The standing completion control documents still described the world as
   mostly PR #207 through PR #220, which could cause stale goal restarts.

## Changed behavior

- Pending questions now include:
  - `answerable`
  - `answer_command`
  - `pending_command`
- `elnath explain pending-questions` text output shows the exact answer command.
- `ask_user_question` and pending-question lookup share the same command
  builder.
- Completion control docs now record PR #221 through PR #224 and the local
  post-PR224 pending-answer handoff slice.
- Control-surface wording distinguishes implemented runtime/CLI handoff from
  deferred UI modal collection.

## Changed files

- `.omc/research/elnath-completion-program-control-2026-05-14.md`
- `.omc/research/elnath-final-completion-program-control-2026-05-14.md`
- `.omc/research/completion-control-refresh-post-pr224-2026-05-14.md`
- `.omc/research/user-question-pending-answer-handoff-2026-05-14.md`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`
- `internal/agent/user_question_tool.go`
- `internal/learning/pending_questions.go`
- `internal/learning/pending_questions_test.go`

## References inspected

- Claude Code:
  - `AskUserQuestionTool`
  - `AskUserQuestionPermissionRequest`
  - `StreamingToolExecutor`
  - user-question / plan-mode prompt guidance
- Hermes:
  - `clarify_tool.py`
  - `run_agent.py` clarify dispatch
  - `cli.py` clarify callback
- claw-code:
  - Rust `AskUserQuestion` tool implementation
  - `PARITY.md` / `rust/PARITY.md` user-question limitations

## Verification

Focused:

- `go test ./internal/learning -run 'TestPendingUserQuestions|TestFindPendingUserQuestion' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/learning 0.609s`
- `go test ./internal/agent -run 'TestAskUserQuestionTool' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/agent 0.669s`
- `go test ./cmd/elnath -run 'TestExplainPendingQuestions|TestExplainControlSurfaces' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.765s`
- `go run ./cmd/elnath explain control-surfaces --json | rg -n "pending-list answer commands|PR #224|UI-level modal|watch_text|LSP|registry/control"`
  - PASS

Affected packages:

- `go test ./cmd/elnath ./internal/agent ./internal/learning -count=1`
  - PASS:
    - `ok github.com/stello/elnath/cmd/elnath 25.417s`
    - `ok github.com/stello/elnath/internal/agent 14.095s`
    - `ok github.com/stello/elnath/internal/learning 2.302s`
- `go vet ./cmd/elnath ./internal/agent ./internal/learning`
  - PASS
- `git diff --check origin/main..HEAD`
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

- Local post-PR224 batch improves runtime/CLI user-input handoff.
- Local post-PR224 batch corrects stale completion-control state after PR #224.

Forbidden:

- Elnath is complete as a public product.
- Elnath is better than Claude Code or Codex.
- v8 benchmark passed.
- Full UI modal answer collection exists.
- Full async streaming monitor exists.
- Full multi-language LSP lifecycle exists.

## Remaining risk

- The branch has not been pushed or CI-verified yet.
- UI modal collection, async process notifications, and full LSP lifecycle
  remain product/platform boundaries, not runtime-completion blockers.

## Next recommendation

Open one coherent PR for this local batch if integration is desired. Do not
split it into smaller PRs.
