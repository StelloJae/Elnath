# Provider Route Explainability Slice - 2026-05-17

## Branch

- Branch: `codex/provider-route-explain`
- Base: `origin/main` at `076bc93bf5e005f45a9cf857f2d44c38725d9544`
- PR: not opened yet

## Lane

Codex-Claude-Hermes convergence program.

Product/runtime first. No benchmark lane was run.

## Problem

Elnath already had provider status, provider candidates, provider checks, runtime
provider switch boundaries, manual `/effort`, and auto-effort heuristics.

The missing product/runtime surface was route explainability: a user or model
could inspect provider capability, but could not ask for one structured view of:

- active provider/model route;
- why that route is active;
- current effort mode and provider effort compatibility;
- auto-effort policy rows;
- provider-switch boundaries;
- safe next actions;
- claim boundary for the route report itself.

This matters because future provider/cost/quality routing should be explicit,
inspectable, and receipt-friendly before Elnath performs stronger automatic
fallback or provider switching.

## References Inspected

Elnath:

- `cmd/elnath/cmd_provider.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`
- `cmd/elnath/runtime_provider.go`
- `cmd/elnath/runtime_command_execute_tool.go`
- `cmd/elnath/runtime_local_slash.go`
- `cmd/elnath/runtime_effort.go`
- `cmd/elnath/commands.go`
- `.omc/research/elnath-ultimate-goal-codex-claude-hermes-convergence-2026-05-17.md`

Hermes:

- `/Users/stello/.hermes/hermes-agent/cron/scheduler.py`
  - Loads model/reasoning/provider routing per job.
  - Resolves runtime provider.
  - Uses fallback providers on auth failure.
  - Passes provider routing constraints into the agent.
- `/Users/stello/.hermes/hermes-agent/RELEASE_v0.8.0.md`
  - Live model switching and aggregator-aware routing.
  - Provider/model diagnostics and fallback direction.
- `/Users/stello/.hermes/hermes-agent/RELEASE_v0.12.0.md`
  - Provider expansion, `hermes fallback`, model dashboard, remote model catalog,
    reasoning isolation, and provider/fallback quality improvements.

Claude Code:

- `/Users/stello/claude-code-src/src/query.ts`
  - Model fallback updates active model, resets tool execution state, strips
    model-bound protected thinking signatures, and emits user-visible fallback
    notification.
- `/Users/stello/claude-code-src/src/entrypoints/sdk/controlSchemas.ts`
  - Runtime-resolved model/effort settings are exposed distinctly from raw disk
    merge state.
- `/Users/stello/claude-code-src/src/commands/effort/effort.tsx`
  - Effort status and user-facing effort commands.

Codex:

- `/Users/stello/codex/codex-rs/core/src/config/config_tests.rs`
  - Provider/model/reasoning settings are config-level first-class values.
- `/Users/stello/codex/codex-rs/core/src/agent/role.rs`
  - Role application preserves active profile/provider unless a role explicitly
    overrides them.
- `/Users/stello/codex/codex-rs/core/src/tools/handlers/multi_agents_common.rs`
  - Spawned-agent model/reasoning effort overrides are validated against
    supported presets.

## Implemented Behavior

Added `provider route` and `/provider route` as explainability-only surfaces.

CLI:

- `elnath provider route`
- `elnath provider route --json`

Runtime slash command:

- `/provider route`
- `/provider route --json`

Model-callable read-only runtime command:

- `runtime_command` now accepts `/provider route` and `/provider route --json`
  as read-only provider queries.

The JSON view reports:

- `active_provider`
- `active_model`
- `selection_reason`
- `reasoning_effort_mode`
- `configured_effort`
- `provider_effort`
- `provider_effort_note`
- `effort_compatibility`
- `auto_effort_compatible`
- `auto_effort_policy`
- `request_timeout_seconds`
- `runtime_provider_switch_available`
- `provider_switch_boundaries`
- `fallback_policy`
- `configured_providers`
- `next_safe_actions`
- `claim_boundary`

Claim boundary emitted by the route report:

- `route_explain_only`
- `does_not_switch_provider`
- `does_not_make_model_request`
- `auto_effort_policy_is_heuristic`

## Changed Files

