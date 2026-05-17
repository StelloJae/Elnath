# Skill Curator CLI Status / Install Milestone

Date: 2026-05-18 KST

Branch:

- `codex/runtime-progress-status`

Goal lane:

- Codex-Claude-Hermes convergence
- Product/runtime first
- Benchmark second

## Summary

This milestone exposes Elnath's existing skill lifecycle substrate as an
operator-facing CLI surface.

Elnath already had:

- skill usage tracking;
- skill improvement proposals;
- draft promotion/cleanup consolidator;
- daemon `skill-promote` task type;
- static scheduled task creation.

The gap was product visibility: an operator could not easily tell whether the
skill curator was installed or install the recurring curator schedule without
knowing the lower-level scheduler payload.

## References Inspected

Elnath:

- `cmd/elnath/cmd_skill.go`
- `cmd/elnath/cmd_skill_test.go`
- `internal/skill/consolidator.go`
- `internal/skill/tracker.go`
- `internal/skill/promotion.go`
- `internal/scheduler/task_tools.go`
- `internal/scheduler/scheduler.go`
- `internal/daemon/task_payload.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/cmd_daemon.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/README.zh-CN.md`
- `/Users/stello/.hermes/hermes-agent/CONTRIBUTING.md`
- `/Users/stello/.hermes/hermes-agent/AGENTS.md`

## Changed Behavior

Added:

- `elnath skill curator status`
- `elnath skill curator status --json`
- `elnath skill curator install`
- `elnath skill curator install --interval DURATION`
- `elnath skill curator install --run-on-start`
- `elnath skill curator install --json`

Status reports:

- schedule path;
- whether a `skill-promote` scheduled task exists;
- scheduled task metadata when present;
- draft skill count;
- improvement proposal count;
- usage-tracked skill count;
- total usage count;
- scheduler runtime semantics.

Install writes a static scheduled task:

- default name: `skill-curator`
- type: `skill-promote`
- default prompt: `promote queued skill drafts`
- default interval: `24h`
- effective after daemon restart, matching existing scheduler semantics.

## Product Impact

Before:

- Skill lifecycle curator behavior existed below the surface.
- Operator had to know `schedule_create` and `skill-promote` payload details.

After:

- Operator can inspect curator readiness directly.
- Operator can install recurring skill curation through a domain-specific CLI.
- This moves Elnath closer to Hermes-style self-improving skill lifecycle while
  preserving Elnath's review/receipt posture.

## Changed Files

- `cmd/elnath/cmd_skill.go`
- `cmd/elnath/cmd_skill_test.go`
- `.omc/research/skill-curator-cli-status-install-2026-05-18.md`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`

## Verification

Focused verification:

- `go test ./cmd/elnath -run TestCmdSkillCuratorStatusAndInstall -count=1`
- Result: PASS

Additional verification:

- `go test ./cmd/elnath -run 'TestCmdSkill(Curator|Proposals)' -count=1`
- Result: PASS

- `go test ./cmd/elnath ./internal/skill ./internal/scheduler -count=1`
- Result: PASS

- `go vet ./...`
- Result: PASS

- `git diff --check`
- Result: PASS

## Benchmark Boundary

No benchmark lane was run.

- Full v8 benchmark: not run
- Baseline: not run
- Codex comparison: not run
- Claude comparison: not run
- Corpus mutation: none
- Baseline mutation: none

## Claim Boundary

Allowed:

- Elnath now exposes skill curator status/install CLI commands.
- Operators can install a recurring `skill-promote` scheduled task without
  manually composing scheduler payloads.

Not claimed:

- automatic skill rewrite quality;
- automatic proposal approval;
- full Hermes skill curator parity;
- live daemon hot reload of newly installed schedules;
- benchmark success;
- Codex/Claude/Hermes superiority.

## Remaining Risk

- Installed schedules take effect after daemon restart because static scheduler
  hot reload is not implemented.
- Curator still promotes/cleans only existing draft skills through current
  consolidator thresholds.
- Proposal application remains review/approval driven.

## Next Recommendation

Continue product/runtime completion with one of:

- richer skill curator reporting/actions after dogfood;
- terminal-native user input choice UX;
- session handoff/resume recap polish.
