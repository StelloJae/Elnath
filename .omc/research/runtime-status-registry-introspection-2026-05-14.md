# Runtime Status Registry Introspection - 2026-05-14

## Summary

Branch: `codex/post-pr223-registry-introspection`
PR: none
Commit: none yet

This milestone makes `/status` expose actual runtime registry health instead of only provider,
effort, permission, and tool-exposure settings. The goal is to reduce stale control-surface drift:
operators and model-callable `runtime_command` can now see whether the runtime has registered the
tools promised by the control-surface manifest.

## References Inspected

- `/Users/stello/claude-code-src/src/Tool.ts`
- `/Users/stello/claude-code-src/src/tools/ToolSearchTool/ToolSearchTool.ts`
- `/tmp/elnath-registry-introspection.dxxVoH/cmd/elnath/runtime_status.go`
- `/tmp/elnath-registry-introspection.dxxVoH/cmd/elnath/runtime.go`
- `/tmp/elnath-registry-introspection.dxxVoH/cmd/elnath/cmd_explain.go`
- `/tmp/elnath-registry-introspection.dxxVoH/internal/tools/registry.go`
- `/tmp/elnath-registry-introspection.dxxVoH/internal/tools/tool_search.go`

No proprietary code, prompts, or error strings were copied. The design is Elnath-native:
introspect the Go runtime registry and compare it against the existing control-surface manifest.

## Changed Files

- `cmd/elnath/runtime_status.go`
- `cmd/elnath/runtime_test.go`

## Behavior Added

- `/status --json` now includes:
  - `tool_count`
  - `deferred_tool_count`
  - `control_surface.tool_count`
  - `control_surface.missing`
- `/status` text now includes registered/deferred tool counts and a compact control-surface status.
- Control-surface coverage is computed from the actual runtime `tools.Registry`, not only from
  static manifest metadata.
- `elnath explain control-surfaces` no longer says full runtime registry introspection is entirely
  future work; it now points at `/status` coverage and keeps deeper diagnostics as the remaining gap.

## Verification

TDD probe before implementation:

```text
go test ./cmd/elnath -run 'TestExecutionRuntimeRunTaskStatusSlashCommand|TestExecutionRuntimeRunTaskStatusSlashCommandJSON' -count=1
FAIL as expected: /status had no tool registry or control-surface coverage fields
```

Focused verification after implementation:

```text
gofmt -w cmd/elnath/runtime_status.go cmd/elnath/runtime_test.go
go test ./cmd/elnath -run 'TestExecutionRuntimeRunTaskStatusSlashCommand|TestExecutionRuntimeRunTaskStatusSlashCommandJSON|TestExecutionRuntimeRegistersControlSurfaceManifestTools' -count=1
PASS
```

Stale-gap focused verification:

```text
go test ./cmd/elnath -run TestExplainControlSurfacesJSON -count=1
FAIL as expected: remaining gap still said full runtime registry introspection was future polish

gofmt -w cmd/elnath/cmd_explain.go cmd/elnath/cmd_explain_test.go
go test ./cmd/elnath -run 'TestExplainControlSurfacesJSON|TestExplainControlSurfacesText|TestExecutionRuntimeRunTaskStatusSlashCommand|TestExecutionRuntimeRunTaskStatusSlashCommandJSON' -count=1
PASS
```

Package verification:

```text
go test ./cmd/elnath -count=1
PASS (latest: ok github.com/stello/elnath/cmd/elnath 22.005s)

go vet ./cmd/elnath
PASS

git diff --check
PASS
```

Local batch verification with the active-form guard slice included:

```text
go test ./internal/tools ./cmd/elnath -count=1
PASS (ok github.com/stello/elnath/internal/tools 39.741s; ok github.com/stello/elnath/cmd/elnath 23.073s)

go vet ./internal/tools ./cmd/elnath
PASS

git diff --check origin/main..HEAD
PASS
```

## Benchmark / Baseline Boundary

- Full v8 benchmark: not run
- Baseline: not run
- Codex comparison: not run
- Claude comparison: not run
- Benchmark corpus changed: no
- Baseline artifact changed: no

## Claim Boundary

Allowed:

- Runtime `/status` now reports registry counts and manifest coverage.
- Focused `cmd/elnath` runtime status tests passed.

Forbidden:

- Elnath completion is proven.
- Benchmark readiness is proven.
- Elnath is better than Claude Code or Codex.

## Remaining Risk

This improves live runtime introspection, but it does not make every control surface complete.
UI-level answer collection and richer async streaming monitor behavior remain separate product gaps.

## Next Recommendation

Commit this as the second local slice in the post-PR223 control-surface batch.
