# Control Surface Gap Refresh

Date: 2026-05-14
Branch: `codex/completion-gap-audit`
Status: implemented locally

## Goal

Refresh `elnath explain control-surfaces` remaining-gap wording after the
post-PR210/211/212 control-surface milestones.

## Evidence

Current `origin/main` now reports:

- `process`: `process_start`, `process_monitor`, `process_wait`, `process_stop`
- `user_input`: `ask_user_question`, `user_question_list`,
  `user_question_wait`, `user_question_answer`

But the remaining gap text still says blocking wait state is missing. That is
stale after `process_wait` and `user_question_wait`.

## Boundary

- This does not claim full UI-level answer collection.
- This does not claim streaming line-watch process monitoring.
- This does not claim full LSP lifecycle.
- No benchmark/baseline/comparison work.

## Changed Files

- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`

## Behavior Added

- Removed stale `blocking wait state` wording from
  `elnath explain control-surfaces`.
- Remaining gaps now distinguish:
  - runtime wait receipts are implemented
  - UI-level answer collection remains outside runtime
  - bounded `process_wait` exists
  - streaming line-watch remains deferred
  - `code_symbols` exists
  - full LSP remains deferred

## Verification

- `go test ./cmd/elnath -run 'TestExplainControlSurfacesJSON|TestExplainControlSurfacesText|TestControlSurfaceManifestMatchesToolSearchRouting' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.659s`
- `go run ./cmd/elnath explain control-surfaces --json | rg -n "blocking wait|user_question_wait|process_wait|UI-level|LSP"`
  - PASS: output includes `user_question_wait`, `process_wait`, UI/LSP boundaries,
    and no stale `blocking wait` line
- `go test ./cmd/elnath -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 19.249s`
- `git diff --check`
  - PASS

## Claim Boundary

Allowed:

- `elnath explain control-surfaces` now reflects post-wait-tool reality more
  accurately.

Forbidden:

- Full UI-level answer collection is complete.
- Streaming process line-watch exists.
- Full LSP lifecycle exists.
- Elnath benchmark success.
