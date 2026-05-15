# Product Runtime Session Retirement Enforcement

Date: 2026-05-15
Branch: `codex/product-runtime-watchdog`
Parent milestone commit: `a8f74d5c3f049b6f2b5528207d8bf00118aac7b1`

## Purpose

The previous milestone added receipt metadata for failures that should retire a
session. This milestone makes that metadata operational: retirable daemon
failures now write a `retire` metadata line into the session JSONL, and
conversation resume paths reject retired sessions instead of silently reusing
stale state.

This remains product/runtime work. No benchmark lane was run.

## References Inspected

Hermes narrow references:

- `/Users/stello/.hermes/hermes-agent/agent/transports/codex_app_server_session.py`
- `/Users/stello/.hermes/hermes-agent/tests/agent/transports/test_codex_app_server_session.py`

Claude Code narrow references:

- `/Users/stello/claude-code-src/src/remote/SessionsWebSocket.ts`
- `/Users/stello/claude-code-src/src/tasks/LocalAgentTask/LocalAgentTask.tsx`
- `/Users/stello/claude-code-src/src/query.ts`

claw-code narrow reference:

- `/Users/stello/claw-code/ROADMAP.md`

Reference pattern used:

- mark dead/stale/ended runtime state explicitly
- do not keep reconnecting or resuming past terminal state
- make the visible failure state structured enough for status/monitoring

## Changed Files

- `internal/agent/session.go`
- `internal/agent/session_test.go`
- `internal/conversation/manager.go`
- `internal/conversation/manager_test.go`
- `internal/daemon/daemon.go`
- `internal/daemon/daemon_test.go`
- `cmd/elnath/cmd_daemon.go`

## Behavior Added

Session JSONL now supports metadata-only retirement lines:

```json
{"type":"retire","failure_class":"provider_auth","reason":"provider_auth_refresh_failed","next_action":"reauthenticate_provider","at":"..."}
```

Agent/session APIs added:

- `SessionRetirementEvent`
- `SessionRetirementStatus`
- `Session.RecordRetirement(...)`
- `Session.Retired()`
- `LoadSessionRetirementStatus(...)`

Conversation manager behavior:

- `LoadSessionForPrincipal` now rejects retired sessions with the recorded
  reason and next action.
- `LoadLatestSession` skips retired sessions and tries the next valid session.
- `RecordSessionRetirement(...)` records runtime retirement metadata in the
  canonical JSONL transcript.

Daemon behavior:

- Added `SessionRetirer` callback interface.
- Retirable task failures call the session retirement hook after queue failure
  receipt update.
- `cmd/elnath daemon` wires the hook to the conversation manager.

## Verification

Focused tests:

```text
go test ./internal/agent ./internal/conversation ./internal/daemon -run 'TestRecordRetirementAndLoadStatus|TestManagerLoadLatestSession_SkipsRetiredSession|TestManagerLoadSessionForPrincipal_RetiredSessionRejected|TestDaemonRetiresSessionAfterRetirableFailure|TestDaemonProviderAuthFailureRecordsRetirementReceipt|TestDaemonProviderRateLimitDoesNotRetireSession' -count=1
PASS:
ok github.com/stello/elnath/internal/agent 0.600s
ok github.com/stello/elnath/internal/conversation 1.094s
ok github.com/stello/elnath/internal/daemon 4.679s
```

Touched runtime packages:

```text
go test ./internal/agent ./internal/conversation ./internal/daemon -count=1
PASS:
ok github.com/stello/elnath/internal/agent 11.004s
ok github.com/stello/elnath/internal/conversation 1.734s
ok github.com/stello/elnath/internal/daemon 39.241s
```

CLI package:

```text
go test ./cmd/elnath -count=1
PASS: ok github.com/stello/elnath/cmd/elnath 24.815s
```

Internal packages:

```text
go test ./internal/... -count=1
PASS: all internal packages passed
```

Whitespace:

```text
git diff --check
PASS
```

## Benchmark / Corpus Boundary

- Full v8 benchmark: not run
- Baseline: not run
- Codex comparison: not run
- Claude Code comparison: not run
- Benchmark corpus mutation: no
- Baseline mutation: no
- Benchmark superiority claim: no

## Claim Boundary

Allowed:

- Retirable daemon failures now persist a session retirement marker when the
  daemon has a session ID and the runtime wires a `SessionRetirer`.
- Conversation resume paths now reject or skip retired sessions.

Not allowed:

- Elnath product/runtime is complete.
- All stale session cases are solved.
- All provider errors are structurally classified by adapters.
- Benchmark readiness or benchmark superiority is proven.

## Remaining Risks

- Explicit manual override for retired sessions is not implemented yet. Current
  behavior favors safety by rejecting retired `LoadSessionForPrincipal` paths.
- Provider adapters still mostly surface unstructured error strings; structured
  provider error codes remain the next hardening step.
- External-process stderr-tail redaction remains a later subprocess transport
  concern.

## Next Milestone Recommendation

Proceed to structured provider/runtime error codes:

1. inspect provider adapters and Hermes provider error classifier references;
2. add Elnath-native provider error classes where adapters can emit them;
3. route those classes into daemon failure metadata without relying only on
   substring matching;
4. keep tests focused on auth/rate-limit/transport/context-window separation.

