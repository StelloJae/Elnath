# W3: Hermes Parity — #11 tool arg type coercion + #15 plugin lifecycle hooks

**Target**: `feat/hermes-parity-w3`
**Estimated LOC**: 300-450 (code) + ~200 (tests)
**Estimated time**: 4-6 hours (opencode)
**Depends on**: nothing
**Conflicts with**: nothing (owns `internal/tools/registry.go` + `internal/agent/hooks.go` + `internal/agent/executor.go` surgical additions; W1/W2 must not touch these)

## Context (read first)

### #11 tool arg type coercion

LLM providers occasionally return tool-call arguments with the wrong JSON type:
- `{"offset": "10"}` (string) when the tool schema declares `int`
- `{"recursive": "true"}` (string) when the schema declares `bool`
- `{"count": 5.0}` (float) when the schema declares `int`

Currently each tool's `Execute(params json.RawMessage)` calls `json.Unmarshal` directly into a typed struct. A string-to-int mismatch returns a Go error, which becomes a tool failure. Agent retries without recourse — Hermes coerces before unmarshaling and avoids the failure entirely.

### #15 plugin lifecycle hooks

Current Elnath hooks (`internal/agent/hooks.go`) expose **3 stages**:
- `PreToolUse(ctx, toolName, params)` — can deny or allow
- `PostToolUse(ctx, toolName, params, result)` — observer only
- `OnStop(ctx)` — end-of-run finalizer

Hermes exposes **10 stages**. Cherry-pick the highest-value missing ones; **do not add all 10** (YAGNI). Add these 4:
1. `PreLLMCall(ctx, request)` — can observe/mutate outgoing LLM request (e.g., inject system context)
2. `PostLLMCall(ctx, request, response)` — observer after a provider stream completes
3. `OnCompression(ctx, beforeCount, afterCount)` — fires after Stage 2 auto-compression
4. `OnIterationStart(ctx, iteration, maxIterations)` — start of each agent loop iteration

## W3 scope

### Task A — Type coercion helper

Create new file `internal/tools/coerce.go`:

```go
// CoerceToolArgs inspects the target struct's JSON tags and coerces common
// type mismatches in params before unmarshaling.
// Supported coercions:
//   string "123" -> int/int64 123
//   string "true" / "false" -> bool
//   float64 5.0 -> int/int64 5 (only if no fractional part)
//   string "1.5" -> float64 1.5
// Returns a new json.RawMessage with coerced types; unchanged if no coercion needed.
func CoerceToolArgs(params json.RawMessage, target any) json.RawMessage
```

**Implementation approach**:
- Use reflect.TypeOf(target).Elem() to enumerate fields + json tags + field kinds
- Unmarshal params into `map[string]json.RawMessage`
- For each field, check if the raw value type mismatches the target kind; if so, coerce
- Re-marshal the map

**Integration**: in `internal/agent/executor.go` before the tool's `Execute` call, wrap the params:
```go
params = tools.CoerceToolArgs(params, targetStruct)
```

But tools don't expose their target struct directly. Two options:
- **Option A (preferred)**: each tool implements an optional `ArgsTarget() any` interface method that returns a pointer to a zero-valued args struct. Coercion applies only to tools that implement it.
- **Option B**: parse JSON Schema from `Tool.Schema()` and infer types at runtime. More code, no tool changes needed.

**Go with Option A**. Opt-in per tool, explicit, no reflection surprises.

New interface in `internal/tools`:
```go
// ArgTargetProvider is implemented by tools that want automatic arg coercion.
// The returned pointer receives a fresh zero-valued struct of the tool's args type.
type ArgTargetProvider interface {
    ArgsTarget() any
}
```

Apply `ArgsTarget` + `CoerceToolArgs` to at least:
- `read_file` (offset, limit → int)
- `bash` (timeout_ms → int, stream → bool if present)
- `glob` (limit → int)
- `grep` (multiline → bool, case_sensitive → bool, head_limit → int)

### Task B — Plugin lifecycle hook stages

Extend `internal/agent/hooks.go` interface:

```go
// Hook is the interface for agent lifecycle hooks.
// All methods have default no-op behavior; implement only what you need.
type Hook interface {
    PreToolUse(ctx context.Context, toolName string, params json.RawMessage) (HookResult, error)
    PostToolUse(ctx context.Context, toolName string, params json.RawMessage, result *tools.Result) error
}

// LLMHook adds optional observation/mutation points around provider calls.
// Implemented optionally; HookRegistry type-asserts to detect support.
type LLMHook interface {
    PreLLMCall(ctx context.Context, req *llm.Request) error
    PostLLMCall(ctx context.Context, req llm.Request, resp llm.ChatResponse, usage llm.UsageStats) error
}

// CompressionHook observes Stage 2 auto-compression events.
type CompressionHook interface {
    OnCompression(ctx context.Context, beforeCount, afterCount int) error
}

// IterationHook observes the start of each agent loop iteration.
type IterationHook interface {
    OnIterationStart(ctx context.Context, iteration, maxIterations int) error
}
```

**Why split interfaces**: keeps `Hook` backward compatible. Existing hooks (CommandHook) don't need changes. New features opt in.

**HookRegistry extensions**:
```go
func (r *HookRegistry) RunPreLLMCall(ctx context.Context, req *llm.Request) error {
    for _, h := range r.hooks {
        if lh, ok := h.(LLMHook); ok {
            if err := lh.PreLLMCall(ctx, req); err != nil {
                return err
            }
        }
    }
    return nil
}
// Similarly RunPostLLMCall, RunOnCompression, RunOnIterationStart
```

