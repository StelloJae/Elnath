# Skill Conditional Prompt Activation

Date: 2026-05-13
Branch: codex/skill-conditional-prompt
Lane: ccunpacked reference-parity control surface
Milestone estimate after local verification: 70%

## Objective

Make path-scoped compatible skills easier for the model to discover without exposing every conditional skill as always-on prompt context.

## Finding

Elnath already supports:

- `paths` metadata on wiki and compatible `SKILL.md` skills;
- `Registry.ConditionalMatchesForPaths`;
- `skill_catalog match_paths`;
- durable completion evidence when `skill_catalog match_paths` is used.

The remaining gap was activation ergonomics: if the user input already names a matching file path, the static prompt still only said to use `skill_catalog match_paths`. The model had to infer the discovery step even when enough path evidence was already present.

## Change

- `SkillCatalogNode` now extracts conservative file-like path tokens from the current user input.
- If a path-scoped skill matches those paths, the prompt adds a compact `Matched conditional skills` section.
- Unmatched conditional skills remain hidden from the static prompt.
- The existing `skill_catalog match_paths` guidance remains present for files discovered later through tools.

## Evidence

Red check:

- `go test ./internal/prompt -run TestSkillCatalogNodeRenderShowsConditionalSkillsMatchingUserInputPaths -count=1` failed because matched conditional skills were not shown.

Green checks:

- `go test ./internal/prompt -run 'TestSkillCatalogNodeRender(ShowsConditionalSkillsMatchingUserInputPaths|HidesConditionalSkills|ListsSkills)' -count=1` PASS
- `go test ./internal/prompt ./internal/skill ./cmd/elnath -count=1` PASS
- `go vet ./...` PASS
- `git diff --check` PASS

## Claim Boundary

Allowed:

- Elnath now surfaces conditional skills when the current user input explicitly names a matching file-like path.
- Conditional skills remain hidden unless matched or discovered via `skill_catalog match_paths`.

Not claimed:

- No automatic skill execution.
- No background skill activation policy.
- No new skill trust policy.
- No benchmark behavior change.
- No v8 benchmark, baseline, or comparison evidence.

## Next Action

Open one focused PR for this skill-discovery milestone, then continue toward process/background monitoring or remaining self-correction policy gaps.
