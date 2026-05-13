# Command catalog follow-up receipts

Date: 2026-05-13
Branch: `codex/process-followup-receipts`

## Claim

`command_catalog` now emits `followup_tool: skill` when returned command metadata includes a model-callable skill-backed slash command.

This improves the reference-parity control surface without executing commands from the catalog tool.

## Scope

Changed:

- `command_catalog show` adds `followup_tool: skill` for model-callable skill-backed commands.
- `command_catalog recommend` adds `followup_tool: skill` when at least one recommendation is model-callable.
- command catalog follow-up metadata is preserved in completion summaries, learning receipts, and agentic completion gate receipts.

Not changed:

- `command_catalog` remains metadata-only.
- CLI commands are not made model-callable.
- runtime slash commands are not executed through `command_catalog`.
- no benchmark corpus, baseline, or v8 evidence changes.

## Evidence

TDD red before implementation:

- `go test ./cmd/elnath -run 'TestCommandCatalogTool(AddsSkillFollowup|IncludesDiscoveryReceipt)' -count=1`
- Result: FAIL as expected; model-callable skill recommendations and show receipts lacked `followup_tool`.

Focused verification after implementation:

- `go test ./cmd/elnath -run 'TestCommandCatalogTool(AddsSkillFollowup|IncludesDiscoveryReceipt)|TestCompletionContractSummaryRecordsCommandCatalogReceipt|Test.*CommandCatalog.*Learning|Test.*CommandCatalog.*Agentic' -count=1`
- Result: PASS

Broader verification:

- `go test ./cmd/elnath ./internal/agentic/completion ./internal/learning -count=1`
- Result: PASS

- `go vet ./...`
- Result: PASS

- `git diff --check`
- Result: PASS

## Claim boundary

Allowed:

- command catalog receipts now point model-callable skill-backed command discovery toward the `skill` tool.
- follow-up metadata is receipt-backed and propagated into learning/agentic summaries.

Not allowed:

- no claim that all Claude Code commands exist.
- no claim that `command_catalog` executes commands.
- no claim that CLI command execution is model-callable.
- no v8 benchmark claim.
