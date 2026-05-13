# Tool Search Routing Metadata

Date: 2026-05-13
Branch: codex/toolsearch-deferred-catalog
Lane: ccunpacked reference-parity implementation

## Summary

This slice strengthens `tool_search` as a deferred tool catalog by adding
closed-enum routing metadata and filters.

Each match now includes:

- `category`
- `surface`

The tool input also accepts optional:

- `category`
- `surface`

This lets a model search narrowly for surfaces such as daemon tasks, scheduler
tasks, skills, runtime commands, worktrees, long-running processes, MCP tools,
and built-in file/code tools without exposing every deferred schema up front.

## Implemented Routing Metadata

Examples:

- `task_*` -> `category=task`, `surface=daemon`
- `schedule_*` -> `category=schedule`, `surface=scheduler`
- `enter_worktree`, `exit_worktree`, `worktree_*` -> `category=worktree`, `surface=worktree`
- `process_*` -> `category=process`, `surface=process`
- `skill`, `skill_catalog`, `create_skill` -> `category=skill`, `surface=skill`
- `command_catalog`, `runtime_command` -> `category=command`, `surface=runtime`
- `mcp_*` -> `category=mcp`, `surface=mcp`

## Claim Boundary

Allowed:

- `tool_search` now exposes routing metadata for each returned match.
- `tool_search` can filter the searchable candidate set by `category` and
  `surface`.
- This is discovery/catalog behavior only.

Not claimed:

- No tool execution behavior changed.
- No permission behavior changed.
- No new model-callable tools were added.
- No benchmark behavior changed.
- No v8 benchmark claim.

## Verification

TDD:

```text
go test ./internal/tools -run TestToolSearchFiltersByCategoryAndSurface -count=1
RED before implementation:
TotalTools = 3, want filtered candidate count 1
```

Focused:

```text
go test ./internal/tools -run 'TestToolSearchFiltersByCategoryAndSurface|TestToolSearchReportsRoutingMetadata|TestToolSearchReportsStableMetadata|TestToolSearchIncludesDiscoveryReceipt' -count=1
PASS
```

Package:

```text
go test ./internal/tools -count=1
PASS
```

## Remaining Risk

The routing metadata is name-based. That is intentional for now because Elnath
does not yet have a first-class per-tool manifest interface. A future slice can
promote this into explicit tool metadata if more surfaces need custom labels.

## Next Action

Run broader runtime checks, then batch this as a ToolSearch/deferred catalog
milestone PR if verification stays green.
