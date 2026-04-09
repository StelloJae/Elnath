# Confirmatory Month 3 checkpoint and runtime substrate gaps

- Date: 2026-04-09
- Purpose: freeze the Month 3 carry-forward interpretation before Month 4 closed-alpha work continues.

## Month 3 checkpoint

### Bugfix restored
Month 3 bugfix evidence is strong enough to carry forward.

- `benchmarks/results/month3-cycle-006/summary.md` reports current vs baseline deltas of **+1.00 success** and **+1.00 verification** on the bugfix primary slice.
- `benchmarks/results/month3-cycle-006/comparative-analysis.md` concludes the permission/runtime-policy repair restored the real bugfix superiority signal and invalidated the earlier all-zero collapse as a harness distortion.

### Canary status
The carry-forward canary is **restored enough to beat baseline, but still only partial rather than fully healthy**.

- `benchmarks/results/month3-cycle-006/summary.md` shows current vs baseline canary deltas of **+0.50 success** and **+0.50 verification**.
- `benchmarks/results/month3-cycle-006/canary/current-scorecard.json` records 2/4 current successes (`GO-BF-001`, `TS-BF-002`) with two failures still marked `verification_failed` (`TS-BF-001`, `GO-BF-002`) in the full cycle-006 recapture.
- `benchmarks/results/canary-targeted-repair/review.md` upgrades only **GO-BF-002** to repaired status via the fresh rerun artifact under `benchmarks/results/go-bf-002-targeted-rerun-20260409/`, while explicitly leaving **TS-BF-001 unresolved**.

### Remaining risks entering Month 4
1. **Do not overclaim the canary.** The strongest full Month 3 story is still only partial until a canary-only recapture absorbs the fresh `GO-BF-002` rerun and resolves or supersedes `TS-BF-001`.
2. **Runtime-policy disclosure must stay explicit.** The trustworthy post-fix evidence depends on the disclosed policy (`sandbox=workspace-write`, bypass approvals, non-interactive CLI).
3. **Verification-path drift remains a risk.** The TypeScript targeted verification path needed narrowing once already; future recaptures must keep current/baseline verification behavior aligned.

## Runtime substrate gap list

The current repo has enough continuity substrate to queue background tasks, persist session history, and emit structured progress updates, but the Month 4 shared-runtime alpha surface is still incomplete.

### Present now
- Background daemon queue with durable task completion payloads: `internal/daemon/queue.go`, `internal/daemon/daemon.go`
- Shared progress-event envelope for workflow/text/usage updates: `internal/daemon/progress.go`
- CLI session resume via `--session` and `--continue`: `cmd/elnath/commands.go`
- Resume-safe raw-turn persistence improved in commit `187094e` so session snapshots/history keep the initiating user turn before workflow execution.

### Gaps still open
1. **No companion delivery surface exists yet.** No in-tree Telegram adapter, notification bridge, or cross-surface operator shell is present.
2. **Completion handoff stops at daemon status.** The daemon exposes completion payloads over IPC, but the operator path is still a polling `daemon status` table rather than a durable completion-notification flow.
3. **Progress contract is too narrow for Month 4 control-plane needs.** Current structured events cover `workflow`, `text`, and `usage`, but not approval-required, completion-delivered, resume-available, or follow-up-trigger events.
4. **Approval routing is not shared-runtime aware.** Permission prompts remain interactive CLI behavior; there is no daemon-backed approval state persistence or companion-surface approval action path.
5. **Resume is CLI-local, not cross-surface.** Sessions can be resumed from CLI flags, but there is no daemon/notification initiated resume trigger or operator shortcut that binds back into the same task/session lineage.
6. **Alpha telemetry for continuity trust is not wired yet.** The repo has timeout metrics in the queue, but not the Month 4 completion/resume/retention counters and summaries called for in the PRD/test spec.

## Conclusion
Proceed into Month 4 as **bugfix restored / canary partially restored**. The next safe claim is closed-alpha hardening on the shared continuity runtime, not broad companion expansion, until the remaining control-plane gaps above are closed and the canary is frozen with one confirmatory recapture.
