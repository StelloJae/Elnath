# Elnath final completion program control

Date: 2026-05-14
Status: standing control pointer

## Purpose

This file exists because the active goal may point at this exact path.

The detailed standing authority remains:

- `.omc/research/elnath-completion-program-control-2026-05-14.md`

Read that document first, then this pointer, then the latest closeout artifacts.

## Current state

The supervisor/control-loop/tool/provider structural program has moved past the original A-G milestone list.

Merged structural PRs now include:

- PR #207: shell command intent receipts
- PR #208: process command intent receipts
- PR #209: process timeout receipts
- PR #210: process timeout policy in `elnath explain timeouts`
- PR #211: bounded `process_wait`
- PR #212: bounded `user_question_wait`
- PR #213: refreshed control-surface gap wording and broad runtime health artifact

Do not restart old Milestone C/G work unless fresh evidence proves a regression.

## Required resume behavior

1. Confirm branch, HEAD, `origin/main`, dirty files, and open PRs.
2. Preserve unrelated dirty files in `/Users/stello/elnath`.
3. Read:
   - `.omc/research/elnath-completion-program-control-2026-05-14.md`
   - `.omc/research/completion-program-post-pr213-closeout-2026-05-14.md`
   - `.omc/research/control-surface-gap-refresh-2026-05-14.md`
   - `.omc/research/completion-program-runtime-health-check-2026-05-14.md`
4. Choose the next structural blocker from current code evidence.
5. Prefer no benchmark work until the post-PR213 closeout is understood.

## Current recommended next lane

First close the post-PR213 structural evidence state.

Then choose exactly one:

- tiny current-only control smoke to validate receipt behavior in benchmark environment; or
- code-intelligence/LSP design slice; or
- UI-level answer collection design slice.

Do not run full v8, baseline, Codex comparison, Claude comparison, or superiority lanes from this document.

## Claim boundary

Allowed:

- Elnath has stronger supervisor/control-loop/tool/provider structure after PR #207-#213.
- Local `go test ./... -count=1` and `go vet ./...` were clean in the PR #213 evidence artifact.

Forbidden:

- Elnath is complete as a public product.
- Elnath is better than Claude Code or Codex.
- v8 benchmark passed.
- baseline/comparison evidence exists.
- full LSP lifecycle exists.
- UI-level answer collection is complete.
