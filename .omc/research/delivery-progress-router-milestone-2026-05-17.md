# Delivery Progress Router Milestone

Date: 2026-05-17 KST

Branch: `codex/delivery-progress-router`

Status: locally verified milestone

## Goal

Close a small part of the Codex-Claude-Hermes convergence gap around
user-visible async progress delivery.

The prior state had:

- daemon queue progress storage;
- a `ProgressObserver` hook;
- Telegram progress rendering;
- `DeliveryRouter` for task completion only.

The missing structure was that live progress bypassed the delivery router while
completion notifications used it. This made the router less suitable as the
future gateway/delivery abstraction described in the convergence gap map.

## References Inspected

Elnath:

- `internal/daemon/delivery.go`
- `internal/daemon/delivery_test.go`
- `internal/daemon/daemon.go`
- `internal/daemon/progress.go`
- `internal/telegram/sink.go`
- `cmd/elnath/cmd_daemon.go`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`

Hermes:

- `/Users/stello/.hermes/hermes-agent/gateway/delivery.py`
- `/Users/stello/.hermes/hermes-agent/gateway/display_config.py`

Claude Code:

- `/Users/stello/claude-code-src/src/types/hooks.ts`
- `/Users/stello/claude-code-src/src/remote/sdkMessageAdapter.ts`

## Change

Added Elnath-native progress delivery support:

- `daemon.TaskProgress`
- `daemon.ProgressSink`
- `DeliveryRouter.RegisterProgress`
- `DeliveryRouter.DeliverProgress`
- `DeliveryRouter.OnProgress`

Updated wiring:

- `DeliveryRouter` now implements `daemon.ProgressObserver`.
- `Register` automatically enrolls sinks that implement both
  `CompletionSink` and `ProgressSink`.
- Telegram sink now implements `NotifyProgress`.
- daemon Telegram wiring now routes progress through `DeliveryRouter` instead
  of directly through `TelegramSink`.

Completion behavior remains unchanged:

- completion delivery dedup remains backed by `task_completion_deliveries`;
- completion sink partial/all-failure behavior remains unchanged.

Progress behavior:

- progress events are not deduplicated because every event is part of the live
  stream;
- progress sink failures are logged;
- `DeliverProgress` returns an error only when every registered progress sink
  fails, matching completion-delivery failure semantics.

## Verification

Focused TDD proof:

```text
go test ./internal/daemon -run 'TestDeliveryRouter_DeliverProgressRoutesRegisteredProgressSink|TestDeliveryRouter_OnProgressParsesAndRoutes|TestDeliveryRouter_DeliverProgressAllSinksFail' -count=1
```

Result: PASS.

Focused regression proof:

```text
go test ./internal/daemon -run 'TestDeliveryRouter_DeliverProgressRoutesRegisteredProgressSink|TestDeliveryRouter_OnProgressParsesAndRoutes|TestDeliveryRouter_DeliverProgressAllSinksFail|TestDeliverDedupSameTaskSameSink|TestDeliveryRouter_MixedSinks' -count=1
go test ./internal/telegram -run 'TestSinkOnProgressRoutesToProgressReporter|TestSinkOnProgressSummaryRoutesToStream|TestSinkNotifyCompletionEditsProgressMessage' -count=1
go test ./cmd/elnath -run 'TestProgressObserverDispatchesRepresentativeEventTypesAndIgnoresUnknown|TestDaemonTaskRunnerCreatesSessionAndUsesClassifier' -count=1
```

Result: PASS.

Package proof:

```text
go test ./internal/daemon -count=1
go test ./internal/telegram -count=1
go test ./cmd/elnath -count=1
```

Result: PASS.

Static checks:

```text
go vet ./internal/daemon ./internal/telegram ./cmd/elnath
git diff --check
```

Result: PASS.

## Claim Boundary

Allowed:

- Elnath daemon progress can now flow through the delivery router.
- Telegram daemon progress delivery now uses the same router layer as completion
  delivery.
- The change is locally verified for daemon, Telegram, and CLI daemon wiring.

Not claimed:

- full multi-platform Hermes-style gateway parity;
- native button UX;
- durable per-progress delivery receipt table;
- benchmark success;
- Codex/Claude/Hermes superiority;
- full v8 benchmark readiness.

## Corpus / Baseline / Benchmark

- Benchmark run: no
- Full v8: no
- Baseline: no
- Codex comparison: no
- Claude comparison: no
- Corpus mutation: no
- Baseline mutation: no

## Remaining Risk

- Progress delivery is routed but not durably deduplicated or individually
  receipted. This is intentional for live progress events.
- Non-Telegram platform adapters are not added in this milestone.
- Delivery target/origin/home-channel modeling remains a later gateway-router
  milestone.

## Next Recommendation

Continue product/runtime completion with the next structural blocker:

1. Gateway delivery target model:
   - origin
   - home channel
   - explicit target
   - local fallback
2. Or session handoff/resume recap if the user-visible continuity gap is more
   urgent.

Do not return to benchmark loops from this milestone.
