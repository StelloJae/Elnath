# Code symbols gitignore filter

Date: 2026-05-14
Branch: `codex/post-pr214-next`
Lane: final completion program / code-intelligence slice
Status: implemented locally

## Problem

After PR #214, the next remaining structural boundary was code intelligence.

Elnath had a Go-native `code_symbols` tool, but workspace symbol search could
return symbols from gitignored files. Claude Code's LSP tool filters
location-based results through `git check-ignore`, so Elnath's current behavior
was noisier and less aligned with reference control-loop quality.

## References inspected

- Elnath: `internal/tools/code_symbols.go`
- Elnath tests: `internal/tools/code_symbols_test.go`
- Claude Code: `/Users/stello/claude-code-src/src/tools/LSPTool/LSPTool.ts`
- Claude Code: `/Users/stello/claude-code-src/src/services/lsp/LSPServerManager.ts`
- Hermes: `/Users/stello/.hermes/hermes-agent/tools/process_registry.py`

Hermes did not expose an equivalent LSP/symbol surface in the inspected path.
Claude Code was used as behavior reference only. No source, prompt, or error
text was copied.

## Design

Keep Elnath Go-native. Do not add full LSP.

For `workspace_symbols`:

- collect session-scoped `.go` candidates
- batch-check candidate relative paths with `git -C <session> check-ignore --stdin -z`
- skip ignored files when the repository supports gitignore checks
- fail open when `git` is unavailable, the directory is not a git repo, or the
  gitignore check errors
- preserve existing parse-error and symlink-escape behavior

## Changed files

- `internal/tools/code_symbols.go`
- `internal/tools/code_symbols_test.go`
- `.omc/research/code-symbols-gitignore-filter-2026-05-14.md`

## Verification

- Initial TDD check:
  - `go test ./internal/tools -run TestCodeSymbolsToolWorkspaceSymbolsSkipsGitIgnoredFiles -count=1`
  - FAIL as expected before implementation: ignored symbol was returned.
- Focused after implementation:
  - `go test ./internal/tools -run 'TestCodeSymbolsToolWorkspaceSymbolsSkipsGitIgnoredFiles|TestCodeSymbolsToolWorkspaceSymbolsFiltersQueryAndCaps|TestCodeSymbolsToolWorkspaceSymbolsReportsPartialParseErrors|TestCodeSymbolsToolDocumentSymbols' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/tools 0.742s`
- Affected package:
  - `go test ./internal/tools -count=1`
  - PASS: `ok github.com/stello/elnath/internal/tools 41.376s`
- Broader affected CLI/tool check:
  - `go test ./cmd/elnath ./internal/tools -count=1`
  - PASS: `cmd/elnath 26.511s`, `internal/tools 43.248s`
- Vet:
  - `go vet ./internal/tools`
  - PASS
- Whitespace:
  - `git diff --check`
  - PASS

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

- `code_symbols workspace_symbols` now skips gitignored Go files when gitignore
  information is available.
- This improves Elnath's code-intelligence hygiene without claiming full LSP.

Forbidden:

- Full LSP lifecycle exists.
- Elnath matches Claude Code's LSP feature set.
- Benchmark success or superiority.

## Remaining risk

- This uses `git check-ignore`; non-git directories and git failures fail open
  to preserve existing behavior.
- Only Go-native symbol lookup is covered. Definition, hover, references, and
  diagnostics remain future LSP-grade work.

## Next autonomous action

Commit this code-intelligence slice as one coherent milestone. If clean, open a
single PR and let CI decide. Do not run benchmark lanes for this slice.
