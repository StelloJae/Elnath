# Session Handoff Transition Guard

Date: 2026-05-17 KST

Branch: `codex/handoff-notification`

Status: local milestone implemented and verified

## Summary

This milestone tightens Elnath's session handoff lifecycle. Before this change,
`Session.RecordHandoff` accepted any known handoff state in any order. That made
stale or unrelated gateway/operator writes possible, such as recording a fresh
`running` state after a handoff had already completed.

Elnath now validates handoff state transitions before appending a handoff event.

## Reference Files Inspected

Elnath:

- `internal/agent/session.go`
- `internal/agent/session_test.go`
- `cmd/elnath/cmd_task_handoff.go`
- `cmd/elnath/cmd_task_test.go`
- `internal/telegram/shell.go`
- `internal/telegram/shell_test.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/tests/hermes_cli/test_session_handoff.py`

Claude Code:

- `/Users/stello/claude-code-src/src/remote/RemoteSessionManager.ts`
- `/Users/stello/claude-code-src/src/remote/SessionsWebSocket.ts`

## What Changed

- `Session.RecordHandoff` now loads the latest persisted handoff status before
  appending a new handoff event.
- Added `validSessionHandoffTransition`.
- Allowed lifecycle:
  - no prior state -> `requested`, `claimed`, or `running`
  - `requested` -> `claimed`, `running`, or `failed`
  - `claimed` -> `running`, `completed`, or `failed`
  - `running` -> `completed` or `failed`
  - terminal `completed` / `failed` -> `requested` only, for explicit retry
- Added regression coverage for invalid first-terminal writes, stale active
  writes after terminal completion, and terminal retry through `requested`.

Changed files:

- `internal/agent/session.go`
- `internal/agent/session_test.go`

## Verification

Focused RED before implementation:

- `go test ./internal/agent -run TestRecordHandoffRejectsInvalidTransition -count=1`
  - Failed because `completed` could be recorded with no active handoff.

Focused GREEN after implementation:

- `go test ./internal/agent -run 'TestRecordHandoff(AndLoadStatus|RejectsUnknownState|RejectsInvalidTransition)' -count=1`
  - PASS

Gateway/CLI compatibility:

- `go test ./cmd/elnath ./internal/telegram ./internal/agent -run 'TestRecordHandoff|TestCmdTaskHandoffWithQueue|TestShellHandoffCommand' -count=1`
  - PASS

Related runtime verification:

- `go test ./cmd/elnath ./internal/telegram ./internal/daemon ./internal/agent -count=1`
  - PASS

Static / hygiene checks:

- `go vet ./...`
  - PASS
- `git diff --check`
  - PASS

## Claim Boundary

Allowed:

- Elnath now rejects invalid session handoff state transitions at the persisted
  session layer.
- Terminal handoff states can be retried only by starting a new `requested`
  handoff.

Not claimed:

- distributed atomic claim across multiple gateway processes;
- remote claimant authentication;
- live runtime migration;
- full Hermes handoff parity.

## Risk

- Direct first-state `claimed` and `running` remain allowed for compatibility
  with existing CLI/Telegram operator flows.
- This is a local JSONL append guard, not a database-level compare-and-swap
  handoff lock.

## Next Recommendation

Keep this branch as one coherent handoff/continuity PR candidate after one final
diff review. Do not run benchmark lanes from this work.
