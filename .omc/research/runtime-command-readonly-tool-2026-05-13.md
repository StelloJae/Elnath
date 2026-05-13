# Read-only runtime command tool

Date: 2026-05-13
Branch: `codex/runtime-command-tool`

## Claim

Elnath now has a bounded model-callable `runtime_command` tool for read-only local runtime slash commands.

`command_catalog` remains metadata-only. The new tool is the explicit execution boundary for safe runtime-control queries.

## Scope

Added:

- `runtime_command` model-callable tool.
- Deferred initial schema exposure for `runtime_command`.
- Read-only execution support for:
  - `/version`
  - `/status`
  - `/commands`
  - `/help`
  - `/skills`
  - `/provider` status/candidates/check/help forms
  - `/model` current/status/help forms
  - `/effort` current/status/help forms
  - `/plan` status/help forms
- Rejection for mutating arguments such as `/effort high`, `/model <model>`, `/provider use <provider>`, and `/plan exit`.
- `command_catalog` marks safe runtime slash commands as model-callable and points them to `runtime_command`.
- `runtime_command` receipts are preserved through completion, learning, and agentic gate receipt types.
- permission plan-mode read-only allowlist includes `runtime_command`.

Not changed:

- `command_catalog` still does not execute commands.
- full CLI command execution is not model-callable.
- mutating runtime slash commands are not model-callable through this tool.
- no benchmark corpus, baseline, or v8 evidence changes.

## Evidence

TDD red before implementation:

- `go test ./cmd/elnath -run 'TestExecutionRuntimeRegistersRuntimeCommandTool|TestRuntimeCommandTool|TestCommandCatalogTool(RecommendsRuntimeControlByQuery|IncludesDiscoveryReceipt|ExposesExecutionPolicyMetadata)' -count=1`
- Result: FAIL as expected; `runtime_command` was missing and command catalog had no runtime-command follow-up.

Focused verification after implementation:

- `go test ./cmd/elnath -run 'TestExecutionRuntimeRegistersRuntimeCommandTool|TestRuntimeCommandTool|TestCommandCatalogTool(RecommendsRuntimeControlByQuery|IncludesDiscoveryReceipt|ExposesExecutionPolicyMetadata)|TestCompletionContractSummaryRecordsRuntimeCommandReceipt' -count=1`
- Result: PASS

- `go test ./internal/agent -run 'TestPermissionModes|TestPermissionWithActualToolNames' -count=1`
- Result: PASS

Broader verification:

- `go test ./cmd/elnath ./internal/agent ./internal/agentic/completion ./internal/learning -count=1`
- Result: PASS

- `go vet ./...`
- Result: PASS

- `git diff --check`
- Result: PASS

## Claim boundary

Allowed:

- Elnath can now execute a bounded subset of read-only runtime slash commands through a model-callable tool.
- `command_catalog` can guide runtime-control command discovery to `runtime_command`.

Not allowed:

- no claim that all slash commands are model-callable.
- no claim that mutating runtime commands are executable by the model.
- no claim that arbitrary CLI commands are executable through this tool.
- no v8 benchmark claim.
