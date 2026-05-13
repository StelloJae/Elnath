# Skill-Compatible Tool Pattern Polish

Date: 2026-05-13
Branch: codex/plan-worktree-receipts
Lane: ccunpacked reference-parity control surface
Milestone estimate after local verification: 67%

## Objective

Improve Claude/Codex-compatible `SKILL.md` tool allowlist parsing without copying proprietary prompts or implementation code.

This slice keeps Elnath's Go-native skill model and only adapts compatible metadata behavior.

## Reference Finding

Claude-style skill metadata often writes tool permissions as names such as:

- `Bash(git diff:*)`
- `BashOutput`
- `KillBash`
- `AskUserQuestion`

Elnath uses different tool names:

- `bash`
- `process_monitor`
- `process_stop`
- `ask_user_question`

Before this slice, scalar `allowed-tools` parsing split on spaces inside parentheses. That produced fake tool names such as `diff:*)` and failed to map several reference tool aliases.

## Change

- Parse comma/whitespace-separated skill tool lists without splitting inside parentheses.
- Map additional reference tool names:
  - `BashOutput` -> `process_monitor`
  - `KillBash` -> `process_stop`
  - `AskUserQuestion` -> `ask_user_question`
- Preserve existing behavior for:
  - list-style YAML `allowed-tools`
  - comma-separated simple scalar `allowed-tools`
  - Claude permission suffix stripping
  - MCP double-underscore tool names

## Evidence

Red check:

- `go test ./internal/skill -run TestLoadClaudeSkillDirParsesPermissionPatternsWithSpaces -count=1` failed with `diff:*)`, `bashoutput`, `killbash`, and `askuserquestion` in `RequiredTools`.

Focused green checks:

- `go test ./internal/skill -run 'TestLoadClaudeSkillDirParsesPermissionPatternsWithSpaces|TestLoadClaudeSkillDirNormalizesClaudeToolNames|TestLoadClaudeSkillDirAcceptsCommaSeparatedAllowedTools' -count=1` PASS

Broader batch checks:

- `go test ./internal/skill ./internal/agent ./internal/worktree ./cmd/elnath -count=1` PASS
- `go vet ./...` PASS from the preceding same-batch verification
- `git diff --check` PASS from the preceding same-batch verification

## Claim Boundary

Allowed:

- Compatible skill tool allowlists now handle parentheses with spaces.
- Additional Claude-style tool names are mapped to existing Elnath tool surfaces where a close equivalent exists.

Not claimed:

- No new tool execution capability.
- No NotebookEdit support.
- No full Claude Code skill parity.
- No benchmark result.
- No Codex/Claude comparison evidence.

## Next Action

Batch with the plan/worktree follow-up receipt slice and open one coherent PR after final local checks.
