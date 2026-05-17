# Telegram Handoff Command

Date: 2026-05-17 KST

Branch:

- `codex/user-input-operator-ux`

## Goal

Expose Elnath's session handoff/recap path on the Telegram operator surface.

The concrete gap:

- CLI could generate task handoff recaps and record handoff lifecycle states.
- Telegram operator shell had no direct `/handoff` command.
- Hermes-style continuity should be available from the surface where the task
  is being operated.

## References Inspected

Elnath:

- `internal/telegram/shell.go`
- `internal/telegram/shell_test.go`
- `cmd/elnath/cmd_daemon.go`
- `cmd/elnath/cmd_telegram.go`
- `cmd/elnath/cmd_task_handoff.go`
- `internal/agent/session.go`

Reference intent:

- Hermes `/handoff` makes session transfer visible and operator-driven.
- Elnath should keep its queue/session JSONL contract and expose a bounded
  Telegram command rather than adding broad live migration.

## Change

Telegram shell now supports:

```text
/handoff <task_id>
/handoff <task_id> claimed taking over from phone
```

Behavior:

- `WithShellDataDir` passes the Elnath data directory into the Telegram shell.
- Daemon-embedded Telegram shell and standalone `elnath telegram shell` both
  provide `cfg.DataDir`.
- `/handoff <task_id>` renders a compact task/session recap:
  - task id;
  - status;
  - shortened session id;
  - CLI resume command;
  - summary;
  - latest handoff state when present;
  - last session messages.
- `/handoff <task_id> <state> [reason]` records a session handoff lifecycle
  state through the existing session JSONL metadata path.
- `request` is accepted as an alias for `requested`.

## Changed Files

- `internal/telegram/shell.go`
- `internal/telegram/shell_test.go`
- `cmd/elnath/cmd_daemon.go`
- `cmd/elnath/cmd_telegram.go`

## Verification

Red first:

- `go test ./internal/telegram -run 'TestShellHandoffCommand(RendersTaskRecap|RecordsLifecycleState)' -count=1`
  failed before implementation because `WithShellDataDir` and `/handoff`
  command support did not exist.

Green:

- `go test ./internal/telegram -run 'TestShellHandoffCommand(RendersTaskRecap|RecordsLifecycleState)' -count=1`
  passed.
- `go test ./internal/telegram -count=1` passed.
- `go test ./cmd/elnath -run 'Test(CommandHelpers|CmdTelegram|CmdDaemon|Telegram|Daemon)' -count=1`
  passed.
- `git diff --check -- internal/telegram/shell.go internal/telegram/shell_test.go cmd/elnath/cmd_daemon.go cmd/elnath/cmd_telegram.go`
  passed.

## Benchmark / Corpus Boundary

- No benchmark run.
- No baseline run.
- No corpus mutation.
- No public superiority claim.

## Claim Boundary

Allowed:

- Telegram operators can request a handoff recap for a task.
- Telegram operators can record handoff lifecycle states for a task session.
- This improves Hermes-style continuity on the Telegram operator surface.

Forbidden:

- Do not claim full Hermes live `/handoff` parity.
- Do not claim live runtime migration between devices/processes.
- Do not claim product completion from this slice alone.

## Remaining Risk

- Telegram command uses existing session JSONL and queue state; it does not
  move a running process.
- Handoff state is operator-recorded metadata, not remote claimant auth.
- Rich cross-surface handoff notifications remain future work.

## Next Recommendation

This UX batch is now coherent. Next best action:

1. run one broad local verification over the changed packages;
2. decide whether to open one draft PR for the whole batch after resolving or
   sequencing PR #254.
