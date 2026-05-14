# Subcommand help coverage repair

Date: 2026-05-14
Branch: `codex/post-pr220-next`
Lane: final completion program / command discoverability slice
Status: local implementation

## Problem found

After PR #220, known command `--help` dispatch stayed command-specific.
Scanning registered commands showed remaining subcommand help drift:

- `elnath telegram --help` returned `unknown telegram subcommand: help`.
- `elnath eval --help` fell back to a generated one-line command summary, not
  eval subcommands.
- `elnath skill --help` fell back to a generated one-line command summary, not
  skill subcommands.
- `elnath profile --help` fell back to a generated one-line command summary,
  not profile subcommands.

These are not benchmark issues. They are command-surface discoverability gaps:
implemented control surfaces existed but were not reliably exposed to the
operator or model-facing command inventory.

## References inspected

- Elnath:
  - `cmd/elnath/commands.go`
  - `cmd/elnath/cmd_eval.go`
  - `cmd/elnath/cmd_skill.go`
  - `cmd/elnath/cmd_profile.go`
  - `cmd/elnath/cmd_telegram.go`
  - `cmd/elnath/commands_help_test.go`
- Claude Code:
  - `/Users/stello/claude-code-src/src/commands.ts`
  - `/Users/stello/claude-code-src/src/components/HelpV2/Commands.tsx`
  - `/Users/stello/claude-code-src/src/hooks/useMergedCommands.ts`
- Hermes:
  - `/Users/stello/.hermes/hermes-agent/AGENTS.md`
- claw-code:
  - `/Users/stello/claw-code/src/commands.py`
  - `/Users/stello/claw-code/src/main.py`

The reference pattern is command inventory consistency: registered commands,
help, autocomplete/menu surfaces, and dispatch should not diverge.

## Chosen Elnath-native design

- Add inline help handling for `eval`, `skill`, `profile`, and `telegram`.
- Add `skillUsage()` and `profileUsage()` helpers instead of overloading list
  behavior as help.
- Keep existing default no-arg behavior:
  - `skill` still lists skills.
  - `profile` still lists profiles.
  - `eval` and `telegram` still print usage when called without args.
- Extend `commandUsesInlineHelp` so dispatcher-level `--help` reaches these
  inline command help surfaces.

## Changed files

- `cmd/elnath/commands.go`
- `cmd/elnath/cmd_eval.go`
- `cmd/elnath/cmd_skill.go`
- `cmd/elnath/cmd_profile.go`
- `cmd/elnath/cmd_telegram.go`
- `cmd/elnath/commands_help_test.go`
- `.omc/research/subcommand-help-coverage-2026-05-14.md`

## Behavior added

- `elnath eval --help` shows eval subcommands including benchmark helper
  commands.
- `elnath skill --help` shows skill subcommands.
- `elnath profile --help` shows profile subcommands.
- `elnath telegram --help` shows Telegram subcommands instead of erroring.
- `elnath task --help`, `elnath explain --help`, and generated fallback help
  behavior from PR #220 remain intact.

## Verification

TDD failure before implementation:

- `go test ./cmd/elnath -run TestExecuteCommand_SubcommandHelpCoverage -count=1`
  - FAIL as expected: `eval`, `skill`, and `profile` returned generated
    summaries; `telegram` returned an unknown-subcommand error.

Focused checks after implementation:

- `go test ./cmd/elnath -run 'TestExecuteCommand_SubcommandHelpCoverage|TestExecuteCommand_CommandSpecificHelp' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.676s`

Broader checks:

- `go test ./cmd/elnath -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 40.565s`
- `go vet ./cmd/elnath`
  - PASS
- `git diff --check`
  - PASS
- `for c in eval skill profile telegram task explain version; do go run ./cmd/elnath "$c" --help ...; done`
  - PASS: first-line help output is command-specific for all checked commands.

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

- Subcommand help coverage is repaired for `eval`, `skill`, `profile`, and
  `telegram`.
- Command-specific help dispatch from PR #220 remains covered by focused tests.

Forbidden:

- Elnath is complete as a public product.
- Elnath is better than Claude Code or Codex.
- v8 benchmark passed.
- Full command localization is complete.
- Full UI-level answer collection, full LSP, or async process watch is complete.

## Remaining risk

- Some command-specific help remains concise. This slice fixes routing and
  coverage, not full documentation polish for every subcommand flag.

## Next autonomous action

Commit this as one command discoverability follow-up, open a coherent PR, and
merge after CI. Then continue the completion program from the next structural
blocker, not from benchmark reruns.
