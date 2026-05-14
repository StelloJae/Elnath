# Elnath completion program closeout

Date: 2026-05-14
Branch: codex/supervisor-scope-drift-guard
Lane: document-anchored supervisor/control-loop/tool/provider completion
Status: PR-ready local milestone bundle

## Summary

This branch completes the current document-anchored Elnath completion program
scope. It does not claim Elnath is fully equivalent to Claude Code, better than
Claude Code/Codex, or benchmark-ready. It closes the structural runtime lane
defined in `.omc/research/elnath-completion-program-control-2026-05-14.md`.

The branch moved away from benchmark symptom loops and implemented reference-
driven control-loop fixes:

- supervisor recovery scope drift guard
- verification ownership classification
- shell/diff mutation supervision
- shell command policy receipts
- model-facing shell/process guidance
- OpenAI Responses-compatible provider alias ergonomics
- callable control-surface manifest/receipt tightening

## Commits in this bundle

- `03f153b fix(runtime): fail closed on correction scope drift`
- `03c9bd9 fix(runtime): classify verification ownership before retry`
- `0890c6e fix(runtime): detect correction scope drift from git diff`
- `4e11413 feat(runtime): record shell command policy receipts`
- `daf16ce fix(tools): guide shell commands through supervisor policy`
- `f877eb7 feat(config): add generic responses provider aliases`
- `3293816 feat(tools): expose callable control surface receipts`

## Reference discipline

For these milestones, Elnath code was inspected before changes. Claude Code,
Hermes, and the local ccunpacked research were used as behavioral references,
not copied source.

Key references:

- `/Users/stello/claude-code-src/src/tools/BashTool/prompt.ts`
- `/Users/stello/claude-code-src/src/tools/BashTool/BashTool.tsx`
- `/Users/stello/claude-code-src/src/tools/ToolSearchTool/prompt.ts`
- `/Users/stello/claude-code-src/src/tools/TodoWriteTool/TodoWriteTool.ts`
- `/Users/stello/claude-code-src/src/constants/tools.ts`
- `/Users/stello/.hermes/hermes-agent/tools/todo_tool.py`
- `/Users/stello/.hermes/hermes-agent/environments/agent_loop.py`
- `/Users/stello/.hermes/hermes-agent/cli-config.yaml.example`
- `/Users/stello/.hermes/hermes-agent/RELEASE_v0.11.0.md`
- `.omc/research/ccunpacked-parity-refresh-2026-05-12.md`
- `.omc/research/claude-code-vs-elnath-control-loop-diagnosis-2026-05-14.md`
- `.omc/research/elnath-control-loop-structural-correction-2026-05-14.md`

## Milestone artifacts

- `.omc/research/supervisor-scope-drift-guard-milestone-a-2026-05-14.md`
- `.omc/research/supervisor-verification-ownership-milestone-b-2026-05-14.md`
- `.omc/research/supervisor-shell-diff-scope-milestone-c-2026-05-14.md`
- `.omc/research/command-execution-policy-milestone-d-2026-05-14.md`
- `.omc/research/tool-guidance-policy-milestone-e-2026-05-14.md`
- `.omc/research/provider-effort-control-milestone-f-2026-05-14.md`
- `.omc/research/callable-control-surface-milestone-g-2026-05-14.md`

## Changed behavior

### Supervisor / retry / verification

- Correction retries now fail closed on out-of-scope recovery.
- Verification ownership separates focused verification from unrelated broad
  failures.
- Shell/apply-patch/generated diff changes can be checked against allowed
  recovery scope.
- Broad unrelated verification failure is evidence, not automatic edit
  permission.

### Command execution policy

- Shell command receipts record command class metadata without storing raw
  command text.
- Timeout, cancellation, background recommendation, working-directory presence,
  and error classification are available to completion summaries and gates.

### Tool guidance

- Bash/process tool descriptions now steer long-running work through
  `process_start` / `process_monitor`.
- Model-facing guidance prefers focused verification first.
- Scope-lock stop conditions are explicit in the tool guidance.

### Provider / effort control

- Generic OpenAI Responses-compatible aliases are supported:
  - `ELNATH_RESPONSES_API_KEY`
  - `ELNATH_RESPONSES_BASE_URL`
  - `ELNATH_RESPONSES_MODEL`
  - `ELNATH_RESPONSES_REASONING_EFFORT`
  - `ELNATH_RESPONSES_TIMEOUT_SECONDS`
- Existing `ELNATH_OPENAI_RESPONSES_*` keys remain supported and keep
  precedence.
- This improves Kimi/Moonshot, MiniMax, Codex/GPT, and other Responses-shaped
  provider ergonomics without making Anthropic the mental default.

### Callable control surfaces

- `elnath explain control-surfaces` now includes:
  - `discovery`
  - `scratchpad`
  - `code_intelligence`
- `todo_write` now returns a structured receipt.
- `code_symbols` now returns a structured receipt.
- `todo_write` is routed as `scratchpad` instead of overloading `plan`.
- `code_intelligence` remains `partial`; Elnath has `code_symbols`, not a full
  LSP lifecycle.

## Verification

Focused and affected checks:

