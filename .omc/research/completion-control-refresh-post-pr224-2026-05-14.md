# Completion control refresh after PR224

Date: 2026-05-14
Branch: `codex/user-input-ui-collection`
Status: implemented locally

## Goal

Prevent the active completion goal from repeating stale milestone guidance.

The final control pointer and completion control document still described the
world mostly as PR #207 through PR #220, while `main` has already shipped
through PR #224 and this branch adds a local pending-question handoff slice.

## Current confirmed state

GitHub PR state checked with `gh pr view`:

- PR #221: `fix(cli): cover subcommand help surfaces`
  - merged: `c9880468760d76bd47d1423e90ce0e45a122a02f`
- PR #222: `feat(runtime): strengthen control surface evidence`
  - merged: `79abd277b1d06fdcd8605e46fd53c8c6377a2f71`
- PR #223: `feat(tools): strengthen code intelligence and todo guard`
  - merged: `12d55ba99575f87206f39d6f2231f7a7ccbe30d5`
- PR #224: `feat(runtime): strengthen control-surface introspection`
  - merged: `ac46150490f6bdcd9124b9fe4a6b7124e37f1b61`

Local post-PR224 branch state:

- `e747541b144f3d734aae74074faae8460ba1e4ec`
  - `feat(runtime): expose pending user answer handoff`

## Changed files

- `.omc/research/elnath-final-completion-program-control-2026-05-14.md`
- `.omc/research/elnath-completion-program-control-2026-05-14.md`
- `.omc/research/completion-control-refresh-post-pr224-2026-05-14.md`

## Behavior / process corrected

- The final control pointer now records PR #221 through PR #224.
- Resume behavior now points at the PR #224 closure and the local pending-answer
  handoff artifact.
- The recommended next lane no longer says to close post-PR213 state first.
- Remaining product boundaries now distinguish implemented runtime/CLI surfaces
  from deferred platform/UI integrations:
  - UI modal answer UX remains outside runtime.
  - async streaming process notification remains deferred.
  - full multi-language LSP lifecycle remains deferred.

## Verification

- `gh pr view 221..224` equivalent loop
  - PASS: PR #221, #222, #223, #224 are all `MERGED` with merge commits listed above.
- `git diff --check`
  - PASS
- `go run ./cmd/elnath explain control-surfaces --json | rg -n "pending-list answer commands|PR #224|UI-level modal|watch_text|LSP|registry/control"`
  - PASS: output includes the updated UI modal, `watch_text`, LSP, and
    registry/control-surface boundaries.
- `go test ./cmd/elnath -run 'TestExplainControlSurfacesText|TestExplainControlSurfacesJSON' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.568s`

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

- Completion control documents now reflect PR #224 and local post-PR224 handoff work.
- Stale post-PR213/post-PR220 restart guidance is corrected.

Forbidden:

- Elnath is complete as a public product.
- Elnath is better than Claude Code or Codex.
- v8 benchmark passed.
- Full UI modal answer collection exists.
- Full async streaming monitor exists.
- Full multi-language LSP lifecycle exists.

## Remaining risk

- This is control-document correction, not a new runtime feature beyond the
  separate pending-answer handoff commit.
- The local branch is not yet PR/CI-verified.

## Next autonomous action

Run focused doc validation and keep this as part of the same local batch. Do
not open a PR until the batch is clearly reviewable.
