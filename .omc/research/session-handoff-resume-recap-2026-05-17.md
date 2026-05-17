# Session Handoff Resume Recap Slice

Date: 2026-05-17 KST

Branch: `codex/session-handoff-recap`

Goal lane:

- Codex-Claude-Hermes convergence
- Product/runtime first
- Benchmark second
- Current slice: explicit task-resume continuity for retired sessions

## Summary

The convergence gap map lists session handoff / resume recap as a P1 continuity
gap. Direct inspection showed Elnath already has:

- task handoff output;
- compact task resume context;
- resume event metadata;
- session retirement metadata;
- automatic latest-session retirement blocking.

The remaining narrow gap is that a retired session is blocked for every resume
path, including explicit operator `elnath task resume <id>` handoff. That
conflicts with the prior product-runtime watchdog artifact, which says manual
or user-explicit resume should remain possible only when visible receipt context
is provided.

## References Inspected

Elnath:

- `cmd/elnath/cmd_task.go`
- `cmd/elnath/cmd_task_handoff.go`
- `cmd/elnath/cmd_task_resume_context.go`
- `cmd/elnath/cmd_run.go`
- `cmd/elnath/cmd_task_test.go`
- `cmd/elnath/runtime_test.go`
- `internal/conversation/manager.go`
- `internal/conversation/manager_test.go`
- `internal/agent/session.go`
- `.omc/research/product-runtime-watchdog-session-retirement-2026-05-15.md`
- `.omc/research/progress-bridge-process-preview-2026-05-17.md`
- `/Users/stello/elnath/.omc/research/elnath-convergence-gap-map-2026-05-17.md`
- `/Users/stello/elnath/.omc/research/elnath-ultimate-goal-codex-claude-hermes-convergence-2026-05-17.md`

Hermes:

- `/Users/stello/.hermes/hermes-agent/gateway/session.py`
- `/Users/stello/.hermes/hermes-agent/gateway/run.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_restart_resume_pending.py`
- `/Users/stello/.hermes/hermes-agent/agent/context_compressor.py`

Claude Code:

- `/Users/stello/claude-code-src/src/remote/RemoteSessionManager.ts`
- `/Users/stello/claude-code-src/src/remote/SessionsWebSocket.ts`
- `/Users/stello/claude-code-src/src/QueryEngine.ts`
- `/Users/stello/claude-code-src/src/services/compact/*`

Reference interpretation:

- Hermes separates recoverable `resume_pending` from hard `suspended`.
- Hermes preserves transcript continuity for explicit/recoverable paths, but
  hard suspension still wins.
- Elnath's equivalent should keep silent/automatic retired-session reuse
  blocked, while allowing explicit task handoff resume with visible retirement
  context.

## Behavior Added

Added narrow session load options:

- default `LoadSessionForPrincipal` still rejects retired sessions;
- `LoadSessionForPrincipalWithOptions(..., AllowRetired: true)` can load a
  retired session after the same principal/ownership checks pass;
- `cmdRun` enables `AllowRetired` only when a task-resume handoff context was
  explicitly requested through `--task-resume-handoff-context` or
  `--continue-task`;
- `--continue` and ordinary `--session` still reject retired sessions.

This preserves the product boundary:

- no silent automatic reuse of retired sessions;
- manual task resume is allowed only when the handoff context can tell the
  model/operator that the session is retired and why.

## Changed Files

- `internal/conversation/manager.go`
- `internal/conversation/manager_test.go`
- `cmd/elnath/cmd_run.go`
- `cmd/elnath/cmd_task_resume_context.go`
- `cmd/elnath/cmd_task_test.go`

## Verification

Focused checks run from
`/Users/stello/elnath/.worktrees/session-handoff-recap`:

- `go test ./internal/conversation -run 'TestManagerLoadSessionForPrincipal_(RetiredSessionRejected|CanonicalDriftAllowed)|TestManagerLoadSessionForPrincipalWithOptions_AllowsRetiredSession' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/conversation 0.584s`
- `go test ./cmd/elnath -run 'Test(TaskResumeHandoffContextRequested|BuildTaskResumeHandoffContextIncludesCompactRecap|ConsumeTaskResumeHandoffContextOnlyOnce|ExecutionRuntimeAddsResumeHandoffContextToSystemPrompt)' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.635s`

Broader checks:

- `go test ./internal/conversation ./cmd/elnath -count=1`
  - PASS: `internal/conversation 1.123s`, `cmd/elnath 17.064s`
- `git diff --check`
  - PASS
- `go vet ./...`
  - PASS
- `go test ./internal/... ./cmd/elnath -count=1`
  - PASS: all internal packages and `cmd/elnath`; notable package timings
    included `internal/daemon 43.686s`, `internal/tools 42.625s`,
    `internal/telegram 17.973s`, `cmd/elnath 20.433s`

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

- Elnath can now distinguish ordinary retired-session resume blocking from an
  explicit task handoff resume path.
- Explicit task resume can load a retired session only when handoff context is
  requested.

Forbidden:

- Hermes-grade `/handoff` parity.
- Full gateway session transfer.
- Public Codex/Claude/Hermes superiority.
- Benchmark readiness proof.

## Remaining Risk

- This is not a full handoff state machine with requested/claimed/running/
  completed/failed states.
- The handoff context already includes retirement reason and next action, but
  native UI presentation remains plain CLI/system-prompt text.
- Daemon session-bound task execution still uses the default retired-session
  block; this slice does not let automation silently resume retired sessions.

## Next Recommendation

Run package-level verification for `internal/conversation` and `cmd/elnath`,
then `git diff --check`. If clean, commit as one continuity/product-runtime
milestone. The next structural blocker should remain product/runtime focused,
likely either richer handoff state or user-input native UX.
