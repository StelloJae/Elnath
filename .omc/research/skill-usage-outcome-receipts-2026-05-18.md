# Skill Usage Outcome Receipts

Date: 2026-05-18 KST

Branch:

- `codex/skill-usage-outcomes`

Goal lane:

- Codex-Claude-Hermes convergence
- Product/runtime first
- Benchmark second

## Summary

This milestone continues the skill feedback lane after
`.omc/research/skill-invocation-usage-feedback-2026-05-17.md`.

The earlier slice made skill usage visible. This slice makes skill usage more
operationally useful by recording outcome signals and adding a safe improvement
proposal path.

## References Inspected

Elnath:

- `internal/skill/tracker.go`
- `internal/skill/tracker_test.go`
- `internal/skill/invocation_tool.go`
- `internal/skill/catalog_tool.go`
- `internal/skill/creator.go`
- `internal/tools/skill_tool.go`
- `cmd/elnath/runtime.go`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`
- `.omc/research/skill-invocation-usage-feedback-2026-05-17.md`

Hermes:

- `/Users/stello/.hermes/hermes-agent/tools/skill_usage.py`
- `/Users/stello/.hermes/hermes-agent/tools/skills_guard.py`
- `/Users/stello/.hermes/hermes-agent/tests/tools/test_skill_usage.py`
- `/Users/stello/.hermes/hermes-agent/tests/tools/test_skills_guard.py`

Claude Code:

- `/Users/stello/claude-code-src/src/utils/hooks/skillImprovement.ts`

## Design Decision

Hermes keeps skill telemetry outside `SKILL.md` as sidecar operational state.
Claude Code can detect possible skill improvements from conversation context and
apply them through a side path.

Elnath should not automatically rewrite skills from this milestone. The safer
product step is:

- record skill usage outcome fields in JSONL receipts;
- expose outcome summaries through `skill_catalog`;
- allow `create_skill action=propose_improvement` to write a review artifact;
- leave actual skill edits to a later explicit review/apply step.

## Changed Behavior

Skill usage records now support:

- `required_tools`
- `verification_result` with closed values:
  - `unknown`
  - `not_run`
  - `passed`
  - `failed`
- `user_outcome`
- `promotion_candidate`
- `improvement_proposal_path`

Usage summaries now aggregate:

- required tool union
- verification result counts
- promotion candidate count
- latest user outcome
- latest improvement proposal path

Model-callable and slash skill executions now record:

- required tools from the skill execution receipt or skill definition;
- `verification_result:not_run`, because the skill execution path itself does
  not own an external verifier;
- `user_outcome:completed` or `user_outcome:failed`.

`create_skill` now supports:

- `action=propose_improvement`
- `name`
- `session_id`
- `reason`
- `evidence`
- `suggested_change`

This writes a markdown proposal under the skill tracker data directory. It does
not edit the skill file.

## Changed Files

- `cmd/elnath/runtime.go`
- `internal/skill/catalog_tool.go`
- `internal/skill/catalog_tool_test.go`
- `internal/skill/creator.go`
- `internal/skill/invocation_tool.go`
- `internal/skill/invocation_tool_test.go`
- `internal/skill/tracker.go`
- `internal/skill/tracker_test.go`
- `internal/tools/skill_tool.go`
- `internal/tools/skill_tool_test.go`
- `.omc/research/skill-usage-outcome-receipts-2026-05-18.md`

## Verification

Focused verification:

- `go test ./internal/skill -run 'TestTracker|TestInvocationToolRecordsSkillUsage|TestCatalogToolReportsUsageStats' -count=1`
- Result: PASS

- `go test ./internal/skill ./internal/tools -run 'TestTracker|TestInvocationToolRecordsSkillUsage|TestCatalogToolReportsUsageStats|TestSkillToolProposeImprovement|TestSkillToolScope' -count=1`
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

- Elnath skill usage receipts now include outcome-oriented fields.
- `skill_catalog action=usage` can expose aggregated outcome signals.
- `create_skill action=propose_improvement` writes a safe review artifact
  instead of modifying `SKILL.md`.

Not claimed:

- full Hermes curator parity;
- automatic skill pruning;
- automatic skill rewriting;
- automatic skill promotion;
- full Claude Code skill improvement hook parity;
- benchmark success or public superiority.

## Remaining Risk

- Improvement proposals are written but not yet routed into a review/apply UX.
- Skill verification result is `not_run` for skill execution because there is no
  dedicated verifier ownership model for skills yet.
- No automatic curator lifecycle has been added.

## Next Recommendation

Next structural blocker candidate:

- add a reviewed skill improvement apply path, or
- add curator lifecycle decision docs/explain output before any automatic skill
  archival or promotion behavior.

Do not resume benchmark-first work.
