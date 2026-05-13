# control-surface manifest-backed status

Date: 2026-05-13
Branch: codex/ccunpacked-goal-audit
Commit: uncommitted local slice
Lane: ccunpacked reference-parity closeout

## Summary

This slice reduces the last "static control-surface status" gap from the active
ccunpacked reference-parity goal.

Before this slice, `elnath explain control-surfaces` carried a hand-written
policy view where each surface also hard-coded `tool_search_discoverable: true`.
After this slice, the explain view is generated from a small control-surface
manifest, and discoverability is checked against the same routing metadata used
by `tool_search`.

This is still intentionally lighter than full runtime registry introspection.
It removes the duplicated routing claim while keeping the closeout patch small.

## Changed files

- `internal/tools/tool_search.go`
  - exports `ToolRoutingMetadataForName`.
- `cmd/elnath/cmd_explain.go`
  - adds `controlSurfaceManifest`.
  - computes `ToolSearchDiscoverable` from routing metadata instead of a
    literal `true`.
  - replaces the old static-status gap with a narrower registry-introspection
    polish gap.
- `cmd/elnath/cmd_explain_test.go`
  - adds a manifest-to-routing regression test.
  - asserts the old static-status gap wording is gone.

## Verification

- `go test ./cmd/elnath -run 'TestExplainControlSurfacesJSON|TestExplainControlSurfacesText|TestControlSurfaceManifestMatchesToolSearchRouting' -count=1`
  - PASS
- `go test ./internal/tools -run 'TestToolSearchReportsRoutingMetadata|TestToolSearchFiltersByCategoryAndSurface|TestToolSearchReportsDeclaredDeferReason' -count=1`
  - PASS
- `go run ./cmd/elnath explain control-surfaces --json`
  - PASS; output shows task, schedule, plan, worktree, process, skill, and
    command implemented; user_input remains partial.
- `go test ./cmd/elnath ./internal/tools -count=1`
  - PASS
- `git diff --check`
  - PASS

## Claim boundary

Allowed:

- `explain control-surfaces` no longer relies on a purely static
  `tool_search_discoverable` claim.
- Control-surface discoverability is now checked against shared
  `tool_search` routing metadata.

Not allowed:

- Claiming full runtime registry introspection.
- Claiming UI-level answer collection exists.
- Claiming broad silent self-healing exists.
- Claiming full Claude Code parity.

## Remaining risk

The remaining gap is product boundary, not routing metadata:

- `user_input` is still partial because blocking wait state and UI-level answer
  collection are not implemented.
- Process monitoring is present, but streaming line-watch behavior is deferred.
- Full LSP / NotebookEdit / PowerShell parity remain excluded unless a concrete
  workspace need appears.
