# Product Runtime Watchdog / Session Retirement Milestone

Date: 2026-05-15
Branch: `codex/product-runtime-watchdog`
Base HEAD: `0a4e28d73faeabaa4d8380dc184f00a60020b2b5`

## Purpose

This milestone implements the first product/runtime correction from
`elnath-product-runtime-100-control-2026-05-15.md`: make daemon task failures
produce structured, receipt-backed retirement and next-action signals instead of
leaving callers to infer whether a stale session/runtime should continue.

This is product/runtime work. It is not benchmark-readiness proof and does not
claim benchmark success.

## References Inspected

Hermes narrow reference files:

- `/Users/stello/.hermes/hermes-agent/agent/transports/codex_app_server_session.py`
- `/Users/stello/.hermes/hermes-agent/tests/agent/transports/test_codex_app_server_session.py`

Relevant Hermes patterns:

- `TurnResult.should_retire`
- post-tool quiet watchdog
- OAuth refresh failure classification
- dead subprocess/session retirement
- generic non-auth failures do not automatically retire a session
- stderr tail / terminal marker tests around failed sessions

Claude Code narrow reference files:

- `/Users/stello/claude-code-src/src/remote/SessionsWebSocket.ts`
- `/Users/stello/claude-code-src/src/tasks/LocalAgentTask/LocalAgentTask.tsx`
- `/Users/stello/claude-code-src/src/query.ts`

Relevant Claude Code patterns:

- bounded reconnect / close-code handling
- killed local agent task state
- abort-first handling
- draining tool results for aborted streaming work

claw-code narrow reference file:

- `/Users/stello/claw-code/ROADMAP.md`

Relevant claw-code patterns:

- event-native failure taxonomy
- session state classes such as interrupted/degraded/blocked
- safe failure classes such as `provider_auth`, `provider_rate_limit`,
  `provider_transport`, `runtime_io`

## Changed Files

- `internal/daemon/daemon.go`
- `internal/daemon/queue.go`
- `internal/daemon/daemon_test.go`

## Behavior Added

Daemon task failures now classify terminal failures into receipt-safe failure
families:

- `task_timeout_idle`
- `task_timeout_active`
- `task_canceled`
- `worker_panic`
- `provider_auth`
- `provider_rate_limit`
- `provider_timeout`
- `runtime_io`
- `tool_runtime`

Failed task completion receipts can now include:

- `failure_class`
- `should_retire_session`
- `session_retirement_reason`
- `next_action`

Retirement is explicitly recorded for stale or unsafe continuation cases:

- idle/post-tool quiet timeout -> `post_tool_quiet_timeout`
- wall-clock timeout -> `wall_clock_timeout`
- worker panic -> `worker_panic`
- provider auth refresh failure -> `provider_auth_refresh_failed`
- provider timeout -> `provider_timeout`
- runtime IO failure -> `runtime_io`

Non-retirement examples are also explicit:

- manual cancel -> `task_canceled`, `operator_cancelled`
- provider rate limit -> `provider_rate_limit`, `retry_later`
- generic tool/runtime failure -> `tool_runtime`, `inspect_failure_before_retry`

The queue schema is not widened for this slice. The metadata is stored in the
existing durable `completion` JSON receipt, then exposed through `TaskCompletion`
and `TaskCompletion.View()`.

## Verification

Focused daemon failure/receipt tests:

```text
go test ./internal/daemon -run 'TestDaemon(InactivityTimeout|CancelRunningTask|WallClockTimeout|ProviderAuthFailureRecordsRetirementReceipt|ProviderRateLimitDoesNotRetireSession|WorkerPanicRecordsRetirementReceipt)$' -count=1
PASS: ok github.com/stello/elnath/internal/daemon 8.086s
```

Daemon package:

```text
go test ./internal/daemon -count=1
PASS: ok github.com/stello/elnath/internal/daemon 38.058s
```

Internal packages:

```text
go test ./internal/... -count=1
PASS: all internal packages passed
```

CLI package:

```text
go test ./cmd/elnath -count=1
PASS: ok github.com/stello/elnath/cmd/elnath 28.032s
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

- Elnath daemon failures now produce structured completion receipt metadata for
  failure class, session retirement hint, retirement reason, and next action.
- Idle timeout, wall-clock timeout, provider auth failure, provider rate limit,
  manual cancel, and worker panic paths have focused regression coverage.

Not allowed:

- Elnath product/runtime is complete.
- Elnath benchmark readiness is proven.
- Elnath beats Claude Code or Codex.
- Elnath automatically self-repairs all runtime failures.

## Remaining Risks

- This milestone records retirement and next-action metadata; it does not yet
  enforce a hard session-resume block at conversation/session storage level.
- Auth classification is conservative string-based until provider adapters emit
  structured provider error codes.
- Generic runtime stderr-tail redaction remains mostly applicable to external
  subprocess-backed transports, not the current in-process daemon runner.

## Next Milestone Recommendation

Proceed to runtime enforcement slice:

1. Add provider/runtime structured error codes where adapters can emit them.
2. Add session-retirement enforcement for automatic resume paths.
3. Keep manual/user-explicit resume possible only with visible receipt context.
4. Add tests proving retired sessions are not silently reused by automation.

