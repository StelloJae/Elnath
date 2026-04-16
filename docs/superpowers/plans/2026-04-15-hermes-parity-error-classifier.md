# Hermes Parity #12: Structured Error Classifier

**Target**: `feat/hermes-parity-12`
**Estimated LOC**: 300-400 (code) + ~200 (tests)
**Estimated time**: 4-6 hours (opencode)
**Depends on**: v0.5.1 merged (W1 jitter backoff + W3 lifecycle hooks)

## Context (read first)

### Current state

Error handling in Elnath is a flat string-match function at `internal/agent/agent.go:555`:

```go
func isRetryable(err error) bool {
    msg := err.Error()
    for _, marker := range []string{"429", "500", "502", "503", "504", "rate limit", "rate_limit"} {
        // substring scan
    }
    return false
}
```

Problems:
1. **No classification** — all retryable errors are treated identically. A rate limit (wait) vs server error (immediate retry) vs context overflow (compress) get the same response.
2. **No recovery hints** — the retry loop doesn't know whether to compress, rotate credentials, or fall back to another provider.
3. **Inconsistent error formats** — Anthropic returns `"rate limit (429)"`, OpenAI returns `"openai: http 429: ..."`, Ollama returns `"ollama: 503"`. The classifier must normalize across providers.
4. **No observability** — errors aren't logged with structured categories. PostLLMCall hook exists (v0.5.1 W3) but receives no classification.

### Hermes reference (Section F of exhaustive audit)

Hermes uses 13 categories with recovery hints:
```
auth, auth_permanent, billing, rate_limit, overloaded, server_error,
timeout, context_overflow, payload_too_large, model_not_found,
format_error, thinking_signature, long_context_tier, unknown
```

Each category carries: `retryable`, `should_compress`, `should_rotate_credential`, `should_fallback`.

Key heuristics:
- 402 disambiguation: transient ("try again") → rate_limit, permanent → billing
- Disconnect + large session → context_overflow (`tokens > context * 0.6 OR > 120K OR > 200 msgs`)
- OpenRouter metadata.raw parsing for wrapped errors

## Scope

### Task A — ErrorCategory type + ClassifiedError struct

New package `internal/agent/errorclass/`:

```go
package errorclass

type Category string

const (
    Auth              Category = "auth"
    AuthPermanent     Category = "auth_permanent"
    Billing           Category = "billing"
    RateLimit         Category = "rate_limit"
    Overloaded        Category = "overloaded"
    ServerError       Category = "server_error"
    Timeout           Category = "timeout"
    ContextOverflow   Category = "context_overflow"
    PayloadTooLarge   Category = "payload_too_large"
    ModelNotFound     Category = "model_not_found"
    FormatError       Category = "format_error"
    ThinkingExhausted Category = "thinking_exhausted"
    Unknown           Category = "unknown"
)

type Recovery struct {
    Retryable          bool
    ShouldCompress     bool
    ShouldRotateCred   bool
    ShouldFallback     bool
}

type ClassifiedError struct {
    Category Category
    Recovery Recovery
    Original error
    Message  string // normalized human-readable message
}

func (e *ClassifiedError) Error() string { return e.Message }
func (e *ClassifiedError) Unwrap() error { return e.Original }
```

`long_context_tier` from Hermes is merged into `context_overflow` — Elnath doesn't distinguish tier-based context limits. `thinking_signature` renamed to `thinking_exhausted` for clarity.

### Task B — Default recovery table

```go
var defaultRecovery = map[Category]Recovery{
    Auth:              {Retryable: false, ShouldRotateCred: true},
    AuthPermanent:     {Retryable: false, ShouldFallback: true},
    Billing:           {Retryable: false, ShouldFallback: true},
    RateLimit:         {Retryable: true,  ShouldRotateCred: true},
    Overloaded:        {Retryable: true},
    ServerError:       {Retryable: true},
    Timeout:           {Retryable: true},
    ContextOverflow:   {Retryable: false, ShouldCompress: true},
    PayloadTooLarge:   {Retryable: false, ShouldCompress: true},
    ModelNotFound:     {Retryable: false, ShouldFallback: true},
    FormatError:       {Retryable: false},
    ThinkingExhausted: {Retryable: true},
    Unknown:           {Retryable: false},
}
```

