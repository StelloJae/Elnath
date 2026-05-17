# Skill Invocation Usage Feedback

Date: 2026-05-17 KST

Branch:

- `codex/mutation-verifier`

Goal lane:

- Codex-Claude-Hermes convergence
- Product/runtime first
- Benchmark second

## Summary

The fresh code pass showed that per-turn mutation receipts and mutation
diagnostic footers already exist in current `main`. Reimplementing them would
duplicate shipped work.

The next smaller real gap is in skill feedback:

- Elnath has a JSONL `skill.Tracker`.
- Runtime slash skill execution records usage.
- Model-callable `skill` execution did not record usage.

This means agent/tool-driven skill use could complete without feeding the
skill usage/self-improvement substrate.

## References Inspected

Elnath:

- `internal/skill/tracker.go`
- `internal/skill/invocation_tool.go`
- `internal/skill/invocation_tool_test.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/cmd_skill.go`
- `internal/agent/agent.go`
- `internal/agent/mutation_footer.go`
- `internal/tools/file.go`
- `internal/tools/mutation.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/tools/skill_usage.py`
- `/Users/stello/.hermes/hermes-agent/tools/skills_guard.py`

Claude Code:

- `/Users/stello/claude-code-src/src/skills/loadSkillsDir.ts`
- `/Users/stello/claude-code-src/src/tools/FileEditTool/FileEditTool.ts`
- `/Users/stello/claude-code-src/src/tools/FileWriteTool/FileWriteTool.ts`

Local convergence docs:

- `/Users/stello/elnath/.omc/research/elnath-convergence-gap-map-2026-05-17.md`
- `/Users/stello/elnath/.omc/research/pr248-provider-route-explain-closure-2026-05-17.md`
- `.omc/research/typescript-diagnostic-adapter-design-2026-05-17.md`

## Design

Add `Tracker *Tracker` to `InvocationToolConfig`.

When model-callable `skill` execution reaches the actual skill run:

- record success with `Success: true` after a completed run;
- record failure with `Success: false` when `Registry.Execute` returns an
  execution error;
- bind the record to `tools.SessionIDFrom(ctx)`;
- keep tracker failures non-fatal;
- expose `usage_recorded` in the successful tool JSON output.

Do not record usage for:

- invalid params;
- missing registry;
- trust filter rejection before provider execution;
- unknown skill;
- provider-not-configured before execution.

This keeps usage telemetry tied to real skill execution attempts, not every
invalid call shape.

## Changed Files

- `internal/skill/invocation_tool.go`
- `internal/skill/invocation_tool_test.go`
- `internal/skill/catalog_tool.go`
- `internal/skill/catalog_tool_test.go`
- `internal/skill/tracker.go`
- `cmd/elnath/runtime.go`
- `.omc/research/skill-invocation-usage-feedback-2026-05-17.md`

## Second Slice: Model-Visible Usage Summaries

After model-callable skill execution started recording usage, the next issue
was visibility: the model could record usage but could not inspect it through a
skill-native read-only tool.

Added:

- `Tracker.UsageSummaries()`
- `skill_catalog` action `usage`
- usage entries with invocation, success, failure, last-used, source,
  trust-level, and external metadata
- read-only usage receipt with `tracker_available` and `returned_usage`
- runtime wiring that passes the existing skill tracker into `skill_catalog`

This gives Elnath a small Hermes-style skill feedback loop without adding
automatic pruning or broad curator behavior.

## Verification

TDD expected failure before implementation:

- `go test ./internal/skill -run 'TestInvocationToolRecordsSkillUsageOnSuccess|TestInvocationToolRecordsSkillUsageOnExecutionFailure' -count=1`
- Result: failed because `InvocationToolConfig` had no `Tracker` field.

Focused verification after implementation:

- `go test ./internal/skill -run 'TestInvocationToolRecordsSkillUsageOnSuccess|TestInvocationToolRecordsSkillUsageOnExecutionFailure' -count=1`
- Result: PASS

Broader proportional verification:

- `go test ./internal/skill -count=1`
- Result: PASS

- `go test ./internal/skill -run 'TestCatalogToolReportsUsageStats|TestCatalogToolUsageRequiresTracker' -count=1`
- Result: PASS

- `go test ./cmd/elnath -run 'TestCommandRegistryIncludesSkill|TestCmdSkillCreateDeleteEditAndStats|TestCommandCatalogToolShowsRuntimeControlArgumentHints' -count=1`
- Result: PASS

- `go test ./cmd/elnath -count=1`
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

- model-callable `skill` execution now records usage attempts when a tracker is
  configured;
- successful skill tool output reports whether usage was recorded;
- runtime wiring passes the existing skill tracker into the model-callable skill
  tool.
- `skill_catalog` can expose read-only skill usage summaries when a tracker is
  configured;
- usage summaries include success/failure counts for skill feedback.

Not claimed:

- full Hermes curator parity;
- automatic skill pruning or lifecycle management;
- skill safety scanning beyond existing trust filters;
- benchmark or public superiority evidence.

## Next Recommendation

Continue with the skill feedback lane before opening a PR:

- add explicit skill guard/explainability for external/local-compatible skills,
  or
- document skill curator exclusions before any automatic skill lifecycle work.

Do not resume benchmark-first work.
