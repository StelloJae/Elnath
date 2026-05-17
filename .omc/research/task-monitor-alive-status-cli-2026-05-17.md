# Task Monitor Alive Status CLI

Date: 2026-05-17 KST

Branch:

- `codex/progress-alive-status`

## Scope

This milestone improves product/runtime operator visibility for long-running
daemon tasks.

It does not run benchmarks and does not change task execution behavior.

## Reference Files Inspected

Elnath:

- `cmd/elnath/cmd_task.go`
- `cmd/elnath/cmd_task_test.go`
- `internal/daemon/task_tools.go`
- `internal/daemon/progress.go`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`

Claude Code source:

- `/Users/stello/claude-code-src/src/cli/remoteIO.ts`
- `/Users/stello/claude-code-src/src/main.tsx`

Hermes source/release references:

- `/Users/stello/.hermes/hermes-agent/RELEASE_v0.8.0.md`
- `/Users/stello/.hermes/hermes-agent/cron/scheduler.py`

## Finding

`task_monitor` JSON already included timing evidence:

- `age_seconds`
- `running_seconds`
- `idle_seconds`

But plain CLI output hid those fields. Operators could see `Updated` and
`Observed`, but not a compact alive/idle view.

This left a gap against the convergence goal:

- long-running work should show visible progress and recovery signals;
- background work should not feel silent or stuck;
- operator surfaces should expose existing receipts/evidence instead of
  requiring raw JSON inspection.

## Change

- Added `age_seconds`, `running_seconds`, and `idle_seconds` to the CLI monitor
  view model.
- `elnath task monitor <id>` plain output now prints:
  - `Age`
  - `Running` for running tasks
  - `Idle` for running tasks
- JSON output remains unchanged and machine-readable.

## TDD Evidence

Red:

- `go test ./cmd/elnath -run TestCmdTaskMonitorWithQueueShowsRunningAndIdleAge -count=1`
  failed because plain monitor output did not include `Age`, `Running`, or
  `Idle`.

Green:

- `go test ./cmd/elnath -run 'TestCmdTaskMonitorWithQueue(ShowsRunningAndIdleAge|ShowsSnapshot|RendersStructuredProgress)' -count=1`
  passed.

## Claim Boundary

Allowed:

- Elnath CLI task monitor now exposes existing alive/idle timing evidence in
  plain output.
- This improves product/runtime operator visibility for long-running tasks.

Forbidden:

- Full rich TUI progress timeline.
- Remote keep-alive protocol parity with Claude Code.
- Full Hermes continuity parity.
- Benchmark readiness or superiority claim.

## Remaining Risk

- Telegram/gateway progress formatting remains separate.
- No richer timeline or streaming TUI was added.
- This does not change daemon timeout policy.

## Next Recommendation

Continue product/runtime completion with either:

1. Telegram/gateway progress formatting, or
2. session handoff automatic cross-surface notification.
