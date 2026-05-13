# Plan / Worktree Follow-up Receipts

Date: 2026-05-13
Branch: codex/plan-worktree-receipts
Lane: ccunpacked reference-parity control surface
Milestone estimate after local verification: 66%

## Objective

Tighten plan/worktree callable-tool receipts so autonomous loops can see the next bounded tool to use after state transitions or registry mutations.

This is a control-loop polish slice, not a benchmark lane.

## Change

- `enter_plan_mode` receipts now include `followup_tool: exit_plan_mode`.
- `exit_plan_mode` remains terminal and does not emit a follow-up hint.
- `enter_worktree` receipts now include `followup_tool: worktree_run`.
- `exit_worktree` receipts now include `followup_tool: worktree_list`.
- `worktree_prune` dry-runs now include `followup_tool: worktree_prune`.
- `worktree_prune` removal runs now include `followup_tool: worktree_list`.

## Evidence

TDD red checks first failed because the receipt schemas did not yet expose `followup_tool`:

- `go test ./internal/agent -run TestPlanModeToolsSwitchAndRestorePermissionMode -count=1`
- `go test ./internal/worktree -run 'TestEnterWorktreeCreatesRegistryAndReusesExisting|TestExitWorktreeRequiresCleanOrDiscard|TestWorktreePruneToolDefaultsToDryRun|TestWorktreePruneToolRemovesMissingRegistryEntries' -count=1`

Focused green checks:

- `go test ./internal/agent -run TestPlanModeToolsSwitchAndRestorePermissionMode -count=1` PASS
- `go test ./internal/worktree -run 'TestEnterWorktreeCreatesRegistryAndReusesExisting|TestExitWorktreeRequiresCleanOrDiscard|TestWorktreePruneToolDefaultsToDryRun|TestWorktreePruneToolRemovesMissingRegistryEntries' -count=1` PASS

Broader checks:

- `go test ./internal/agent ./internal/worktree ./cmd/elnath -count=1` PASS
- `git diff --check` PASS

## Claim Boundary

Allowed:

- Plan/worktree tool receipts now include bounded follow-up hints where useful.
- The hints are structured fields, not free-text prompt injection material.
- The change improves autonomous control-loop observability.

Not claimed:

- No new plan/worktree execution capability.
- No permission policy change.
- No automatic self-correction guarantee.
- No benchmark result.
- No v8, baseline, Codex CLI, or Claude Code comparison evidence.

## Next Action

Run `go vet ./...`, then either broaden local verification or batch this with the next nearby control-surface polish before opening a PR.
