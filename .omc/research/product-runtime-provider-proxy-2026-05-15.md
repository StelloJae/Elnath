# Product/runtime Milestone 4: local provider proxy

Date: 2026-05-15
Branch: `codex/product-runtime-provider-proxy`
Base HEAD: `ac5f0e0`
Status: implementation slice locally verified

## Purpose

Move Elnath provider/model flexibility from config-only toward an operator-usable
product surface. This slice adds a local OpenAI Responses-compatible proxy that
can be pointed to by external clients using `http://127.0.0.1:8645/v1`.

This is product/runtime work, not benchmark work.

## Reference files inspected

- `/Users/stello/elnath/.omc/research/elnath-product-runtime-100-control-2026-05-15.md`
- `/Users/stello/.hermes/hermes-agent/hermes_cli/proxy/server.py`
- `/Users/stello/.hermes/hermes-agent/tests/hermes_cli/test_proxy.py`
- `/Users/stello/.hermes/hermes-agent/website/docs/user-guide/features/subscription-proxy.md` via `git show origin/main`
- `/Users/stello/claude-code-src/src/upstreamproxy/upstreamproxy.ts`
- `/Users/stello/claude-code-src/src/upstreamproxy/relay.ts`
- `/Users/stello/claw-code/USAGE.md`
- `/Users/stello/claw-code/rust/crates/rusty-claude-cli/src/main.rs`
- `/Users/stello/claw-code/rust/crates/runtime/src/config.rs`
- `/Users/stello/elnath/cmd/elnath/cmd_provider.go`
- `/Users/stello/elnath/cmd/elnath/cmd_doctor.go`
- `/Users/stello/elnath/internal/config/config.go`
- `/Users/stello/elnath/internal/llm/responses.go`

## Behavior added

- Added `internal/providerproxy`:
  - provider adapter interface;
  - OpenAI Responses-compatible static adapter from `openai_responses` config;
  - local HTTP handler with `/health` and `/v1/*`;
  - explicit allowlist: `/responses`, `/models`;
  - OpenAI-style structured JSON errors:
    - `path_not_allowed`
    - `upstream_auth_failed`
    - `upstream_unreachable`
    - `upstream_timeout`
  - inbound `Authorization` stripping and upstream credential attachment;
  - hop-by-hop response header stripping;
  - request/response body passthrough with no request-body logging.

- Added `elnath proxy` CLI:
  - `elnath proxy status [--json]`
  - `elnath proxy providers [--json]`
  - `elnath proxy start [--host HOST] [--port PORT] [--provider openai-responses]`
  - default bind `127.0.0.1:8645`;
  - LAN exposure warning when host is not loopback.

- Added command catalog entry:
  - `proxy`, category `provider`.

- Added `doctor --json` visibility:
  - new `provider_proxy` check reports proxy provider readiness, base URL, and allowed paths.

## Verification

TDD red checks:

- `go test ./internal/providerproxy -count=1`
  - failed before implementation because `Credential`, `NewHandler`,
    `ServerOptions`, and `OpenAIResponsesAdapterFromConfig` did not exist.
- `go test ./cmd/elnath -run 'TestCmdProxy|TestCommandCatalogIncludesProxy' -count=1`
  - failed before implementation because `cmdProxy`,
    `proxyProviderView`, and `proxyStatusView` did not exist.

Passing checks:

- `go test ./internal/providerproxy -count=1`
  - PASS
- `go test ./cmd/elnath -run 'TestCmdProxy|TestCommandCatalogIncludesProxy|TestCmdDoctorJSONReportsLocalReadiness' -count=1`
  - PASS
- `go test ./internal/providerproxy ./cmd/elnath -count=1`
  - PASS
- `go test ./internal/... ./cmd/elnath -count=1`
  - PASS
- `go vet ./...`
  - PASS
- `git diff --check`
  - PASS

## Benchmark boundary

- Full v8 benchmark: not run.
- Baseline: not run.
- Codex comparison: not run.
- Claude Code comparison: not run.
- Benchmark corpus mutation: none.
- Baseline mutation: none.
- Benchmark superiority claim: none.

## Remaining risks

- This slice supports OpenAI Responses-compatible passthrough paths only:
  `/v1/responses` and `/v1/models`.
- `/v1/chat/completions` is not transformed into `/responses` in this slice.
- OAuth/refresh-token credential minting is not implemented yet; the adapter
  exposes a non-refreshable static config-backed credential.
- `proxy status` is a local readiness view, not a process supervisor that
  detects an already running proxy from another process.
- `doctor` checks configuration readiness; it does not perform a live upstream
  API call.

## Next recommended milestone

Milestone 5: provider/model/effort operator hardening.

Recommended next slice:

- show proxy support and effort compatibility in `provider status/check`;
- classify requested-provider auth failure vs unsupported effort vs timeout;
- ensure no silent fallback when a user explicitly asks for a provider/model
  that is not usable.
