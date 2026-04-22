# Scorecard Axis: `routing_trend_spearman`

Phase 7.4c landed a fifth axis on the maturity scorecard that continuously
asks "is advisor routing still improving as the real outcome stream
accumulates?" Instead of waiting for the next benchmark cycle, the axis
computes a rolling-window Spearman correlation per `(intent, workflow)`
cell, plus a decay-weighted companion rate, and flips the scorecard's
overall verdict when any eligible cell declines (`FAIL`) or stays flat
(`DEGRADED`).

## Config (v31 defaults, partner-approved)

| Field | Default | Rationale |
|---|---|---|
| `Window` | 5 | Matches current per-cell sample budget (n ≈ 15–30). W=10 needs n ≥ 50 per cell; nearly every live cell would trip `INSUFFICIENT_DATA`. |
| `MinCellSamples` | 15 | At W=5 that is 3 samples/bucket. Below this, Spearman variance overwhelms signal. |
| `MinDistinctWorkflows` | 2 | Trend requires routing choice; single-workflow intents have nothing to rank. Coverage for those lives in `axes_outcome.go`. |
| `HalfLifeDays` | 7.0 | Partner dogfood cadence is weekly. Controls the decay-weighted companion rate only — Spearman itself is unaffected. |

## Verdict rules

Per cell:

| Condition | Verdict |
|---|---|
| intent has fewer than `MinDistinctWorkflows` workflows | `INSUFFICIENT_DATA` |
| n < `MinCellSamples` | `INSUFFICIENT_DATA` |
| fewer than 3 non-empty windows | `INSUFFICIENT_DATA` |
| all window hit-rates identical | `DEGRADED` (flat) |
| Spearman coeff ≥ 0.5 | `OK` (improving) |
| Spearman coeff ≤ -0.3 | `FAIL` (declining) |
| otherwise | `DEGRADED` (flat/noisy) |

Axis verdict = worst eligible cell verdict across the stream. When no
cell is eligible, the axis reports `INSUFFICIENT_DATA` and the scorecard
adapter maps it to `ScoreNascent`.

**Score-enum mapping**: the existing `Score` enum has no `FAIL` slot.
Both `FAIL` and `DEGRADED` cells project to `ScoreDegraded`; the raw
verdicts stay in `AxisReport.Metrics["overall_verdict"]` and each
per-cell summary entry, so operators keep the `FAIL` vs `DEGRADED`
distinction when inspecting `~/.elnath/data/scorecard/YYYY-MM-DD.jsonl`.

## Decay-weighted companion rate

```
rate_decayed = Σ (success_i × w_i) / Σ w_i
w_i          = exp(-ln(2)/halfLife × age_days_i)
```

Records with future timestamps clamp to `age = 0` (defensive against
clock skew). Empty input or non-positive halfLife returns `0`. Surfaced
per cell so operators can separate "trend flat because level stays
high" (benign) from "trend flat because level stays low" (regression).

## Dogfood snapshot — 2026-04-22, 365-record partner stream

```
Overall:                        DEGRADED
Eligible / Insufficient cells:  3 / 7

intent          workflow           n    verdict              coeff   decay
--------------  ---------------  ---  -------------------  ------  -----
<empty>         <empty>            7  INSUFFICIENT_DATA    +0.000  0.000
chat            chat_direct       34  DEGRADED             +0.447  0.946
chat            single            26  DEGRADED             +0.000  1.000
complex_task    single            15  DEGRADED             +0.000  1.000
complex_task    team               5  INSUFFICIENT_DATA    +0.000  0.000
project         autopilot          6  INSUFFICIENT_DATA    +0.000  0.000
question        single           139  INSUFFICIENT_DATA    +0.000  0.000
simple_task     single           101  INSUFFICIENT_DATA    +0.000  0.000
unclear         single             4  INSUFFICIENT_DATA    +0.000  0.000
wiki_query      single            28  INSUFFICIENT_DATA    +0.000  0.000
```

### Observations

- **Decay is doing real work**: eligible cells show `decay ∈ [0.946,
  1.000]`, correctly weighting recent records. Identical raw rates and
  decayed rates on `chat/single` and `complex_task/single` reflect
  uninterrupted success streaks.
- **Flat + high-level cells surface as `DEGRADED`**: `chat/single` and
  `complex_task/single` sit at 100% success → Spearman is flat →
  `DEGRADED`. `decay = 1.000` signals the level is perfect, so
  operators should treat `DEGRADED + decay ≥ 0.9` as benign. Phase 4
  alert logic honors this (alert on `FAIL`, or `DEGRADED` with
  `decay < 0.5`).
- **Borderline improving**: `chat/chat_direct` coeff = +0.447 is just
  below the 0.5 `OK` cut. `decay = 0.946` confirms the direction is
  healthy. Threshold held.
- **Diversity gate filters single-workflow intents**: `question`
  (n = 139), `simple_task` (n = 101), `wiki_query` (n = 28) gate to
  `INSUFFICIENT_DATA` despite high n. This is the intended behavior —
  the axis detects trend; coverage lives in `axes_outcome.go`.
- **Sparse workflow correctly gated**: `complex_task/team` at n = 5
  stays below `MinCellSamples = 15`. Matches the Phase 7.4b gate
  observation that `team` needs more adoption before its trend is
  readable.
- **Data quality observed, not crashed**: 7 records with empty
  `intent`/`workflow` collapsed into the `<empty>/<empty>` cell and
  gated out on diversity. The axis surfaces the count without
  crashing — those records deserve a separate audit.

### Comparison to Phase 7.4b gate-decision

7.4b's historic benchmarks recorded `complex_task→single` at 100% and
`complex_task→team` at 20%. With the live axis, `complex_task/single`
is the only `complex_task` cell that meets the eligibility gate today;
`team` stays quiet until adoption grows. The 7.4b decision to skip
synthetic fixture scaffolding in favor of the live axis holds: any
regression from here will show up in real time.

## Tuning verdict

**Defaults held.** No knob adjustments recommended from this run.

- `Window = 5` / `MinCellSamples = 15` correctly separates signal from
  noise at current stream density.
- `HalfLifeDays = 7` puts recent bias where partner cadence demands it.
- `MinDistinctWorkflows = 2` keeps trend-axis scope focused.

Re-tune when:

- eligible cell count falls below 2 (sample attrition);
- cascade `DEGRADED` flips appear across unrelated intents (broader
  regression pattern);
- `chat/chat_direct` stays in `[0.4, 0.5)` for three consecutive
  scorecard runs (consider lowering `OK` threshold to 0.4).

## Phase 4 (alert hook) readiness notes

The current landscape is noisy at the `DEGRADED`-flat end — three
cells flat at high success rates. A naive "alert on DEGRADED" would
fire on benign steady-state traffic. When Phase 4 is revisited, the
alert gate should require:

- `FAIL` verdict entry, **or**
- `DEGRADED` with `decay < 0.5` (real level drop, not just a flat
  success ceiling).

Global cooldown 60 min (partner OQ#3). Surface: wiki append only at
`~/.elnath/wiki/logs/routing_trend_alerts.md` (partner OQ#5). Telegram
integration stays deferred until partner sees alert cadence in
practice.

## References

- Axis core: `internal/scorecard/axes_routing_trend.go`
- Tests: `internal/scorecard/axes_routing_trend_test.go`
- Corpus snapshot: `internal/scorecard/testdata/trend_corpus.jsonl`
- Spearman helper: `internal/eval/spearman.go`
- Scorecard CLI: `cmd/elnath/cmd_debug_scorecard.go`