### Task C — Classify function

```go
type Context struct {
    Provider       string // "anthropic", "openai", "ollama"
    StatusCode     int    // 0 if not an HTTP error
    TokensUsed     int    // current session token count
    ContextLimit   int    // model's context window
    MessageCount   int    // number of messages in session
}

func Classify(err error, ctx Context) ClassifiedError
```

Classification rules (evaluated in order, first match wins):

1. **err == nil** → panic (caller bug)
2. **StatusCode == 401 OR message contains "unauthorized"/"invalid.*api.key"** → `Auth`
3. **StatusCode == 403 AND message contains "disabled"/"suspended"/"banned"** → `AuthPermanent`
4. **StatusCode == 403** → `Auth` (may be transient API key issue)
5. **StatusCode == 402 AND message contains "try again"/"retry"** → `RateLimit` (Hermes 402 disambiguation)
6. **StatusCode == 402** → `Billing`
7. **StatusCode == 429 OR message contains "rate.limit"/"too many requests"** → `RateLimit`
8. **StatusCode == 529 OR message contains "overloaded"** → `Overloaded`
9. **StatusCode in [500, 502, 503, 504]** → `ServerError`
10. **message contains "timeout"/"deadline exceeded"/"context deadline"** → `Timeout`
11. **message contains "context.*length"/"too many tokens"/"max.*tokens"/"context_length_exceeded"** → `ContextOverflow`
12. **Heuristic: StatusCode == 0 AND connection error AND session is large** (ctx.TokensUsed > ctx.ContextLimit*60/100 OR ctx.TokensUsed > 120_000 OR ctx.MessageCount > 200) → `ContextOverflow`
13. **message contains "payload.*too.*large"/"request.*too.*large"/"413"** → `PayloadTooLarge`
14. **StatusCode == 404 OR message contains "model.*not.*found"/"does not exist"** → `ModelNotFound`
15. **message contains "invalid.*request"/"malformed"/"parse error"** → `FormatError`
16. **message contains "thinking"/"budget.*exhaust"** AND response has empty visible content → `ThinkingExhausted`
17. **everything else** → `Unknown`

All message matching is case-insensitive. Regex patterns use `(?i)`.

### Task D — Wire into agent.go retry loop

Replace `isRetryable(err)` with classifier-based decision in `streamWithRetry`:

```go
// Before (agent.go:313):
if isRetryable(err) {
    lastErr = err
    continue
}

// After:
classified := errorclass.Classify(err, errorclass.Context{
    Provider:     a.provider.Name(),
    StatusCode:   extractStatusCode(err),
    TokensUsed:   totalUsage.InputTokens + totalUsage.OutputTokens,
    ContextLimit: /* from model metadata or config */,
    MessageCount: len(req.Messages),
})
if classified.Recovery.Retryable {
    lastErr = &classified
    continue
}
if classified.Recovery.ShouldCompress {
    // Log and return a sentinel so the Run loop can trigger compression
    return llm.Message{}, req, llm.UsageStats{}, &classified
}
return llm.Message{}, req, llm.UsageStats{}, &classified
```

In the `Run` loop, check if the returned error is a `ClassifiedError` with `ShouldCompress`:

```go
if ce, ok := err.(*errorclass.ClassifiedError); ok && ce.Recovery.ShouldCompress {
    a.logger.Warn("context overflow detected, triggering compression",
        "category", ce.Category,
        "tokens", totalUsage.InputTokens+totalUsage.OutputTokens,
    )
    // Trigger compression and retry the iteration
    // (details depend on how ContextWindow is accessible from agent)
}
```

