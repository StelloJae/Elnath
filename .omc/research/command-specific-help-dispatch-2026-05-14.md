# Command-specific help dispatch repair

Date: 2026-05-14
Branch: `codex/post-pr219-next`
Lane: final completion program / command discoverability slice
Status: local implementation

## Problem found

PR #219 made top-level `elnath help` registry-backed. That exposed a second
discoverability bug: `executeCommand` intercepted `--help` before command
dispatch, then fell back to top-level help whenever a command did not have a
static `cmd.<name>.help` translation.

Real examples:

- `elnath task --help` showed top-level help instead of task subcommands.
- `elnath explain --help` showed top-level help instead of explain subcommands.
- `elnath provider --help` showed top-level help instead of provider usage.

This made the new top-level help line "Run `elnath <command> --help`" partly
false.

## References inspected

- Elnath:
  - `cmd/elnath/commands.go`
  - `cmd/elnath/cmd_task.go`
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

The reference pattern remains registry-first command metadata plus
command-specific help/dispatch surfaces derived from or routed through the same
command inventory.

## Chosen Elnath-native design

- Keep `executeCommand` registry-first.
- Unknown commands still print the unknown-command message and top-level help.
- For known commands with `--help` / `-h`:
  - use special/static command help first when available;
  - otherwise route selected commands to their inline `help` handler;
  - otherwise print a generated command-specific summary from `commandSpec`.
- Add `task help`, `task -h`, and `task --help` support so task's inline help is
  reachable from the dispatcher.

## Changed files

- `cmd/elnath/commands.go`
- `cmd/elnath/cmd_task.go`
- `cmd/elnath/commands_help_test.go`
- `.omc/research/command-specific-help-dispatch-2026-05-14.md`

## Behavior added

- `elnath task --help` shows task subcommands.
- `elnath explain --help` shows explain subcommands.
- `elnath provider --help` shows provider usage.
- Commands without static or inline detailed help receive a generated
  command-specific help summary from `commandSpec` instead of top-level help.
- Static command help remains preferred when it exists, preserving existing
  detailed help tests for commands like `agentic`, `daemon`, and `doctor`.

## Verification

TDD failure before implementation:

- `go test ./cmd/elnath -run TestExecuteCommand_CommandSpecificHelp -count=1`
  - FAIL as expected: `task`, `explain`, `provider`, and generated fallback all
    returned top-level help.

Focused checks after implementation:

- `go test ./cmd/elnath -run 'TestExecuteCommand_CommandSpecificHelp|TestCmdTaskUsage|TestPrintCommandHelp_AgenticMatchesDispatcher|TestPrintCommandHelp_DaemonMatchesDispatcher|TestPrintCommandHelp_DoctorMatchesDispatcher' -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 0.633s`

Broader checks:

- `go test ./cmd/elnath -count=1`
  - PASS: `ok github.com/stello/elnath/cmd/elnath 24.349s`
- `go vet ./cmd/elnath`
  - PASS
- `git diff --check`
  - PASS
- `go run ./cmd/elnath task --help | sed -n '1,24p'; go run ./cmd/elnath explain --help | sed -n '1,24p'; go run ./cmd/elnath version --help | sed -n '1,18p'`
  - PASS: task/explain show command-specific subcommands; version shows a
    generated command-specific summary.

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

- Known command `--help` now stays command-specific instead of falling back to
  top-level help.
- Task/explain/provider help dispatch is repaired.

Forbidden:

- Elnath is complete as a public product.
- Elnath is better than Claude Code or Codex.
- v8 benchmark passed.
- Full command localization is complete.
- Full UI-level answer collection, full LSP, or async process watch is complete.

## Remaining risk

- Some generated help entries are short summaries, not rich man pages.
- The inline-help allowlist is explicit. If a future command grows inline help,
  it should be added to `commandUsesInlineHelp` or given a static help string.

## Next autonomous action

Commit this as the command-specific help dispatch follow-up, open one coherent
PR, and merge when CI passes.
