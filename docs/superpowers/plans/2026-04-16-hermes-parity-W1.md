# W1: Hermes Parity — #8 3-layer tool result control + #13 jitter backoff

**Target**: `fix/hermes-parity-w1`
**Estimated LOC**: 150-250 (code) + ~150 (tests)
**Estimated time**: 3-5 hours (opencode)
**Depends on**: nothing
**Conflicts with**: nothing (this branch owns `internal/agent/agent.go` exclusively; other parity workers MUST NOT touch agent.go)

## Context (read first)

Two Hermes audit items, both living in `internal/agent/agent.go`.

Current code already has **two of three** tool-result size layers implemented:

- **Layer 1 (per-tool truncation)**: `toolResultPerToolLimit = 50_000` at `internal/agent/agent.go:602`. Any single tool result over 50K chars gets truncated to 2K + a marker. ✅ done.
- **Layer 2 (per-turn total budget)**: `toolResultTotalLimit = 200_000` at `internal/agent/agent.go:603`. Even if every individual result is under 50K, the aggregate across the turn is capped by picking the largest and truncating it repeatedly until total <= 200K. ✅ done.
- **Layer 3 (per-result persistence over history)**: NOT implemented. Hermes description is terse; this layer appears to progressively attenuate *older* tool results across multiple turns so long sessions don't accumulate un-truncated history.

Retry backoff at `internal/agent/agent.go:252-267`:
```go
delay := retryBaseDelay      // time.Second
...
case <-time.After(delay):
...
delay *= 2
```
Deterministic 2x multiplier. When the gate suite runs 3 times in parallel with the same retry pattern, all 3 retries hit the provider at the same second, amplifying any transient overload.

## W1 scope

### Task A — Layer 3 progressive history attenuation (bulk of the work)

**Goal**: In long sessions, tool results in messages older than the current turn should be progressively truncated on every `Run` iteration. Newest-turn results keep 50K per-tool limit; older turns drop to 10K, then 2K, then a marker-only stub.

**Design**:
- Extend `truncateToolResults(messages)` at `internal/agent/agent.go:611` or add a sibling helper
- Introduce an `attenuateHistoricalToolResults(messages []llm.Message, currentTurnIdx int)` helper
- Attenuation schedule (apply per turn, where "turn" = one assistant message + its tool results in the next user message):
  - `turnsAgo <= 1` — keep layer 1/2 existing limits
  - `turnsAgo == 2` — cap each tool result at 10_000 chars
  - `turnsAgo == 3` — cap each tool result at 2_000 chars
  - `turnsAgo >= 4` — replace content with `"[stale tool result, turns=N, original=<bytes>]"` placeholder only
- Run after `truncateToolResults` in the main Run loop (`internal/agent/agent.go:222`)

**Constants**:
```go
const (
    toolResultHistoryStage1Limit = 10_000  // turnsAgo == 2
    toolResultHistoryStage2Limit = 2_000   // turnsAgo == 3
    // turnsAgo >= 4: placeholder only
)
```

**Truncation marker** must be recognizable so future iterations don't re-truncate an already-attenuated result:
```go
const attenuationMarker = "[attenuated/"  // prefix test for skip
```

**Tests required** (add to `internal/agent/agent_test.go`):
1. `TestAttenuateHistoricalToolResults_NewTurnUnaffected` — current-turn results untouched
2. `TestAttenuateHistoricalToolResults_TwoTurnsAgoLimit10K` — 30K result in t-2 truncated to 10K with marker
3. `TestAttenuateHistoricalToolResults_ThreeTurnsAgoLimit2K` — 10K result in t-3 truncated to 2K with marker
4. `TestAttenuateHistoricalToolResults_FourPlusTurnsAgoPlaceholder` — any result in t-4+ becomes placeholder
5. `TestAttenuateHistoricalToolResults_Idempotent` — running twice doesn't re-truncate (marker detected)

### Task B — Decorrelated jitter backoff

**Goal**: Replace `delay *= 2` with decorrelated-jitter formula (AWS Architecture Blog pattern, also used in Hermes).

**Formula**:
```
delay = min(maxDelay, random_between(baseDelay, delay * 3))
```

where `baseDelay = retryBaseDelay = time.Second` and `maxDelay = 30 * time.Second`.

**Implementation**:
- Replace `delay *= 2` at `internal/agent/agent.go:267`
- New helper `nextJitterDelay(current time.Duration) time.Duration`
- Use `math/rand/v2` (Go 1.25+). Package-level `*rand.Rand` seeded once with `rand.NewPCG(uint64(time.Now().UnixNano()), 0)` to avoid data races (each agent or package-global mutex-protected source).
- `maxDelay` const: `retryMaxDelay = 30 * time.Second`

**Tests required**:
1. `TestNextJitterDelay_MinimumRespected` — always >= baseDelay
2. `TestNextJitterDelay_MaximumCapped` — always <= maxDelay
3. `TestNextJitterDelay_Distribution` — 1000 samples, mean should be meaningfully > deterministic 2x (sanity)
4. `TestNextJitterDelay_Reproducible` — with a seeded RNG, output is reproducible (needed for regression testing)

## Files touched

- `internal/agent/agent.go` — modify `truncateToolResults` callsite area (add `attenuateHistoricalToolResults`); replace `delay *= 2`; add `nextJitterDelay` helper + constants
- `internal/agent/agent_test.go` — new tests per above

**DO NOT TOUCH** (other workers own these):
- `internal/conversation/*` (W2 territory)
- `internal/tools/*` (W3 territory)
- `internal/agent/hooks.go` (W3 territory)
- `cmd/elnath/*` (no CLI changes for W1)

## Behavior invariants

- `truncateToolResults` behavior unchanged for single-turn cases
- Attenuation never grows content; only shortens
- Attenuation is idempotent across iterations
- Jitter backoff respects `ctx.Done()` the same way deterministic delay does
- No test flakes from RNG (seed or bound assertions generously)

## Verification (before opening PR)

```bash
go test -race ./internal/agent/...
go vet ./internal/agent/...
go build ./...
```

All must pass. The test count should grow by 9 (5 attenuation + 4 jitter).

## PR body template

```
## Summary

- Task A: Layer 3 progressive attenuation of historical tool results (turnsAgo=2 → 10K, =3 → 2K, >=4 → placeholder)
- Task B: Decorrelated-jitter retry backoff replacing deterministic 2x
- 9 new tests covering attenuation schedule and jitter bounds

Hermes parity items #8 (now 3-layer complete) and #13.

## Test plan

- [ ] `go test -race ./internal/agent/...` PASS
- [ ] `go vet ./...` PASS
- [ ] Full suite still green: `go test -race ./...`
- [ ] No flake: attenuation tests use fixed message arrays; jitter tests use seeded RNG or generous bounds
```

## Notes for the worker

- `feedback_no_stubs.md`: no placeholder logic that "works in tests only". Attenuation must actually shrink `tr.Content`.
- `feedback_baseline_recovery_scope.md`: do not alter any existing truncation constants (`50_000`, `200_000`) or change when `truncateToolResults` is called. Only add the attenuation pass.
- When unsure about message-turn segmentation (how to count "turnsAgo"), treat one assistant message + the immediately-following user message (containing tool results) as a single turn. Walk backward from the end of the message slice.