**Keep `isRetryable` as a deprecated private function** for one release cycle — test coverage references it. Remove in v0.6.0.

### Task E — extractStatusCode helper

Provider errors embed status codes in different formats. Add a helper that tries multiple extraction strategies:

```go
func extractStatusCode(err error) int
```

1. Check for `interface{ StatusCode() int }` (typed provider errors)
2. Regex scan for `(\b[0-9]{3}\b)` in error message — take first 3-digit code in HTTP range (400-599)
3. Return 0 if not found

### Task F — Structured logging via PostLLMCall hook

Create `internal/agent/errorclass/hook.go`:

```go
type LoggingHook struct {
    logger *slog.Logger
}

func (h *LoggingHook) PreLLMCall(ctx context.Context, req *llm.Request) error { return nil }

func (h *LoggingHook) PostLLMCall(ctx context.Context, req llm.Request, resp llm.ChatResponse, usage llm.UsageStats) error {
    // No-op on success — classification only happens on errors
    return nil
}
```

The classification logging happens in `streamWithRetry` itself (Task D), not the hook. However, the hook interface exists for future plugins that want to observe classified errors. Add an optional `ErrorObserver` interface:

```go
type ErrorObserver interface {
    OnClassifiedError(ctx context.Context, classified ClassifiedError) error
}
```

Wire in HookRegistry: after classification in streamWithRetry, call `hooks.RunOnClassifiedError(ctx, classified)` (new Run method, same pattern as W3 lifecycle hooks).

## Files touched

- `internal/agent/errorclass/category.go` — NEW: Category enum + Recovery struct + ClassifiedError type + defaultRecovery table
- `internal/agent/errorclass/classify.go` — NEW: Classify function + classification rules
- `internal/agent/errorclass/classify_test.go` — NEW: tests
- `internal/agent/agent.go` — replace `isRetryable` usage with `Classify`, add `extractStatusCode`, handle `ShouldCompress` in Run loop
- `internal/agent/agent_test.go` — update retry tests to use classified errors
- `internal/agent/hooks.go` — add `ErrorObserver` interface + `RunOnClassifiedError` method
- `internal/agent/hooks_test.go` — test ErrorObserver

**DO NOT TOUCH**:
- `internal/conversation/*` (compression trigger is a signal from agent, not a direct call)
- `internal/llm/*` (provider error formats stay as-is; classifier normalizes at the agent layer)
- `internal/tools/*`

## Required tests

### classify_test.go
1. `TestClassify_RateLimit429` — Anthropic "rate limit (429)" → RateLimit, Retryable=true
2. `TestClassify_RateLimitOpenAI` — "openai: http 429: ..." → RateLimit
3. `TestClassify_Overloaded529` — "overloaded (529)" → Overloaded, Retryable=true
4. `TestClassify_ServerError500` — "status 500" → ServerError, Retryable=true
5. `TestClassify_ServerError502` — "502 bad gateway" → ServerError
6. `TestClassify_Auth401` — "unauthorized" → Auth, ShouldRotateCred=true
7. `TestClassify_AuthPermanent403` — "account suspended" → AuthPermanent, ShouldFallback=true
8. `TestClassify_Billing402` — "payment required" → Billing, ShouldFallback=true
9. `TestClassify_Billing402Transient` — "402: try again later" → RateLimit (disambiguation)
10. `TestClassify_ContextOverflow` — "context_length_exceeded" → ContextOverflow, ShouldCompress=true
11. `TestClassify_ContextOverflowHeuristic` — connection error + large session → ContextOverflow
12. `TestClassify_PayloadTooLarge` — "request too large" → PayloadTooLarge, ShouldCompress=true
13. `TestClassify_ModelNotFound` — "model does not exist" → ModelNotFound, ShouldFallback=true
14. `TestClassify_Timeout` — "context deadline exceeded" → Timeout, Retryable=true
15. `TestClassify_FormatError` — "invalid request body" → FormatError, Retryable=false
16. `TestClassify_Unknown` — "something unexpected" → Unknown
17. `TestClassify_OllamaError` — "ollama: 503" → ServerError (cross-provider normalization)
18. `TestClassifiedError_Unwrap` — errors.Is/As chain preserved

