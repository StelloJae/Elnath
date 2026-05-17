# Skill Proposals CLI Review UX

Date: 2026-05-18 KST

Branch:

- `codex/skill-proposals-cli`

Goal lane:

- Codex-Claude-Hermes convergence
- Product/runtime first
- Benchmark second

## Summary

PR #259 added skill improvement proposals. PR #260 added a bounded apply path.
This milestone adds operator CLI UX for reviewing and applying those proposals.

## References Inspected

Elnath:

- `cmd/elnath/cmd_skill.go`
- `cmd/elnath/cmd_skill_test.go`
- `internal/skill/tracker.go`
- `internal/skill/tracker_test.go`
- `.omc/research/skill-usage-outcome-receipts-2026-05-18.md`
- `.omc/research/skill-improvement-proposal-apply-2026-05-18.md`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`

Hermes:

- `/Users/stello/.hermes/hermes-agent/tools/skill_usage.py`

Claude Code:

- `/Users/stello/claude-code-src/src/utils/hooks/skillImprovement.ts`

## Changed Behavior

Added:

- `elnath skill proposals list`
- `elnath skill proposals list --json`
- `elnath skill proposals show <proposal-file>`
- `elnath skill proposals show <proposal-file> --json`
- `elnath skill proposals apply <proposal-file>`
- `elnath skill proposals apply <proposal-file> --yes`

Apply behavior:

- without `--yes`, asks for confirmation;
- `n` / default cancels without changing the skill;
- `--yes` applies through `Creator.ApplyImprovementProposal`;
- proposal reading still respects tracker proposal-dir confinement.

## Changed Files

- `cmd/elnath/cmd_skill.go`
- `cmd/elnath/cmd_skill_test.go`
- `internal/skill/tracker.go`
- `internal/skill/tracker_test.go`
- `.omc/research/skill-proposals-cli-review-ux-2026-05-18.md`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`

## Verification

Focused verification:

- `go test ./internal/skill ./cmd/elnath -run 'TestTrackerListImprovementProposals|TestCmdSkillProposals' -count=1`
- Result: PASS

Broader proportional verification:

- `go test ./cmd/elnath ./internal/skill ./internal/tools -count=1`
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

- Elnath operators can now list, inspect, and apply skill improvement proposals
  through CLI review UX.
- Proposal apply remains bounded and confirmation-gated.

Not claimed:

- automatic skill improvement apply;
- LLM-native whole-skill rewrite parity;
- full Hermes curator parity;
- benchmark success or public superiority.

## Remaining Risk

- Apply remains mechanical append, not natural rewrite.
- No automatic lifecycle curator yet.
- No Telegram/UI proposal review surface yet.

## Next Recommendation

Next product/runtime candidate:

- add curator lifecycle explain/docs before automatic skill archival/promotion,
  or
- add Telegram/operator proposal review commands if cross-surface skill
  maintenance becomes higher priority.
