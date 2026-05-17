# Task Pending Handoffs Surfaces

Date: 2026-05-17 KST

Branch: `codex/handoff-pending`

Status: local milestone implemented and verified

## Summary

This milestone adds operator-visible pending handoff surfaces:

- `elnath task handoffs`
- `elnath task handoffs --json`
- Telegram `/handoffs`

The command lists sessions whose latest handoff state is `requested`, excluding
already claimed/running/completed/failed handoffs. Each item includes the
existing claim command so an operator can immediately move from discover to
claim:

```text
elnath task handoff <id> --state claimed --surface cli
```

This follows the Hermes reference pattern of `list_pending_handoffs` before
claiming, while keeping Elnath's implementation Go-native and queue/session
JSONL based.

## Reference Files Inspected

Elnath:

- `cmd/elnath/cmd_task.go`
- `cmd/elnath/cmd_task_handoff.go`
- `cmd/elnath/cmd_task_test.go`
- `internal/telegram/shell.go`
- `internal/telegram/shell_test.go`
- `internal/daemon/queue.go`
- `internal/agent/session.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/tests/hermes_cli/test_session_handoff.py`

Claude Code:

- `/Users/stello/claude-code-src/src/remote/RemoteSessionManager.ts`
- `/Users/stello/claude-code-src/src/remote/SessionsWebSocket.ts`

## What Changed

- Added `task handoffs` subcommand.
- Added `cmdTaskHandoffsWithQueue`.
- Added pending handoff list view with plain text and JSON output.
- Added Telegram `/handoffs`.
- Pending list includes:
  - task ID
  - queue status
  - session ID
  - latest handoff state/surface/principal/reason
  - task summary
  - claim command
- Pending list intentionally includes only `requested` handoffs.
- Running/claimed/terminal handoffs are excluded.
- Telegram `/help` now lists `/handoffs`.

Changed files:

- `cmd/elnath/cmd_task.go`
- `cmd/elnath/cmd_task_handoff.go`
- `cmd/elnath/cmd_task_test.go`
- `internal/telegram/shell.go`
- `internal/telegram/shell_test.go`

## Verification

Focused RED before implementation:

- `go test ./cmd/elnath -run 'TestCmdTaskHandoffsWithQueue' -count=1`
  - Failed because `cmdTaskHandoffsWithQueue` did not exist.

Focused GREEN after implementation:

- `go test ./cmd/elnath -run 'TestCmdTaskHandoffsWithQueue' -count=1`
  - PASS

Telegram GREEN:

- `go test ./internal/telegram -run TestShellHandoffsCommandListsPendingOnly -count=1`
  - PASS

Related handoff verification:

- `go test ./cmd/elnath ./internal/agent -run 'TestCmdTaskHandoff|TestCmdTaskHandoffs|TestRecordHandoff' -count=1`
  - PASS

Related runtime verification:

- `go test ./cmd/elnath ./internal/telegram ./internal/agent ./internal/daemon -count=1`
  - PASS

Static / hygiene checks:

- `go vet ./...`
  - PASS
- `git diff --check`
  - PASS

## Claim Boundary

Allowed:

- Elnath can list pending requested handoffs from task/session state.
- Elnath exposes the operator claim command alongside each pending handoff.
- Telegram operators can list pending requested handoffs with `/handoffs`.

Not claimed:

- distributed atomic gateway claim;
- remote claimant authentication;
- automatic gateway watcher loop;
- live runtime migration;
- full Hermes handoff parity;
- benchmark readiness or superiority.

## Risk

- Listing scans queue tasks and loads session handoff status from JSONL. This is
  acceptable for current operator scale, but not a high-volume indexed handoff
  table.
- Missing session files are skipped; malformed readable session files still
  return an error.

## Next Recommendation

This is now a coherent handoff/gateway PR candidate. Do not run benchmark lanes
from this work.