- `cmd/elnath/cmd_provider.go`
- `cmd/elnath/runtime_provider.go`
- `cmd/elnath/runtime_command_execute_tool.go`
- `cmd/elnath/runtime_local_slash.go`
- `cmd/elnath/commands.go`
- `cmd/elnath/command_helpers_test.go`
- `cmd/elnath/runtime_test.go`
- `cmd/elnath/runtime_command_tool_test.go`
- `.omc/research/provider-route-explainability-2026-05-17.md`

## Verification

TDD expected failure before implementation:

```text
go test ./cmd/elnath -run 'TestProviderCommandRouteJSONExplainsActiveRoute|TestExecutionRuntimeRunTaskProviderRouteJSONExplainsRuntimeRoute' -count=1
FAIL: undefined: providerRouteView
```

Focused new tests after implementation:

```text
go test ./cmd/elnath -run 'TestProviderCommandRouteJSONExplainsActiveRoute|TestExecutionRuntimeRunTaskProviderRouteJSONExplainsRuntimeRoute' -count=1
PASS
```

Provider/runtime regression subset:

```text
go test ./cmd/elnath -run 'TestProviderCommand|TestProviderSelection|TestExecutionRuntimeRunTaskProviderSlashCommand|TestRuntimeCommand|TestCommandCatalogToolShowsRuntimeControlArgumentHints' -count=1
PASS
```

Runtime command read-only route proof:

```text
go test ./cmd/elnath -run 'TestRuntimeCommandToolExecutesProviderRouteReadOnly|TestRuntimeCommandToolExecutesStatusReadOnly|TestRuntimeCommandToolRejectsMutatingEffortCommand' -count=1
PASS
```

Explain/control-surface visibility proof:

```text
go test ./cmd/elnath -run 'TestExplainControlSurfacesJSON|TestControlSurfaceManifestMatchesToolSearchRouting|TestExplainControlSurfacesText' -count=1
PASS
```

Fallback planning boundary proof:

```text
go test ./cmd/elnath -run 'TestProviderCommandRouteJSONExplainsActiveRoute|TestExecutionRuntimeRunTaskProviderRouteJSONExplainsRuntimeRoute|TestRuntimeCommandToolExecutesProviderRouteReadOnly' -count=1
PASS
```

Formatting whitespace:

```text
git diff --check
PASS
```

Package test:

```text
go test ./cmd/elnath -count=1
PASS
```

Vet:

```text
go vet ./cmd/elnath
PASS
```

Intermediate failure:

```text
go test ./cmd/elnath -count=1
FAIL: TestCommandCatalogToolShowsRuntimeControlArgumentHints
```

Root cause: existing catalog test pinned the old `/provider` argument hint and
did not yet include `route`. Test expectation was updated to match the new
catalog surface.

## Benchmark Boundary

- Full v8 benchmark: not run.
- Baseline: not run.
- Codex comparison: not run.
- Claude Code comparison: not run.
- Benchmark corpus: not mutated.
- Benchmark baseline artifacts: not mutated.
- No benchmark success or superiority claim.

## Product Impact

This slice improves provider/model/effort route introspection.

It does not implement automatic cost optimization, automatic provider fallback,
or live provider switching beyond the boundaries Elnath already had. The route
view now exposes `fallback_policy.mode=planning_only` and
`automatic_provider_switch=false`, so future fallback automation has an explicit
closed boundary before it is implemented.

`elnath explain control-surfaces` now also exposes the provider route boundary
through the command surface notes and product boundary list.

## Remaining Risk

- Auto-effort routing remains heuristic.
- Provider route explains current policy; it does not yet score quality/cost or
  choose among providers dynamically.
- Provider fallback remains planning-only. No automatic provider switch is
  performed.
- Runtime provider switching still has startup-bound constraints when reflection
  or daemon shared runtime is active.

## Next Milestone Recommendation

Next highest-leverage slice:

Provider route receipts and fallback planning boundary.

Possible shape:

- expose the route view in session/runtime receipts when provider/effort/model
  matters;
- add a closed-enum fallback plan field without executing fallback automatically;
- later, implement bounded automatic fallback only for explicitly classified
  provider setup/auth/rate-limit failures.
