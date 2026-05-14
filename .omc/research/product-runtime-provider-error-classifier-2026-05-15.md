# Product Runtime Provider Error Classifier Wiring

Date: 2026-05-15
Branch: `codex/product-runtime-watchdog`
Parent milestone commit: `33f138796ee19a6c7bd91280bdaa31a9ae1d01ef`

## Purpose

The daemon failure receipt path was still partly string-substring based. This
milestone wires daemon terminal failure metadata into Elnath's existing
`internal/agent/errorclass` classifier, then fills a narrow auth gap found during
testing: `invalid_grant refresh token expired` was not classified as auth.

This keeps the design Elnath-native and avoids adding a second provider error
taxonomy.

## References Inspected

Elnath:

- `internal/agent/errorclass/classify.go`
- `internal/agent/errorclass/category.go`
- `internal/agent/errorclass/classify_test.go`
- `internal/daemon/daemon.go`
- `internal/llm/anthropic.go`
- `internal/llm/responses.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/agent/bedrock_adapter.py`
- `/Users/stello/.hermes/hermes-agent/agent/gemini_cloudcode_adapter.py`
- `/Users/stello/.hermes/hermes-agent/agent/google_code_assist.py`
- `/Users/stello/.hermes/hermes-agent/tests/test_ctx_halving_fix.py`

claw-code:

- `/Users/stello/claw-code/rust/crates/api/src/error.rs`
- `/Users/stello/claw-code/rust/crates/rusty-claude-cli/src/main.rs`

Claude Code:

- `/Users/stello/claude-code-src/src/QueryEngine.ts`
- `/Users/stello/claude-code-src/src/query.ts`
- `/Users/stello/claude-code-src/src/remote/SessionsWebSocket.ts`

Reference pattern used:

- one shared error classifier should drive retry/recovery decisions;
- auth/rate/context/model failures should be distinguishable from generic tool
  runtime failure;
- context-window failures should not become stale-session retirement by default.

## Changed Files

- `internal/agent/errorclass/classify.go`
- `internal/agent/errorclass/classify_test.go`
- `internal/daemon/daemon.go`
- `internal/daemon/daemon_test.go`

## Behavior Added

Daemon failure receipt classification now maps `errorclass.Category` into daemon
receipt failure classes:

- `Auth`, `AuthPermanent`, `Billing` -> `provider_auth`
- `RateLimit`, `Overloaded` -> `provider_rate_limit`
- `Timeout`, `ServerError` -> `provider_timeout`
- `ContextOverflow`, `PayloadTooLarge` -> `context_window`
- `ModelNotFound` -> `model_not_found`
- `FormatError` -> `provider_error`
- unknown -> `tool_runtime`

New next actions:

- `context_window` -> `compact_context_before_retry`
- `model_not_found` -> `select_supported_model`

Classifier auth coverage now includes:

- `invalid_grant`
- `refresh token`
- `expired token`
- `oauth`

## Verification

Focused classifier and daemon tests:

```text
go test ./internal/agent/errorclass ./internal/daemon -run 'TestClassify_AuthRefreshTokenFailure|TestDaemon(ProviderAuthFailureRecordsRetirementReceipt|ProviderRateLimitDoesNotRetireSession|ContextWindowFailureUsesProviderClassifier|ModelNotFoundFailureUsesProviderClassifier|WorkerPanicRecordsRetirementReceipt)$' -count=1
PASS:
ok github.com/stello/elnath/internal/agent/errorclass 0.629s
ok github.com/stello/elnath/internal/daemon 5.767s
```

Touched packages:

```text
go test ./internal/agent/errorclass ./internal/daemon -count=1
PASS:
ok github.com/stello/elnath/internal/agent/errorclass 0.314s
ok github.com/stello/elnath/internal/daemon 41.077s
```

Internal packages:

```text
go test ./internal/... -count=1
PASS: all internal packages passed
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

- Daemon failure receipts now use the shared Elnath provider error classifier for
  auth/rate/context/model/provider categories.
- OAuth refresh-token style failures are now classified as auth.

Not allowed:

- All provider adapters emit structured status metadata.
- All provider failures are perfectly classified.
- Runtime completion is 100%.
- Benchmark readiness is proven.

## Remaining Risks

- Provider adapters still return mostly plain Go errors; status-code metadata is
  not consistently preserved across adapters.
- `provider_timeout` currently covers both timeout and server-error style
  transient provider failures in daemon receipts.
- The next milestone should move status-code preservation closer to LLM adapter
  boundaries.

## Next Milestone Recommendation

Proceed to adapter-level status/error wrapping:

1. add a small `llm.ProviderError` or equivalent typed error shape;
2. wrap Anthropic and Responses HTTP status failures with provider/status data;
3. feed that structured data into `errorclass.Classify`;
4. keep daemon receipt behavior unchanged but less dependent on text.

