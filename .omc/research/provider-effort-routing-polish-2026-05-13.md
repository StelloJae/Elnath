# Provider Effort Routing Polish - 2026-05-13

## Summary

Branch: `codex/provider-effort-routing`

This slice tightens Elnath's provider-aware auto effort routing without changing
the provider selection model.

## Reference Context

- Existing Elnath provider/control-plane work already supports
  OpenAI Responses-compatible providers through `openai_responses.api_key`,
  `base_url`, `model`, `reasoning_effort`, selected provider config, and
  `/provider` / `/effort` status surfaces.
- The ccunpacked parity refresh says the remaining provider/effort gap is
  outcome-backed refinement of the simple auto-effort heuristic, not another
  broad provider rewrite.

## Change

- Route short Korean progress/completion/percentage status reports to low
  effort in auto mode.
- Keep auto effort from sending an `empty_task_default` effort to providers
  that declare effort as ignored or unsupported.
- Update `/effort status` policy text so it matches the progress/status
  low-effort routing behavior.
- Add provider effort capability and auto-effort compatibility to
  `/status --json`, so status probes do not need a separate `/provider status`
  call to understand effort support.

## TDD Evidence

RED:

- `go test ./internal/agent -run 'TestAgentReasoningEffortAuto|TestAgentReasoningEffortAutoEmptyTaskSkipsKnownIgnoredProvider' -count=1`
  failed because Korean progress percentage reports routed to `medium`, and
  ignored-effort providers received `medium` for an empty auto task.
- `go test ./cmd/elnath -run TestExecutionRuntimeEffortStatusExplainsAutoRoutingPolicy -count=1`
  failed because the status text did not include progress routing.
- `go test ./cmd/elnath -run TestExecutionRuntimeRunTaskStatusSlashCommandJSON -count=1`
  failed because `/status --json` did not include provider effort capability
  or auto-effort compatibility.

GREEN:

- `go test ./internal/agent -run 'TestAgentReasoningEffortAuto|TestAgentReasoningEffortAutoEmptyTaskSkipsKnownIgnoredProvider' -count=1`
  passed.
- `go test ./cmd/elnath -run TestExecutionRuntimeEffortStatusExplainsAutoRoutingPolicy -count=1`
  passed.
- `go test ./cmd/elnath -run 'TestExecutionRuntimeRunTaskStatusSlashCommandJSON|TestExecutionRuntimeEffortStatusExplainsAutoRoutingPolicy' -count=1`
  passed.
- `go test ./internal/agent -count=1`
  passed.
- `go test ./cmd/elnath -run 'TestExecutionRuntimeRunTaskStatusSlashCommand|TestExecutionRuntimeEffort|TestExecutionRuntimeRunTaskEffort|TestExecutionRuntimeRunTaskProviderSlashCommandJSONUsesSessionEffortOverride' -count=1`
  passed.
- `go test ./cmd/elnath ./internal/agent -count=1`
  passed.
- `go vet ./...`
  passed.
- `git diff --check`
  passed.

## Claim Boundary

Allowed:

- Auto effort now treats short progress/completion/percentage status reports as
  low-effort tasks.
- Auto effort no longer sends a default medium effort for empty tasks when the
  active provider declares effort ignored or unsupported.
- `/status --json` now includes provider effort capability and auto-effort
  compatibility fields.

Not claimed:

- No provider quality comparison.
- No new provider hot-switch capability.
- No change to manual effort override behavior.
- No v8 benchmark, baseline, Codex CLI comparison, or Claude Code comparison.

## Next Recommendation

Open one provider/effort polish PR, then continue to the next larger milestone:
Task/Cron callable surface receipt polish or Plan/Worktree callable surface
polish.
