# Task/Cron/Plan/Worktree Callable Surface Audit

Date: 2026-05-13
Branch: codex/callable-surface-audit
Lane: ccunpacked reference-parity implementation

## Summary

Task, schedule, plan-mode, and worktree model-callable surfaces are already
present on `main`. The next implementation should not recreate these tools.
Future work should focus on unification/polish only.

## Evidence

Runtime registration in `cmd/elnath/runtime.go`:

- `task_create`
- `task_list`
- `task_get`
- `task_stop`
- `task_output`
- `task_monitor`
- `task_update`
- `schedule_create`
- `schedule_list`
- `schedule_delete`
- `enter_plan_mode`
- `exit_plan_mode`
- `enter_worktree`
- `worktree_list`
- `worktree_run`
- `worktree_prune`
- `exit_worktree`

Completion observability already treats these as control receipts:

- task tools
- schedule tools
- plan-mode tools
- worktree tools

Recent closure evidence:

- PR #151: task monitor timing observability
- PR #154: bounded worktree run tool
- PR #178: command/slash receipt metadata
- PR #180: task/cron follow-up receipts
- PR #188: ToolSearch category/surface routing for task, schedule, plan,
  worktree, process, skill, command, MCP, and built-in surfaces

## Current Classification

| Surface | Status | Evidence |
|---|---|---|
| TaskCreate/Get/List/Output/Stop/Update/Monitor | implemented | `internal/daemon/task_tools.go`, runtime registration, task tool tests |
| Cron/ScheduleCreate/Delete/List | implemented | `internal/scheduler/task_tools.go`, runtime registration, scheduler tool tests |
| EnterPlanMode/ExitPlanMode | implemented | `internal/agent/plan_mode_tools.go`, runtime registration, plan-mode tests |
| EnterWorktree/ExitWorktree/List/Run/Prune | implemented | `internal/worktree/tools.go`, runtime registration, worktree tests |
| Completion receipt propagation | implemented | `completionControlToolReceiptNames`, learning and agentic conversion tests |
| ToolSearch discovery routing | implemented | PR #188 |

## Remaining Gaps

These are polish gaps, not missing-surface gaps:

- one shared user-facing catalog view for task/schedule/plan/worktree tool
  families;
- stronger docs for when to use task tools versus process tools;
- later wait/resume continuation after `ask_user_question`;
- broader self-correction policy beyond current closed retry decisions.

## Claim Boundary

Allowed:

- Elnath already has model-callable task, schedule, plan-mode, and worktree
  surfaces.
- The current lane should not spend effort recreating them.
- `elnath explain control-surfaces` exposes this status as an agent-friendly
  read-only CLI view.

Not claimed:

- Full Claude Code parity is complete.
- Wait/resume is implemented.
- Automatic user-answer continuation is implemented.
- Bounded self-correction is feature-complete.
- Benchmark readiness is implied.

## Next Action

Move to the next actual gap rather than duplicating these surfaces. Best next
candidate: either a short operator-facing catalog/readiness command for control
surfaces, or a bounded self-correction improvement that reduces unnecessary
retry/usage.

## Implemented Follow-up

Added:

```text
elnath explain control-surfaces
elnath explain control-surfaces --json
```

The JSON view reports:

- surface name
- status
- tool names
- ToolSearch discoverability
- receipt-backed status
- honest remaining gaps

## Verification

Focused:

```text
go test ./cmd/elnath -run 'TestExplainControlSurfaces' -count=1
PASS

go test ./cmd/elnath -run 'TestExplain(ControlSurfaces|Timeouts)' -count=1
PASS
```

Broader:

```text
go test ./cmd/elnath -count=1
PASS

go vet ./...
PASS

git diff --check
PASS
```
