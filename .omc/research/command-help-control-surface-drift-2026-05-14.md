# Command help and control-surface drift repair

Date: 2026-05-14
Branch: `codex/post-pr218-next`
Lane: final completion program / command discoverability slice
Status: local implementation

## Problem found

After PR #218, Elnath's runtime/control surfaces had moved forward, but two
operator-facing status surfaces were stale:

- `elnath help` used a static localized `cli.help` string instead of the
  command registry. Real registered commands such as `chaos`, `commands`,
  `debug`, `explain`, `profile`, `provider`, `skill`, `task`, and `telegram`
  were executable but absent from top-level help.
- `elnath explain control-surfaces --json` still described process observation
  as only "bounded process_wait" after `process_wait watch_text` had shipped.

This is a control-loop completion issue because stale help/status makes the
agent and operator rediscover existing capabilities or return to old lanes.

## References inspected

- Elnath:
  - `cmd/elnath/commands.go`
  - `cmd/elnath/commands_help_test.go`
  - `cmd/elnath/cmd_explain.go`
  - `cmd/elnath/cmd_explain_test.go`
- Claude Code:
  - `/Users/stello/claude-code-src/src/commands.ts`
  - `/Users/stello/claude-code-src/src/components/HelpV2/Commands.tsx`
  - `/Users/stello/claude-code-src/src/hooks/useMergedCommands.ts`
- Hermes:
  - `/Users/stello/.hermes/hermes-agent/AGENTS.md`
- claw-code:
  - `/Users/stello/claw-code/src/commands.py`
  - `/Users/stello/claw-code/src/main.py`

The reference pattern is a registry-first command surface: help, command
listing, completion, and downstream dispatch should derive from structured
command metadata instead of separate hand-maintained strings.

## Chosen Elnath-native design

- Keep Elnath's existing `commandSpec` / `commandCatalog` as the source of
  truth.
- Render top-level `elnath help` from `commandCatalog(false)`.
- Preserve hidden internal commands as hidden.
- Keep command-specific help through the existing `elnath <command> --help`
  path.
- Update the control-surface gap text to mention literal `watch_text` marker
  waits while preserving the boundary that async streaming line-watch remains
  deferred.
- Refresh the completion control documents so future goal continuations do not
  restart stale post-PR213 work.

## Changed files

- `cmd/elnath/commands.go`
- `cmd/elnath/commands_help_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`
- `.omc/research/elnath-completion-program-control-2026-05-14.md`
- `.omc/research/elnath-final-completion-program-control-2026-05-14.md`
- `.omc/research/command-help-control-surface-drift-2026-05-14.md`

## Behavior added

- `elnath help` now includes every non-hidden registered command from
  `commandCatalog(false)`.
- Hidden internal commands remain omitted from `elnath help`.
- `elnath help` points users/operators to command-specific help and the
  structured JSON command catalog.
- `elnath explain control-surfaces` now says bounded `process_wait` supports
  literal `watch_text`, while full streaming/async line-watch remains deferred.
- Completion control docs now record PR #214 through PR #218 as shipped
  structural follow-ups.

## Verification

TDD failure before implementation:

- `go test ./cmd/elnath -run TestCmdHelpReflectsCommandCatalog -count=1`
  - FAIL as expected: help missing registered command `chaos`.

Focused checks after implementation:

- `go test ./cmd/elnath -run 'TestCmdHelpReflectsCommandCatalog|TestExecuteCommandAndHelpPaths|TestCommandCatalog|TestExecuteCommand_CommandsJSON|TestCommandRegistryBuiltFromSpecs|TestExplainControlSurfacesJSON|TestExplainControlSurfacesText|TestControlSurfaceManifestMatchesToolSearchRouting' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.740s`
- `go run ./cmd/elnath help | sed -n '1,80p'`
  - PASS: output includes registry-backed commands including `explain`, `task`,
    `provider`, `skill`, `commands`, and `debug`.

Broader checks:

- `go test ./cmd/elnath -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 31.434s`
- `go vet ./cmd/elnath`
  - PASS
- `git diff --check`
  - PASS
- `go run ./cmd/elnath explain control-surfaces --json | rg -n 'watch_text|streaming/async|process_wait|blocking wait'`
  - PASS: output includes `process_wait` and the `watch_text` boundary; no
    stale `blocking wait` line appeared.

## Benchmark / corpus / baseline

- Full v8 benchmark: no
- Current-only smoke: no
- Baseline: no
- Codex CLI comparison: no
- Claude Code comparison: no
- Benchmark corpus mutation: no
- Baseline mutation: no

## Claim boundary

Allowed:

- Top-level CLI help is registry-backed and no longer silently omits registered
  user-facing commands.
- Control-surface status now reflects `process_wait watch_text`.

Forbidden:

- Elnath is complete as a public product.
- Elnath is better than Claude Code or Codex.
- v8 benchmark passed.
- Full streaming/async process line-watch exists.
- Full LSP lifecycle exists.
- UI-level answer collection is complete.

## Remaining risk

- Top-level help is now English registry metadata even when locale-specific
  static `cli.help` translations exist. This favors correctness over stale
  localization. A future i18n slice can localize command metadata if needed.
- Command-specific help texts are still separate strings and can drift from
  dispatcher semantics; existing focused tests cover only selected commands.

## Next autonomous action

Run affected package tests, vet, and diff checks. If clean, commit this as one
coherent discoverability/status milestone and open one PR.
