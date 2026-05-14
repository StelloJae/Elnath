# Provider and effort control milestone F (2026-05-14)

## Status

Implemented and locally verified.

## Branch

`codex/supervisor-scope-drift-guard`

## Problem found

Elnath already has substantial provider and effort control:

- `openai_responses` provider configuration
- `provider: responses` alias
- `/provider`, `/model`, and `/effort` runtime commands
- auto effort routing with provider capability checks
- Responses API reasoning-effort fallback on unsupported 400/422 errors

The remaining small gap is naming and ergonomics. Generic OpenAI Responses-compatible providers such as Kimi/Moonshot or MiniMax still need env vars named `ELNATH_OPENAI_RESPONSES_*`, which makes the surface feel OpenAI-vendor-specific even when the configured `base_url` is not OpenAI.

## References inspected

- `/Users/stello/elnath/.omc/research/elnath-completion-program-control-2026-05-14.md`
- `/Users/stello/elnath/internal/config/config.go`
- `/Users/stello/elnath/internal/agent/reasoning_effort.go`
- `/Users/stello/elnath/internal/llm/responses.go`
- `/Users/stello/elnath/cmd/elnath/runtime_effort.go`
- `/Users/stello/elnath/cmd/elnath/runtime_provider.go`
- `/Users/stello/claude-code-src/src/cli/print.ts`
- `/Users/stello/claude-code-src/src/utils/effort.js`
- `/Users/stello/.hermes/hermes-agent/RELEASE_v0.11.0.md`
- `/Users/stello/.hermes/hermes-agent/cli-config.yaml.example`
- `/Users/stello/.hermes/hermes-agent/cron/scheduler.py`

## Reference findings

Claude Code exposes model and effort as session-level runtime controls with capability-aware effort display.

Hermes treats model/provider selection as a first-class routing surface, supports OpenAI-compatible and Responses API transports, and documents provider/base_url/model overrides. It also keeps reasoning effort as explicit config/runtime policy rather than hidden Anthropic-only behavior.

## Chosen Elnath-native design

Keep Elnath's existing `openai_responses` config key for compatibility, but add generic env aliases:

- `ELNATH_RESPONSES_API_KEY`
- `ELNATH_RESPONSES_BASE_URL`
- `ELNATH_RESPONSES_MODEL`
- `ELNATH_RESPONSES_REASONING_EFFORT`
- `ELNATH_RESPONSES_TIMEOUT_SECONDS`

The more explicit existing `ELNATH_OPENAI_RESPONSES_*` variables keep precedence when both are set.

## Changed files

- `/Users/stello/elnath/internal/config/config.go`
- `/Users/stello/elnath/internal/config/config_test.go`
- `/Users/stello/elnath/cmd/elnath/cmd_run.go`
- `/Users/stello/elnath/cmd/elnath/cmd_run_provider_test.go`
- `/Users/stello/elnath/cmd/elnath/commands.go`
- `/Users/stello/elnath/.omc/research/provider-effort-control-milestone-f-2026-05-14.md`

## Implemented behavior

- Added generic Responses-compatible env aliases:
  - `ELNATH_RESPONSES_API_KEY`
  - `ELNATH_RESPONSES_BASE_URL`
  - `ELNATH_RESPONSES_MODEL`
  - `ELNATH_RESPONSES_REASONING_EFFORT`
  - `ELNATH_RESPONSES_TIMEOUT_SECONDS`
- Preserved existing `ELNATH_OPENAI_RESPONSES_*` env vars.
- Made the explicit `ELNATH_OPENAI_RESPONSES_*` vars win when both generic and explicit vars are set.
- Updated no-provider guidance to mention the generic Responses key first.

## Verification

- `go test ./internal/config -run 'TestApplyEnvOverrides|TestLoad_OpenAIResponsesConfig|TestValidate' -count=1` -> PASS (`ok github.com/stello/elnath/internal/config 0.622s`)
- `go test ./cmd/elnath -run 'TestNoProviderConfiguredMessageMentionsResponsesProvider|TestBuildProviderNoProviderMessagePrefersResponses|TestProviderCommandStatusJSON|TestExecutionRuntimeRunTaskProviderSlashCommandJSONReportsConfiguredCandidates|TestExecutionRuntimeRunTaskProviderSlashCommandListsCandidates' -count=1` -> PASS (`ok github.com/stello/elnath/cmd/elnath 1.229s`)
- `go test ./internal/config ./cmd/elnath -count=1` -> PASS (`ok github.com/stello/elnath/internal/config 0.360s`; `ok github.com/stello/elnath/cmd/elnath 34.855s`)
- `git diff --check` -> PASS

## Benchmark

Not run.

## Corpus / baseline mutation

No corpus or baseline mutation.

## Unrelated dirty files excluded

Known unrelated dirty files remain excluded from this milestone:

- `.omc/research/elnath-final-autonomous-runtime-goal-2026-05-13.md`
- `scripts/run_current_benchmark_wrapper.sh`
- `scripts/test_benchmark_wrapper_v8_task_guidance.sh`
- `scripts/test_current_benchmark_wrapper_completion_guards.sh`
- `.claude/`
- `docs/superpowers/plans/2026-04-30-elnath-local-managed-runtime.md`

## Remaining risk

This improves provider ergonomics, not multi-named-provider routing. Multiple simultaneous custom Responses providers remain a later design problem.

## Next autonomous action

Run focused config/provider tests, update this artifact, commit Milestone F if clean, then continue to the next structural blocker.
