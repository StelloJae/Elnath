# ccunpacked reference-parity closeout boundary

Date: 2026-05-13
Branch: codex/ccunpacked-goal-audit
Head at write time: f20acbedbffe63d7c68d9141334bab6b7ee331c0
Lane: ccunpacked reference-parity implementation
Verdict: implementation lane complete after this branch lands
Progress estimate: 98% local, 100% after branch merge

## Why this boundary exists

The goal was to stop repeated benchmark-loop work and build the missing
Claude-Code-like control surface from reference research:

- ToolSearch / deferred tool loading
- skill compatibility and execution
- command and slash-command discovery
- task / cron / plan / worktree callable tools
- bounded self-correction
- provider / model / reasoning-effort configurability
- timeout / execution policy clarity

Those surfaces now exist in Elnath with focused tests, closure artifacts, and an
inspectable `elnath explain control-surfaces --json` view.

The remaining gaps are real, but they are not blockers for this goal.

## Explicit exclusions for this goal

These are intentionally not required before closing the ccunpacked
reference-parity lane:

| Area | Decision | Reason |
|---|---|---|
| UI-level answer collection | excluded from this lane | Repo now has `ask_user_question`, `user_question_list`, strict `user_question_answer`, and CLI answer submission. A Codex-App-like modal/UI bridge is platform integration work, not core Elnath runtime parity. |
| Blocking wait state | excluded from this lane | Current design remains queue/resume oriented. Blocking a model turn or daemon worker needs a separate pause/resume policy and UX contract. |
| Broad silent self-healing | explicitly forbidden | Elnath keeps closed-enum, bounded, receipt-backed correction. That matches the project scope fence and avoids a false promise that every failed coding attempt self-fixes. |
| Streaming line-watch process monitor | deferred | `process_start`, `process_monitor`, and `process_stop` exist. Streaming subscription/watch behavior is useful polish, not a blocker for model-callable process observation. |
| Full LSP lifecycle | deferred | `code_symbols` provides a small Go-native code-intelligence hook. Definition/reference/hover/language-server lifecycle needs a separate design. |
| NotebookEdit | excluded | No notebook-heavy Elnath workflow currently requires it. |
| PowerShell | excluded | Elnath is currently Unix/macOS oriented. Windows parity needs a separate platform goal. |
| Full runtime registry introspection | deferred | The control-surface view is now manifest-backed and checked against ToolSearch routing metadata. Full runtime registry introspection is polish, not required for this lane. |

## Completion criteria audit

| Criterion | Evidence | Verdict |
|---|---|---|
| Reference map refreshed | `.omc/research/ccunpacked-parity-refresh-2026-05-12.md` | pass |
| Claude Code source structure considered | Refresh artifact records Claude Code tool directory and utility paths; implementation reuses flow-level patterns only. | pass |
| ToolSearch / deferred loading | `tool_search`, routing metadata, deferred schema behavior, allowlists, receipts, tests. | pass |
| Skill compatibility and execution | SKILL.md-compatible discovery, skill catalog, model-callable `skill`, trust/provenance metadata, receipts. | pass |
| Command / slash-command discovery | `command_catalog`, `runtime_command`, slash registry, command receipts. | pass |
| Task callable tools | `task_create/list/get/stop/output/monitor/update` in `explain control-surfaces`. | pass |
| Cron/schedule callable tools | `schedule_create/list/delete` in `explain control-surfaces`. | pass |
| Plan callable tools | `enter_plan_mode`, `exit_plan_mode` in `explain control-surfaces`. | pass |
| Worktree callable tools | `enter_worktree`, `worktree_list`, `worktree_run`, `worktree_prune`, `exit_worktree` in `explain control-surfaces`. | pass |
| Provider/model control plane | OpenAI Responses-compatible config and provider status/candidates/check/use surfaces shipped. | pass |
| Reasoning effort control | manual/auto effort, `/effort`, skill effort override, provider-aware routing. | pass |
| Bounded self-correction | closed-enum retry, explicit verification retry, receipts, no-op mutation guard. | pass |
| Timeout/execution policy clarity | `elnath explain timeouts --json`. | pass |
| Avoid benchmark loop | No v8/full benchmark/baseline/comparison run in this closeout branch. | pass |
| Legal boundary | Flow-level adaptation only; no proprietary source copied verbatim. | pass |

## Verification snapshot

Commands run for the local closeout branch:

- `go run ./cmd/elnath explain control-surfaces --json`
  - PASS
  - task, schedule, plan, worktree, process, skill, command are implemented
  - user_input is partial by explicit platform/UI boundary
- `go run ./cmd/elnath explain timeouts --json`
  - PASS
  - provider request timeout: 120s
  - daemon inactivity timeout: 600s
  - daemon wall-clock timeout: 1800s
  - self-healing correction retry: bounded, observe-only, max 1 configured
- `go test ./cmd/elnath -run 'TestExplainControlSurfacesJSON|TestExplainControlSurfacesText|TestControlSurfaceManifestMatchesToolSearchRouting' -count=1`
  - PASS
- `go test ./internal/tools -run 'TestToolSearchReportsRoutingMetadata|TestToolSearchFiltersByCategoryAndSurface|TestToolSearchReportsDeclaredDeferReason' -count=1`
  - PASS
- `go test ./cmd/elnath ./internal/tools -count=1`
  - PASS
- `go vet ./...`
  - PASS
- `git diff --check`
  - PASS

## Claim boundary

Allowed after merge:

- Elnath has a reference-backed, tested, documented ccunpacked
  reference-parity control-surface upgrade.
- Elnath now has the core model-callable surfaces needed for ToolSearch,
  skills, commands, task, schedule, plan, worktree, provider/model/effort,
  timeout policy, and bounded self-correction.
- Elnath is better positioned to resume benchmark readiness without spending
  benchmark runs to discover missing control-loop surfaces.

Not allowed:

- Elnath has full Claude Code parity.
- Elnath is broadly better than Claude Code or Codex.
- Elnath silently self-fixes every coding failure.
- UI-level answer collection exists.
- Full LSP / NotebookEdit / PowerShell parity exists.
- v8 benchmark passed because of this lane.

## Next after merge

Resume benchmark readiness only from the stronger runtime foundation.

Recommended first follow-up:

1. run a small current-only smoke that exercises control-loop behavior;
2. inspect receipts before running larger v8 lanes;
3. avoid baseline/comparison until current-only evidence is clean.

