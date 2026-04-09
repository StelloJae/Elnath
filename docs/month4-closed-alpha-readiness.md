# Month 4 Closed Alpha Readiness

_Date:_ 2026-04-09  
_Audit basis:_ `.omx/plans/prd-elnath-month4-closed-alpha.md`, `.omx/plans/test-spec-elnath-month4-closed-alpha.md`, and the current repository state in this branch.

## Purpose

This document translates the Month 4 closed-alpha plan into a code-grounded readiness view for the current Elnath implementation. It is intentionally conservative: it lists what already exists, what is still missing, and what operators should rehearse before inviting 2–10 power users.

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

## What is still missing for Month 4 closed alpha

The following gaps are visible in the current code and should be treated as pre-alpha blockers until they are closed.

### 1. No Telegram companion shell exists yet

A repo-wide search shows no Telegram adapter, delivery bridge, approval surface, or resume trigger implementation. The current continuity substrate is CLI + daemon only.

**Implication:** the Month 4 Telegram scope must stay thin and build on the existing queue/session/completion substrate rather than inventing a separate path.

### 2. Progress is durable, but delivery is still local-only

Today the progress envelope is stored and rendered by `elnath daemon status`, but there is no separate completion notification sink, push delivery abstraction, or external subscriber path.

**Implication:** completion data is available, but notification delivery is not yet productized.

### 3. Resume exists at the session level, but resume-safe task snapshots are still narrow

The current implementation supports resuming sessions from conversation history, but there is no explicit resume-safe snapshot contract for long-running background tasks beyond:

- queue task state
- bound `session_id`
- persisted conversation/session history
- task completion summary

**Implication:** Month 4 should treat "resume without re-priming" as only partially implemented until repeated rehearsals prove it holds for interrupted long-running work.

### 4. Alpha telemetry is incomplete

The code currently exposes timeout classification metrics inside the queue layer, but there is no visible implementation for:

- completion handoff success counters
- resume success counters
- repeat-use / retention summaries
- approval friction metrics
- alpha-session telemetry rollups

**Implication:** the PRD's telemetry gate is not yet satisfied even though the false-timeout ingredients exist.

### 5. Operator-facing alpha docs now exist, but they do not open the gate on their own

The repository now includes the explicit Month 4 operator docs the plan calls for:

- `wiki/closed-alpha-setup.md`
- `wiki/closed-alpha-runbook.md`
- `wiki/closed-alpha-known-limits.md`
- README pointers to the same rehearsal bundle and telemetry reporter

**Implication:** rehearsal prep is now documented, but documentation is only supporting evidence. The alpha gate must still stay fail-closed until the missing Telegram shell, richer telemetry, and runtime rehearsals are real.

## Readiness judgment by workstream

| Workstream | Current state | Evidence | Readiness |
| --- | --- | --- | --- |
| Confirmatory Month 3 checkpoint | Frozen in repo artifacts, but canary health is still only partial | `benchmarks/results/month4-closed-alpha-readiness-20260409/confirmatory-month3-checkpoint.md` | Entry checkpoint captured; do not overclaim beyond the frozen memo |
| Shared continuity runtime substrate | Partial but real | `cmd/elnath/runtime.go`, `internal/daemon/queue.go`, `internal/daemon/progress.go` | Best current foundation |
| Thin Telegram shell | Not started in this repo state | No Telegram code found under `cmd/` or `internal/` | Blocked on runtime-first work |
| Onboarding/docs | Operator docs checked in | `wiki/closed-alpha-setup.md`, `wiki/closed-alpha-runbook.md`, `wiki/closed-alpha-known-limits.md` | Documentation ready; rehearsal evidence still needed |
| Telemetry/verification | Partial, daemon-centric | timeout metrics in `internal/daemon/queue.go`; `scripts/alpha_telemetry_report.sh` | Needs explicit alpha signals beyond queue timeouts |

## Month 4 stop/go interpretation for the current branch

### Stay in pre-alpha hardening unless all are true

1. CLI/daemon background completion behaves consistently in repeated rehearsals.
2. Session resume works for real long-running work without full re-priming often enough to satisfy the PRD threshold.
3. Telegram stays thin and is added only after the CLI/daemon runtime path is stable.
4. Alpha telemetry includes more than queue timeout recovery.
5. Operator docs cover setup, first task, failure handling, and known limits.

### What looks strongest today

- Shared CLI/daemon runtime direction is correct.
- Durable completion and progress envelopes already exist.
- The Month 3 checkpoint is now frozen in repo artifacts.
- Operator setup/runbook/known-limits docs are checked in and linked from the README.
- Queue timeout accounting is more mature than the public docs currently imply.

### What looks riskiest today

- Cross-surface continuity is not yet exercised because the second surface does not exist.
- Resume trust is not yet proven by rehearsals/documented evidence.
- Telemetry is too narrow for alpha retention claims.
- Gate automation must not treat docs-only mentions as product implementation evidence.

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

- No Telegram operator shell exists yet.
- No approval workflow is implemented across CLI/daemon/Telegram surfaces.
- Completion exists as stored state, not yet as a generalized notification product surface.
- Resume trust is supported structurally but not yet proven at the product level.
- Telemetry is not yet sufficient to make repeat-use or retention claims.

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

The current codebase is **not yet closed-alpha ready**, but it **does already contain the right substrate direction**: shared CLI/daemon execution, durable completion state, resumable sessions, structured progress events, checked-in operator docs, and initial timeout telemetry. Month 4 should protect that advantage by hardening continuity, capturing rehearsal evidence, expanding alpha telemetry, and only then layering on a thin Telegram companion surface.