### agent_test.go updates
19. `TestStreamWithRetry_ClassifiesBeforeRetry` — verify classified error is used for retry decision
20. `TestStreamWithRetry_ContextOverflowReturnsClassifiedError` — ShouldCompress propagated to caller

### hooks_test.go
21. `TestErrorObserverHookFires` — register ErrorObserver, trigger classification, verify called with correct ClassifiedError

## Behavior invariants

- Classification is **deterministic**: same error + same context = same category, always
- `isRetryable` behavior is preserved: every error that was retryable before is still retryable (RateLimit, Overloaded, ServerError, Timeout all have Retryable=true)
- Classification never panics on weird input (empty string, nil fields in context) — defaults to Unknown
- Recovery hints are **advisory** — the agent loop decides what to actually do
- `ClassifiedError` implements `error` and `Unwrap()` — standard Go error chain preserved
- No provider changes required — classifier operates on error messages at the agent layer

## Verification

```bash
go test -race ./internal/agent/errorclass/...
go test -race ./internal/agent/...
go build ./...
go test -race ./...
```

## PR body template

```
## Summary

- 13-category structured error classifier replacing flat `isRetryable` string matcher
- Categories: auth, auth_permanent, billing, rate_limit, overloaded, server_error, timeout, context_overflow, payload_too_large, model_not_found, format_error, thinking_exhausted, unknown
- Each category carries recovery hints (retryable, should_compress, should_rotate_cred, should_fallback)
- Cross-provider normalization (Anthropic/OpenAI/Ollama error formats → unified classification)
- ErrorObserver hook interface for classified-error observability
- 21 tests covering all categories + provider variants + error chain preservation

Hermes parity item #12. Completes 8/8 Hermes parity punch list.

## Test plan

- [ ] `go test -race ./internal/agent/errorclass/...` PASS
- [ ] `go test -race ./internal/agent/...` PASS
- [ ] `go test -race ./...` full suite PASS
- [ ] Existing retry behavior unchanged: 429/5xx still retried
```

## Notes for the worker

- `feedback_no_stubs.md`: classifier must actually parse error messages, not return hardcoded values. The regex patterns must match real provider error strings (see `internal/llm/anthropic.go:127-134`, `internal/llm/openai.go:114`, `internal/llm/ollama_test.go:176`).
- `feedback_baseline_recovery_scope.md`: do not change existing retry behavior. Every error that `isRetryable` currently catches must still be retried. The classifier adds *more* information, it doesn't change existing decisions.
- The `ShouldCompress` path in the Run loop may be complex. If full compression integration is too large for this PR, it's acceptable to: (1) classify the error, (2) log it with slog, (3) return it to the caller. The actual "trigger compression on context_overflow" can be a follow-up PR. What MUST ship: the classifier, the replacement of isRetryable, and the recovery hints.
- `extractStatusCode` should be robust against false positives (e.g., "file has 500 lines" shouldn't match). Check that the 3-digit number is preceded by a non-digit or start-of-string, and followed by a non-digit or end-of-string.
- Hermes's `long_context_tier` is irrelevant for Elnath (no tier system). Merged into `context_overflow`.
- Hermes's `thinking_signature` is about Anthropic's thinking-mode signature blocks. Renamed to `thinking_exhausted` — the scenario where all output tokens go to thinking and visible content is empty. This already has partial handling in `isEmptyAssistantMessage` (agent.go:347) — classifier should recognize this pattern.
