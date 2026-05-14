# Product Runtime Provider Typed Error Boundary

Date: 2026-05-15
Branch: `codex/product-runtime-watchdog`
Parent milestone commit: `a165e21`

## Purpose

Elnath's daemon receipts now use the shared provider error classifier, but LLM
adapters were still returning mostly plain text HTTP errors. That made runtime
supervision depend on error strings instead of structured provider/status
metadata.

This milestone adds an Elnath-native typed provider error at the LLM adapter
boundary, then verifies that Anthropic, OpenAI Responses, and legacy OpenAI HTTP
failures preserve provider name and HTTP status through wrapping.

## References Inspected

Elnath:

- `internal/llm/provider.go`
- `internal/llm/anthropic.go`
- `internal/llm/responses.go`
- `internal/llm/openai.go`
- `internal/agent/errorclass/classify.go`
- `internal/daemon/daemon.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/agent/gemini_native_adapter.py`
- `/Users/stello/.hermes/hermes-agent/agent/bedrock_adapter.py`
- `/Users/stello/.hermes/hermes-agent/agent/google_code_assist.py`
- `/Users/stello/.hermes/hermes-agent/tests/agent/test_auxiliary_client.py`

claw-code:

- `/Users/stello/claw-code/rust/crates/api/src/error.rs`

Claude Code:

- `/Users/stello/claude-code-src/src/QueryEngine.ts`
- `/Users/stello/claude-code-src/src/query.ts`
- `/Users/stello/claude-code-src/src/remote/SessionsWebSocket.ts`

Reference pattern used:

- keep provider/API status metadata as data, not only text;
- expose retryable/status/error class events without hiding the original error;
- keep model/runtime fallback decisions downstream of explicit classifier data.

## Changed Files

- `internal/llm/provider_error.go`
- `internal/llm/anthropic.go`
- `internal/llm/anthropic_test.go`
- `internal/llm/responses.go`
- `internal/llm/responses_test.go`
- `internal/llm/openai.go`
- `internal/llm/openai_test.go`
- `internal/agent/errorclass/classify.go`
- `internal/agent/errorclass/classify_test.go`

## Behavior Added

New `llm.ProviderError` carries:

- provider name
- HTTP status code
- provider error code/type when available
- provider message when available
- bounded body snippet
- optional wrapped cause

Adapters now emit it for non-200 HTTP responses:

- Anthropic
- OpenAI Responses
- legacy OpenAI Chat Completions

`errorclass.Classify` now reads typed provider metadata through a small interface:

- `ProviderName() string`
- `HTTPStatusCode() int`
- `ProviderErrorCode() string`

This keeps `internal/agent/errorclass` decoupled from `internal/llm` while still
allowing structured provider errors to drive classification.

## Verification

Initial TDD failure before implementation:

```text
go test ./internal/agent/errorclass ./internal/llm -run 'TestClassify_UsesProviderErrorMetadata|TestAnthropicHTTPErrors|TestResponsesHTTPError' -count=1
FAIL:
internal/llm: undefined: ProviderError
TestClassify_UsesProviderErrorMetadata: Category = "unknown", want "rate_limit"
```

Focused provider/error classifier tests:

```text
go test ./internal/agent/errorclass ./internal/llm -run 'TestClassify_UsesProviderErrorMetadata|TestAnthropicHTTPErrors|TestResponsesHTTPError|TestOpenAIHTTPError|TestResponsesUnsupportedReasoningEffortFallback' -count=1
PASS:
ok github.com/stello/elnath/internal/agent/errorclass 0.605s
ok github.com/stello/elnath/internal/llm 0.655s
```

Focused classifier/LLM/daemon receipt regression:

```text
go test ./internal/agent/errorclass ./internal/llm ./internal/daemon -run 'TestClassify_UsesProviderErrorMetadata|TestAnthropicHTTPErrors|TestResponsesHTTPError|TestOpenAIHTTPError|TestDaemon(ContextWindowFailureUsesProviderClassifier|ModelNotFoundFailureUsesProviderClassifier|ProviderAuthFailureRecordsRetirementReceipt|ProviderRateLimitDoesNotRetireSession)$' -count=1
PASS:
ok github.com/stello/elnath/internal/agent/errorclass 0.375s
ok github.com/stello/elnath/internal/llm 0.493s
ok github.com/stello/elnath/internal/daemon 4.746s
```

Touched packages:

```text
go test ./internal/agent/errorclass ./internal/llm ./internal/daemon -count=1
PASS:
ok github.com/stello/elnath/internal/agent/errorclass 0.355s
ok github.com/stello/elnath/internal/llm 0.551s
ok github.com/stello/elnath/internal/daemon 41.909s
```

Internal packages:

```text
go test ./internal/... -count=1
PASS: all internal packages passed
```

CLI package:

```text
go test ./cmd/elnath -count=1
PASS:
ok github.com/stello/elnath/cmd/elnath 46.670s
```

Whitespace:

```text
git diff --check
PASS
```

## Benchmark / Corpus Boundary

- Full v8 benchmark: not run
- Baseline: not run
- Codex comparison: not run
- Claude Code comparison: not run
- Benchmark corpus mutation: no
- Baseline mutation: no
- Benchmark superiority claim: no

## Claim Boundary

Allowed:

- LLM provider HTTP errors now preserve provider name and HTTP status through
  `errors.As`.
- Anthropic, OpenAI Responses, and legacy OpenAI non-200 responses emit typed
  provider metadata.
- The shared classifier can classify errors using typed provider status metadata.

Not allowed:

- All provider/runtime failures are perfectly classified.
- All streaming SSE provider errors carry typed provider metadata.
- Runtime completion is 100%.
- Benchmark readiness is proven.

## Remaining Risks

- Anthropic SSE `event: error` still returns a plain stream error. A later slice
  can type stream-error payloads if needed.
- Provider error code/message extraction is intentionally small and supports the
  common `{"error": ...}` shapes only.
- Retry-after metadata is not yet surfaced from provider headers.

## Next Milestone Recommendation

Proceed to retry-after / backoff receipt wiring or stream-error typing, depending
on which control-document gate is the next highest product/runtime blocker.
