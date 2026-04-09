# Month 4 Closed Alpha Readiness

_Date:_ 2026-04-09  
_Audit basis:_ `.omx/plans/prd-elnath-month4-closed-alpha.md`, `.omx/plans/test-spec-elnath-month4-closed-alpha.md`, and the current repository state in this branch.

## Purpose

This document translates the Month 4 closed-alpha plan into a code-grounded readiness view for the current Elnath implementation. It is intentionally conservative: it separates what is now proven for a small controlled cohort from what is still thin and should be hardened before any broader expansion.

## Current foundations already present in code

### 1. CLI and daemon already share a basic continuity substrate

The repo already has the beginnings of the shared-runtime path the PRD asks for:

- `cmd/elnath/commands.go` creates or resumes sessions for `elnath run` via explicit session IDs and `--continue`.
- `cmd/elnath/runtime.go` routes user work through one execution runtime for both interactive CLI and daemon-triggered tasks.
- `internal/daemon/daemon.go` runs queued tasks through the same task runner used by the CLI runtime.
- `internal/daemon/queue.go` persists queued work, task/session binding, progress, summaries, results, and a durable completion contract.

This is a strong Month 4 starting point because the repo already centers continuity in the CLI/daemon control plane rather than in a chat-surface-specific adapter.

### 2. The repo already has a shared progress event contract

`internal/daemon/progress.go` defines `elnath.progress.v1` with:

- `workflow`
- `text`
- `usage`

`cmd/elnath/runtime.go` emits those events during orchestration, and `cmd/elnath/commands.go` renders the shared `message` field in `elnath daemon status`.

This matches the PRD direction: keep the event contract generic first, then reuse it for later delivery bridges.

### 3. The daemon queue already stores a UI-safe completion payload

`internal/daemon/queue.go` persists a `TaskCompletion` record containing:

- `task_id`
- `session_id`
- `summary`
- `status`
- timestamps for create/start/complete

That gives Month 4 a concrete completion contract to reuse for CLI summaries and future notification surfaces.

### 4. Timeout recovery evidence exists, but only inside the daemon queue

`internal/daemon/queue.go` and `internal/daemon/queue_test.go` already distinguish:

- `idle` recoveries
- `active_but_killed` recoveries
- aggregate `FalseTimeoutRate`

This is useful Month 4 telemetry groundwork, especially for the PRD's false-timeout gate.

### 5. First-run onboarding exists for CLI users

The repo already has a meaningful onboarding path:

- `cmd/elnath/commands.go` triggers onboarding on first run.
- `internal/onboarding/*` implements the TUI/text onboarding flow.
- `internal/config/onboarding.go` writes config, data/wiki paths, permission mode, and a starter wiki page.

This means Month 4 does not start from zero on onboarding, but it still needs a closed-alpha-specific operator guide and rehearsal checklist.

## What remains thin after the initial proof pass

The following gaps are still visible in the current codebase, but they are no longer all pre-alpha blockers in the same way. The thin Telegram operator path is now real and live-rehearsed; the remaining work is about hardening and operational discipline, not pretending the surface does not exist.

### 1. Thin Telegram operator shell is now proven, but it stays a companion surface

The repository now includes a thin Telegram companion shell:

- `internal/telegram/*` implements the operator-only command surface and completion notifier.
- `cmd/elnath/commands.go` adds `elnath telegram shell`.
- `internal/daemon/task_payload.go` lets queued work resume an existing session for Telegram follow-ups.
- `internal/daemon/approval_store.go` persists approval requests so the operator shell can resolve them.

Live evidence now exists in:

- `benchmarks/results/closed-alpha-launch-confidence-20260409/telegram-outbound-rehearsal.md`
- `benchmarks/results/closed-alpha-launch-confidence-20260409/telegram-inbound-rehearsal.md`
- `benchmarks/results/closed-alpha-launch-confidence-20260409/launch-memo.md`

Hermes-inspired lifecycle hardening in this lane should stay narrow:

