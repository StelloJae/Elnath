# Task Progress CLI Render Polish

Date: 2026-05-17 KST

Branch:

- `codex/user-input-operator-ux`

## Goal

Improve Elnath's long-running task visibility in the product/runtime lane.

The concrete gap:

- Daemon progress already uses structured `elnath.progress.v1` envelopes.
- `elnath daemon status` had structured-progress rendering coverage.
- `elnath task monitor` and `elnath task output --field progress` plain text
  could still show raw JSON envelopes.

## References Inspected

Elnath:

- `cmd/elnath/cmd_task.go`
- `cmd/elnath/cmd_task_test.go`
- `internal/daemon/progress.go`
- `internal/daemon/task_tools.go`
- `internal/daemon/delivery_test.go`

Reference intent:

- Codex/Claude-style terminal status should be human-readable by default.
- Hermes-style long-running work should keep a visible, non-log-like alive
  signal.

## Change

Plain text task progress rendering now calls `daemon.RenderProgress`:

- `elnath task monitor <id>` renders structured progress as a concise message.
- `elnath task output <id> --field progress` renders structured progress as a
  concise message.
- `--json` output remains raw and machine-readable.

Example:

```text
Progress:     bash: go test ./cmd/elnath (running)
```

instead of:

```json
{"version":"elnath.progress.v1", ...}
```

## Changed Files

- `cmd/elnath/cmd_task.go`
- `cmd/elnath/cmd_task_test.go`

## Verification

Red first:

- `go test ./cmd/elnath -run 'TestCmdTask(MonitorWithQueueRendersStructuredProgress|OutputWithQueueRendersStructuredProgress)' -count=1`
  failed before implementation because raw progress JSON was printed.

Green:

- `go test ./cmd/elnath -run 'TestCmdTask(MonitorWithQueueRendersStructuredProgress|OutputWithQueueRendersStructuredProgress|MonitorWithQueueShowsSnapshot|OutputWithQueueReturnsTail)' -count=1`
  passed.
- `go test ./cmd/elnath -count=1` passed.
- `go test ./internal/daemon -run 'Test(DeliveryRouter_OnProgressParsesAndRoutes|TaskOutputToolReadsProgressField|TaskMonitorTool)' -count=1`
  passed.
- `git diff --check -- cmd/elnath/cmd_task.go cmd/elnath/cmd_task_test.go`
  passed.

## Benchmark / Corpus Boundary

- No benchmark run.
- No baseline run.
- No corpus mutation.
- No public superiority claim.

## Claim Boundary

Allowed:

- Plain text task monitor/output progress now renders structured progress as
  human-readable status.
- JSON output remains machine-readable.
- This improves product/operator feel for long-running task observation.

Forbidden:

- Do not claim full Codex/Claude TUI parity.
- Do not claim full Hermes progress bridge parity.
- Do not claim benchmark readiness from this UX slice.

## Remaining Risk

- This only changes CLI plain text rendering.
- Rich streaming progress UI remains separate.
- Telegram delivery already has progress routing, but gateway-specific
  formatting remains a separate product surface.

## Next Recommendation

Continue batching local product/runtime polish. Good next candidates:

1. terminal-native user-input choice UX;
2. gateway handoff lifecycle exposure;
3. PR-ready branch cleanup after enough UX work is batched.
