# Local Slash Dispatch Registry Slice - 2026-05-13

## Summary

Branch: `codex/command-slash-receipts`

This slice tightens Elnath's command/slash-command control surface by making
local slash command execution use the same structured spec table that feeds the
runtime command catalog.

## Change

- Added a handler field to `localSlashCommandSpec`.
- Replaced the local slash `switch` dispatcher with spec-table dispatch.
- Added command catalog `execution_available` metadata for CLI, runtime slash,
  internal, and skill-backed command entries.
- Added command catalog receipt aggregate counts for executable and
  model-callable command surfaces.
- Added test coverage that every runtime slash command spec has a non-nil
  handler.

## Claim Boundary

Allowed:

- Runtime local slash commands are now catalog-backed for metadata and
  dispatch.
- Command catalog JSON now exposes whether each entry has an execution path.
- Command catalog receipts now expose aggregate executable and model-callable
  command counts for completion/learning/agentic evidence.
- This reduces ad hoc command sprawl without changing user-visible command
  behavior.

Not claimed:

- No new slash commands.
- No model-callable execution of arbitrary CLI commands.
- No permission behavior change.
- No human-readable command output format change.
- No command execution behavior change.
- No v8 benchmark, baseline, Codex CLI comparison, or Claude Code comparison.

## Verification

Passed:

- `go test ./cmd/elnath -run 'TestRuntimeLocalSlashCommandRegistry|TestExecutionRuntimeRunTaskCommandsSlashCommandListsCatalog|TestExecutionRuntimeRunTaskProviderSlashCommandStatus' -count=1`
- `go test ./cmd/elnath -run 'TestCommandCatalogToolExposesExecutionPolicyMetadata|TestRuntimeLocalSlashCommandRegistry|TestExecutionRuntimeRunTaskCommandsSlashCommandListsCatalog|TestExecutionRuntimeRunTaskProviderSlashCommandStatus' -count=1`
- `go test ./cmd/elnath -run 'TestCommandCatalogToolIncludesDiscoveryReceipt|TestCompletionContractSummaryRecordsCommandCatalogReceipt|TestCompletionGateContextProviderConsumesRuntimeSummary|TestCompletionGateReceiptSummaryIncludesRuntimeContext' -count=1`
- `go test ./internal/learning -run TestOutcomeRecordCompletionObservabilityJSONCompatibility -count=1`
- `go test ./internal/agentic/completion -run TestCompletionGate_ReceiptSummaryIncludesOptionalCompletionContext -count=1`
- `go test ./cmd/elnath ./internal/learning ./internal/agentic/completion -count=1`
- `go vet ./...`
- `git diff --check`

## Next Recommendation

Open one batched PR for the command/slash registry and receipt metadata slice.