- **adopted now:** explicit polling-conflict handling so a second poller fails fast with operator guidance instead of retrying forever
- **deferred on purpose:** Hermes-style pairing and full token-lock orchestration, because Elnath still keeps Telegram single-chat and operator-only rather than a general multi-user adapter

**Implication:** the Month 4 Telegram scope stays thin, reuses the queue/session/completion substrate, and is proven enough for a small controlled operator cohort without claiming Hermes-grade adapter maturity.

### 2. Progress is durable, but delivery is still deliberately thin

Today the progress envelope is stored and rendered by `elnath daemon status`, but there is no separate completion notification sink, push delivery abstraction, or external subscriber path.

**Implication:** completion notifications now exist for the configured Telegram operator chat, but delivery is still a thin operator bridge rather than a generalized multi-sink notification product.

### 3. Resume exists at the session level, but resume-safe task snapshots are still narrow

The current implementation supports resuming sessions from conversation history, but there is no explicit resume-safe snapshot contract for long-running background tasks beyond:

- queue task state
- bound `session_id`
- persisted conversation/session history
- task completion summary

**Implication:** Month 4 should treat "resume without re-priming" as only partially implemented until repeated rehearsals prove it holds for interrupted long-running work.

### 4. Alpha telemetry is incomplete

The code now exposes a broader local telemetry summary via `scripts/alpha_telemetry_report.sh`, including:

- completion/task-state counts
- session-binding coverage
- continuation request / Telegram follow-up counts from structured daemon payloads
- approval decision counts
- timeout recovery classification and false-timeout rate
- recent session activity summary from conversation history

What is still missing:

- completion handoff success counters across real operator rehearsals
- proven resume-success counters rather than continuation-request proxies
- hosted or aggregated retention analytics beyond local SQLite summaries
- richer approval-friction quality signals beyond raw decision counts

**Implication:** the PRD's telemetry gate is closer, but still not fully satisfied without real rehearsal evidence.

### 5. Operator-facing alpha docs now exist, but they must stay truth-aligned

The repository now includes the explicit Month 4 operator docs the plan calls for:

- `wiki/closed-alpha-setup.md`
- `wiki/closed-alpha-runbook.md`
- `wiki/closed-alpha-known-limits.md`
- README pointers to the same rehearsal bundle and telemetry reporter

**Implication:** rehearsal prep is now documented and backed by live rehearsal artifacts, but documentation is still only supporting evidence. The alpha gate should stay fail-closed against future regressions, stale claims, or scope creep.

## Readiness judgment by workstream

| Workstream | Current state | Evidence | Readiness |
| --- | --- | --- | --- |
| Confirmatory Month 3 checkpoint | Frozen in repo artifacts, but canary health is still only partial | `benchmarks/results/month4-closed-alpha-readiness-20260409/confirmatory-month3-checkpoint.md` | Entry checkpoint captured; do not overclaim beyond the frozen memo |
| Shared continuity runtime substrate | Partial but real | `cmd/elnath/runtime.go`, `internal/daemon/queue.go`, `internal/daemon/progress.go` | Best current foundation |
| Thin Telegram shell | Real, thin, and live-rehearsed | `cmd/elnath/commands.go`, `internal/telegram/*`, `internal/daemon/task_payload.go`, `internal/daemon/approval_store.go`, `benchmarks/results/closed-alpha-launch-confidence-20260409/*` | Good for a small operator cohort; keep it thin |
| Onboarding/docs | Real operator docs plus product onboarding | `internal/onboarding/*`, `internal/config/onboarding.go`, `wiki/closed-alpha-setup.md`, `wiki/closed-alpha-runbook.md`, `wiki/closed-alpha-known-limits.md` | Must stay aligned with current truth |
| Telemetry/verification | Partial, daemon-centric | timeout metrics in `internal/daemon/queue.go` | Needs explicit alpha signals |

## Month 4 stop/go interpretation for the current branch

