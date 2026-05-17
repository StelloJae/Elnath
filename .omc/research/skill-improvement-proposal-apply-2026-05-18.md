# Skill Improvement Proposal Apply Path

Date: 2026-05-18 KST

Branch:

- `codex/skill-improvement-apply`

Goal lane:

- Codex-Claude-Hermes convergence
- Product/runtime first
- Benchmark second

## Summary

PR #259 added safe skill improvement proposal artifacts. This milestone adds a
bounded apply path for those artifacts.

The apply path does not ask an LLM to rewrite a skill. It only reads an
Elnath-generated proposal, verifies it is under the tracker proposal directory,
verifies the target skill name, and appends an explicit applied-improvement note
to the wiki-native skill prompt.

## References Inspected

Elnath:

- `internal/skill/tracker.go`
- `internal/skill/creator.go`
- `internal/tools/skill_tool.go`
- `cmd/elnath/cmd_skill.go`
- `internal/skill/consolidator.go`
- `.omc/research/skill-usage-outcome-receipts-2026-05-18.md`

Hermes:

- `/Users/stello/.hermes/hermes-agent/tools/skill_usage.py`

Claude Code:

- `/Users/stello/claude-code-src/src/utils/hooks/skillImprovement.ts`

## Design Decision

Claude Code has an LLM-backed skill improvement apply path. Elnath keeps this
safer for now:

- only Elnath proposal artifacts can be read;
- proposal paths must stay under the tracker proposal directory;
- applying requires `approved:true` in the model-callable tool;
- the skill is not rewritten wholesale;
- the suggested change is appended with an applied-proposal marker;
- repeated apply of the same proposal is idempotent.

## Changed Behavior

Added:

- `Tracker.ReadImprovementProposal(path)`
- proposal path confinement to `skill-improvement-proposals`
- markdown proposal parser for Elnath-generated artifacts
- `Creator.ApplyImprovementProposal(path)`
- `create_skill action=apply_improvement`
- `approved:true` guard for applying a proposal
- tool scope metadata for proposal reads and wiki skill writes

## Changed Files

- `internal/skill/tracker.go`
- `internal/skill/tracker_test.go`
- `internal/skill/creator.go`
- `internal/skill/creator_test.go`
- `internal/tools/skill_tool.go`
- `internal/tools/skill_tool_test.go`
- `.omc/research/skill-improvement-proposal-apply-2026-05-18.md`

## Verification

Focused verification:

- `go test ./internal/skill ./internal/tools -run 'TestTracker(Read|Write)Improvement|TestCreatorApplyImprovementProposal|TestSkillTool(ProposeImprovement|ApplyImprovement|Scope)' -count=1`
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

- Elnath can now apply its own skill improvement proposal artifacts to
  wiki-native skills through a bounded, approved tool path.
- The apply path is marker-based and receipt-compatible.

Not claimed:

- LLM-native skill rewriting parity with Claude Code;
- automatic skill improvement apply;
- full Hermes curator parity;
- compatible/plugin-cache skill editing;
- benchmark success or public superiority.

## Remaining Risk

- Apply path is intentionally mechanical. It appends notes instead of naturally
  rewriting the skill structure.
- No CLI-specific review/list UX for proposals yet.
- No automatic curator lifecycle.

## Next Recommendation

Next structural blocker candidate:

- add `elnath skill proposals list/show/apply` CLI UX for human review, or
- add curator lifecycle explain/docs before automatic skill archival/promotion.
