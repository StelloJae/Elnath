# Telegram Handoff Notification

Date: 2026-05-17 KST

Branch: `codex/handoff-notification`

Status: local milestone implemented and verified

## Summary

This milestone closes a small session-continuity UX gap from the convergence
gap map: Telegram completion notifications now tell the operator how to resume
or inspect the completed task through the existing `/handoff <task>` surface
when a session is bound.

This is product/runtime polish, not benchmark work.

## Reference Files Inspected

Elnath:

- `internal/telegram/sink.go`
- `internal/telegram/sink_test.go`
- `internal/telegram/shell.go`
- `internal/telegram/shell_test.go`
- `cmd/elnath/cmd_task_handoff.go`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`

Claude Code source:

- `/Users/stello/claude-code-src/src/remote/RemoteSessionManager.ts`
- `/Users/stello/claude-code-src/src/remote/SessionsWebSocket.ts`

Hermes source:

- `/Users/stello/.hermes/hermes-agent/tests/hermes_cli/test_session_handoff.py`
- `/Users/stello/.hermes/hermes-agent/gateway/session_context.py`
- `/Users/stello/.hermes/hermes-agent/gateway/mirror.py`

## What Changed

- `TelegramSink.NotifyCompletion` appends `Handoff: /handoff <task_id>` to the
  completion header when a completion has a non-empty session ID.
- `Shell.NotifyCompletions` appends `handoff: /handoff <task_id>` to polled
  completion notifications when a completion has a non-empty session ID.
- Added `telegramHandoffCommand` to keep the task/session guard in one place.
- Added focused tests covering both streaming sink completion notifications and
  shell-polled completion notifications.

Changed files:

- `internal/telegram/sink.go`
- `internal/telegram/sink_test.go`
- `internal/telegram/shell.go`
- `internal/telegram/shell_test.go`

## Verification

Focused RED before implementation:

- `go test ./internal/telegram -run 'TestSinkNotifyCompletionIncludesHandoffHint|TestShellNotifyCompletionsUpdatesBinder' -count=1`
  - Failed because completion notifications did not include `/handoff <task>`.

Focused GREEN after implementation:

- `go test ./internal/telegram -run 'TestSinkNotifyCompletionIncludesHandoffHint|TestShellNotifyCompletionsUpdatesBinder' -count=1`
  - PASS

Package verification:

- `go test ./internal/telegram -count=1`
  - PASS

Related runtime verification:

- `go test ./cmd/elnath ./internal/telegram ./internal/daemon -count=1`
  - PASS

Static / hygiene checks:

- `git diff --check -- internal/telegram/sink.go internal/telegram/shell.go internal/telegram/sink_test.go internal/telegram/shell_test.go`
  - PASS
- `go vet ./...`
  - PASS

## Claim Boundary

Allowed:

- Telegram completion notifications now include a handoff command hint when a
  task completion has a bound session ID.
- The existing `/handoff <task>` operator surface is now easier to discover from
  completion messages.

Not claimed:

- live runtime migration;
- remote claimant authentication;
- full Hermes handoff parity;
- benchmark readiness;
- benchmark superiority over Codex, Claude Code, or Hermes.

## Risk

- This only improves discoverability. It does not change session ownership,
  lifecycle state semantics, or transfer execution.
- Notifications without a session ID intentionally do not show a handoff hint.

## Next Recommendation

Continue product/runtime convergence with the next structural blocker, likely
gateway handoff lifecycle exposure or richer completion/progress receipt
surfacing. Do not resume v8 benchmark loops from this milestone.