### Allow the current thin operator cohort only while all are true

1. CLI/daemon background completion behaves consistently in repeated rehearsals.
2. Session resume works for real long-running work without full re-priming often enough to satisfy the PRD threshold.
3. Telegram stays thin and operator-only on top of the CLI/daemon runtime path.
4. Alpha telemetry includes more than queue timeout recovery.
5. Operator docs cover setup, first task, failure handling, and known limits.

### What looks strongest today

- Shared CLI/daemon runtime direction is correct.
- Durable completion and progress envelopes already exist.
- The Month 3 checkpoint is now frozen in repo artifacts.
- Operator setup/runbook/known-limits docs are checked in and linked from the README.
- Live outbound and inbound Telegram rehearsals are captured in checked-in artifacts.
- Queue timeout accounting is more mature than the public docs currently imply.

### What looks riskiest today

- Cross-surface continuity now has a thin Telegram operator bridge, but broad confidence still depends on repeated rehearsals rather than a single proof pass.
- Resume trust is not yet proven by rehearsals/documented evidence.
- Telemetry is broader than timeout-only reporting now, but still too local and rehearsal-light for strong alpha retention claims.
- Documentation does not yet protect operators from overclaiming readiness.

## Closed-alpha operator rehearsal checklist

Use this checklist before inviting external alpha users.

### A. First successful task path

1. Run first-time setup from a clean config path.
2. Start the daemon.
3. Submit a non-trivial daemon task.
4. Verify `elnath daemon status` shows:
   - task id
   - session id
   - shared progress message
   - completion summary when done
5. Resume the related session in CLI and confirm follow-up does not require full re-priming.

### B. Long-running continuity rehearsal

1. Start a long-running task from the CLI or daemon.
2. Confirm progress updates are durable, not only terminal-local.
3. Wait for completion.
4. Inspect the completion summary.
5. Resume the session and issue a follow-up request.
6. Record whether the user had to restate major context.

### C. False-timeout rehearsal

1. Exercise stale recovery scenarios intentionally.
2. Confirm idle vs active-but-killed classification still behaves correctly.
3. Record the resulting timeout metrics.
4. Treat any unexplained active-task recovery as a release blocker.

## Alpha-specific known limits (current branch)

- Telegram now exists only as a thin operator shell; it is not a broad chat companion.
- Approval workflow exists only for persisted operator decisions resolved through the shared store and shell.
- Completion now notifies the configured Telegram operator chat, but it is not yet a generalized notification product surface.
- Only one Telegram poller should run per bot token; polling conflicts are treated as operator errors, not as a background self-healing guarantee.
- Resume trust is supported structurally but not yet proven at the product level.
- Telemetry is useful for local rehearsal evidence, but it is not yet sufficient on its own to make strong repeat-use or retention claims.

## Recommended documentation and verification follow-through

Before Month 4 is called closed-alpha-ready, the repo should have checked-in evidence for:

1. the frozen confirmatory Month 3 closeout memo (already present)
2. one CLI-only continuity rehearsal record
3. one onboarding dry-run record
4. one telemetry sample for completion/resume/timeout metrics
5. one actual Telegram-thin-scope operator-shell implementation plus rehearsal artifact, but only after the runtime-first gate is met

## Verification commands for this audit

These commands should remain the default verification set for Month 4 readiness work:

- `go test ./...`
- `make lint`
- `make build`
- focused continuity/runtime rehearsals using `elnath daemon start`, `elnath daemon submit`, `elnath daemon status`, and CLI session resume paths

## Bottom line

The current codebase is **ready for a small, controlled closed-alpha operator cohort on the current thin Telegram scope**, and it **already contains the right substrate direction**: shared CLI/daemon execution, durable completion state, resumable sessions, structured progress events, checked-in operator docs, and live Telegram rehearsal evidence. The next step is not feature expansion; it is to protect that advantage by hardening continuity, planner/session semantics, and telemetry before any broader rollout.