- `go test ./cmd/elnath -run 'TestCompletionRetryFailsClosedOnShellDiffScopeDrift' -count=1`
  - PASS
- `go test ./cmd/elnath -run 'TestCompletionRetry.*Scope|TestCompletion.*ScopeDrift|TestCompletion.*CorrectionAttempt|TestRecordOutcomePersistsCompletionObservability|TestCompletionGateContextProviderConsumesRuntimeSummary' -count=1`
  - PASS
- `go test ./cmd/elnath ./internal/orchestrator ./internal/agentic/completion ./internal/learning -count=1`
  - PASS
- `go test ./internal/tools -run TestBuiltinToolDescriptions -count=1`
  - PASS
- `go test ./internal/tools -count=1`
  - PASS
- `go test ./internal/config -run 'TestApplyEnvOverrides|TestLoad_OpenAIResponsesConfig|TestValidate' -count=1`
  - PASS
- `go test ./cmd/elnath -run 'TestNoProviderConfiguredMessageMentionsResponsesProvider|TestBuildProviderNoProviderMessagePrefersResponses|TestProviderCommandStatusJSON|TestExecutionRuntimeRunTaskProviderSlashCommandJSONReportsConfiguredCandidates|TestExecutionRuntimeRunTaskProviderSlashCommandListsCandidates' -count=1`
  - PASS
- `go test ./internal/config ./cmd/elnath -count=1`
  - PASS
- `go test ./cmd/elnath -run 'TestExplainControlSurfacesJSON|TestExplainControlSurfacesText|TestControlSurfaceManifestMatchesToolSearchRouting' -count=1`
  - PASS
- `go test ./internal/tools -run 'TestTodoWriteTool_SummarizesChecklist|TestCodeSymbolsToolDocumentSymbols|TestCodeSymbolsToolWorkspaceSymbolsReportsPartialParseErrors|TestToolSearchFindsCodeSymbolsAsDeferredCodeIntelligence' -count=1`
  - PASS
- `go test ./internal/agent -run 'TestBuildToolDefsSearchFirstDefersControlSurfaceTools|TestAgentSearchFirstLoadsSelectedDeferredToolNextTurn|TestPermissionWithActualToolNames' -count=1`
  - PASS

Broader checks:

- `go test ./cmd/elnath ./internal/tools -count=1`
  - PASS (`cmd/elnath` 44.599s, `internal/tools` 63.431s)
- `go test ./cmd/elnath ./internal/agent ./internal/tools ./internal/orchestrator ./internal/agentic/completion ./internal/learning -count=1`
  - FAIL only in `internal/agent` timing tests:
    - `TestPartition_WritesDifferentPaths_Parallel`
    - `TestPartition_BashBlocksReads`
  - classification: timing-sensitive test miss under broad load; no functional
    assertion failure in changed control-surface/provider/tool behavior.
- `go test ./internal/agent -run 'TestPartition_WritesDifferentPaths_Parallel|TestPartition_BashBlocksReads' -count=5`
  - PASS
- `go test ./internal/agent -count=1`
  - PASS (`11.305s`)
- `git diff --check`
  - PASS

## Benchmark / corpus / baseline

- Full v8 benchmark: no
- Current-only v8 smoke: no
- Baseline run: no
- Codex CLI comparison: no
- Claude Code comparison: no
- Benchmark corpus mutation: no
- Baseline mutation: no

## Claim boundary

Allowed:

- Elnath has a reference-driven supervisor/control-loop/tool/provider milestone
  bundle ready for PR review.
- Recovery scope drift, verification ownership, shell command receipts, tool
  guidance, generic Responses-compatible provider aliases, and callable
  control-surface receipts improved in this branch.
- This branch prepares Elnath to resume benchmark-readiness validation from a
  stronger runtime foundation.

Not allowed:

- Elnath is fully Claude Code-compatible.
- Elnath is better than Claude Code or Codex.
- Broad public benchmark superiority.
- v8 benchmark passed.
- Baseline/comparison evidence exists.
- Full LSP lifecycle exists.
- Streaming process line-watch exists.
- Broad silent self-healing exists.

## Unrelated dirty files excluded

- `.omc/research/elnath-final-autonomous-runtime-goal-2026-05-13.md`
- `scripts/run_current_benchmark_wrapper.sh`
- `scripts/test_benchmark_wrapper_v8_task_guidance.sh`
- `scripts/test_current_benchmark_wrapper_completion_guards.sh`
- `.claude/`
- `docs/superpowers/plans/2026-04-30-elnath-local-managed-runtime.md`

## Remaining risk

- `internal/agent` has timing-sensitive partition tests that can miss wallclock
  thresholds under broad package load. Focused rerun passed, but these tests
  may deserve a future non-wallclock synchronization rewrite.
- `user_input` remains partial by product boundary: structured receipts and CLI
  answer surfaces exist, but UI-level blocking answer collection is not part of
  this branch.
- `code_intelligence` remains partial by design: `code_symbols` is a small
  Go-native hook, not full LSP.
- This branch has not been validated by CI yet.

## Next autonomous action

Push this milestone bundle and open one coherent PR. Do not create more tiny
PRs. After PR CI/review, merge if gates pass, then resume benchmark-readiness
validation with a small current-only smoke only after main is clean.