**Wiring**:
- `internal/agent/agent.go`: call `RunOnIterationStart` at the top of the main loop
- `internal/agent/agent.go`: call `RunPreLLMCall` before `a.streamWithRetry`; `RunPostLLMCall` after
- `internal/conversation/context.go`: CAN'T be touched (W2 exclusive territory). Instead, add the compression hook trigger at the `cmd/elnath/runtime.go` layer by extending the existing `ctxWindow.OnAutoCompress` callback: when it fires, also call `hooks.RunOnCompression`. This keeps the `internal/conversation` package hooks-agnostic and W2 unaffected.

### Task C — Tests

**coerce_test.go** (new file):
1. `TestCoerceToolArgs_StringToInt` — `{"offset": "42"}` with struct tagged int → 42
2. `TestCoerceToolArgs_StringToBool` — `{"recursive": "true"}` → true
3. `TestCoerceToolArgs_FloatToInt` — `{"limit": 5.0}` → 5; `{"limit": 5.5}` → unchanged (can't coerce)
4. `TestCoerceToolArgs_StringToFloat` — `{"threshold": "1.5"}` → 1.5
5. `TestCoerceToolArgs_AlreadyCorrect` — `{"offset": 42}` → unchanged
6. `TestCoerceToolArgs_UnknownField` — extra fields in input are preserved without change
7. `TestReadToolWithCoercion` — end-to-end: pass `{"offset": "10", "limit": "5"}` to read_file via executor, verify result

**hooks_test.go extensions**:
8. `TestLLMHookPreCall` — hook's PreLLMCall sees the outgoing Request
9. `TestLLMHookPostCall` — hook's PostLLMCall sees the response and usage
10. `TestCompressionHookFires` — register a CompressionHook; trigger compression; hook called with (beforeCount, afterCount)
11. `TestIterationHookFires` — register an IterationHook; run agent for 3 iterations; hook called 3 times with correct (iter, max)
12. `TestPartialHookInterface` — hook implements Hook but NOT LLMHook; verify RunPreLLMCall skips it without panic

## Files touched

- `internal/tools/coerce.go` — NEW
- `internal/tools/coerce_test.go` — NEW
- `internal/tools/file.go` — add `ArgsTarget` method to ReadTool (and WriteTool/EditTool if they have coercion candidates)
- `internal/tools/bash.go` — add `ArgsTarget` method
- `internal/tools/glob.go`, `internal/tools/grep.go` (if separate files) — add `ArgsTarget`
- `internal/tools/registry.go` — integrate coercion in the execute path (before tool's Execute)
- `internal/agent/hooks.go` — add 3 new interfaces (LLMHook, CompressionHook, IterationHook) + corresponding Run methods
- `internal/agent/hooks_test.go` — new hook tests
- `internal/agent/agent.go` — call RunOnIterationStart / RunPreLLMCall / RunPostLLMCall in the Run loop (small surgical edits)
- `cmd/elnath/runtime.go` — chain RunOnCompression to the OnAutoCompress callback (keep W1's ResetDedup AND add compression-hook trigger)

**DO NOT TOUCH**:
- `internal/conversation/*` (W2)
- `internal/agent/executor.go` internals beyond the tool-args coercion entry point

## Behavior invariants

- Tools that do NOT implement `ArgsTarget` continue to work exactly as before
- Hooks that implement only the base `Hook` interface work unchanged (partial interface)
- Hook errors (LLMHook.PreLLMCall returning error) abort the iteration with the error propagated (symmetric to PreToolUse deny behavior)
- Compression hook firing coexists with W1's OnAutoCompress dedup reset — both trigger on the same event; ordering: W1 first (reset dedup), then hook (user logic)

## Verification

```bash
go test -race ./internal/tools/... ./internal/agent/...
go vet ./internal/tools/... ./internal/agent/...
go build ./...
```

## PR body template

```
## Summary

- Tool arg type coercion (string↔int/bool/float) via opt-in `ArgsTarget` interface on tools; applied to read_file, bash, glob, grep
- 4 new plugin lifecycle hook stages: PreLLMCall, PostLLMCall, OnCompression, OnIterationStart
- Split interfaces keep existing Hook implementations unchanged (LLMHook/CompressionHook/IterationHook are optional extensions)
- Compression hook chained after W1's OnAutoCompress callback in runtime.go
- 12 new tests

Hermes parity items #11 and #15 (cherry-picked 4 of 10 stages; remaining stages deferred until dog-food evidence).

## Test plan

- [ ] `go test -race ./internal/tools/... ./internal/agent/...` PASS
- [ ] `go test -race ./...` full suite PASS
- [ ] Manual: run a provider mock that returns tool args with string-wrapped int; verify the tool executes without an unmarshal error
```

## Notes for the worker

- `feedback_no_stubs.md`: coercion must work or explicitly reject. No silent swallow.
- Interface-split for hooks keeps extensibility without forcing every hook to implement 10 methods. This is the Go idiomatic way (compare `io.Reader` vs `io.ReaderFrom`).
- The 4 hook stages chosen are the minimum to support (a) pre-LLM system prompt injection, (b) post-LLM token-accounting plugins, (c) compression-triggered cache invalidation (e.g., W1's dedup reset generalized to plugins), (d) per-iteration observability. If dog-food reveals we need PreToolExec/PostToolExec distinction or OnMessageAppend, add them in a follow-up PR.
- `ArgsTarget` returning `any` is slightly un-Go-idiomatic but matches the existing `Execute(params json.RawMessage)` signature variation. If cleaner to use a generic `Tool[T]` later, that's a separate refactor.
