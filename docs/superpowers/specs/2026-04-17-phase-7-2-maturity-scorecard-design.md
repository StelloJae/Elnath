# Phase 7.2 Maturity Scorecard — Design

**Date**: 2026-04-17
**Status**: Design (pre-plan)
**Author**: Partner session (Stage B post-landing)
**Supersedes**: n/a (new document)

---

## Purpose

Provide systematic, reproducible evidence for the Phase 7 claim — **"시간이 갈수록 더 똑똑해진다"** — by measuring four maturity axes at a point in time and emitting structured data that future tooling can consume.

Phase 7.2 sits between Stage A/B (which built the gears) and Phase 7.3 (benchmark v2). The purpose of 7.2 is not to improve the flywheel but to **observe it honestly**.

---

## Framing

> "Scorecard는 1회성 스냅샷이 아니라, Elnath가 자기 자신을 들여다보는 첫 거울."

Three design consequences follow:

1. **Machine-readable first, human-readable second.** JSON is source of truth; Markdown is derived.
2. **Append-only history from day one.** Every run is recorded; comparisons across time are possible later.
3. **No self-consumption yet (YAGNI).** The RoutingAdvisor reading its own scorecard is a Phase 7.3+ concern. The schema must allow it, but we do not build it now.

---

## Goals

- Define four maturity axes with reproducible metrics.
- Emit a JSON record per run to `~/.elnath/data/scorecard/YYYY-MM-DD.jsonl` (append-only).
- Emit a Markdown report to stdout.
- Ship `elnath debug scorecard` CLI.
- Run once in-session today to produce **baseline v1**.

## Non-goals

- Self-consumption: feeding scorecard back into RoutingAdvisor or consolidation scheduler (Phase 7.3+).
- Quality judgment: whether synthesis pages contain *good* knowledge (requires human feedback input).
- Historical backfill: reconstructing what the score would have been last week.
- Cross-project aggregation: the tool reads the local Elnath install only.

---

## The Four Axes

Each axis produces: a `score` (OK / NASCENT / DEGRADED / UNKNOWN), a `metrics` object (raw numbers), and a `reason` (one-line explanation of the score).

### Axis 1 — `routing_adaptation`

**Question**: Does RoutingAdvisor actually use past outcomes to influence future routing?

**Source**: `~/.elnath/data/outcomes.jsonl`

**Metrics**:
- `outcomes_total` — total record count
- `preference_used_count` — records where `preference_used=true`
- `preference_used_pct` — `preference_used_count / outcomes_total`
- `success_rate` — overall `success=true` ratio
- `trend` — success-rate delta between the second half and first half of records, records sorted ascending by `timestamp`, split at `len/2` (float in `[-1.0, +1.0]`; `null` when `outcomes_total < 10`)

**Score rules**:
- `UNKNOWN` — file missing
- `NASCENT` — `outcomes_total < 10` (insufficient sample)
- `OK` — `outcomes_total ≥ 10` AND `preference_used_count ≥ 1` AND `trend ≥ -0.10` (not strongly regressing)
- `DEGRADED` — `outcomes_total ≥ 10` AND (`preference_used_count == 0` OR `trend < -0.10`)

---

### Axis 2 — `outcome_recording`

**Question**: Is outcome recording actually happening across code paths and days?

**Source**: `~/.elnath/data/outcomes.jsonl`

**Metrics**:
- `outcomes_total`
- `success_count`, `error_count` — records with `success=true` vs `false`
- `distinct_days_last_7` — distinct calendar days (local timezone) with at least one record in the 7-day window `[now - 7d, now]`
- `last_record_at` — most recent timestamp

**Score rules**:
- `UNKNOWN` — file missing
- `NASCENT` — `outcomes_total < 5`
- `OK` — `distinct_days_last_7 ≥ 3` AND `error_count ≥ 1` (both success and error paths record)
- `DEGRADED` — `outcomes_total ≥ 5` AND (`distinct_days_last_7 < 3` OR `error_count == 0`)

Note: `error_count ≥ 1` guards against the F2 follow-up (ChatResponder path). If Stage C lands, this will naturally trend upward.

---

### Axis 3 — `lesson_extraction`

**Question**: Are lessons being extracted from outcomes and cycling through supersession?

**Source**: `~/.elnath/data/lessons.jsonl`

**Metrics**:
- `lessons_total`
- `lessons_active` — records without `superseded_by`
- `lessons_superseded` — records with `superseded_by`
- `supersession_ratio` — `lessons_superseded / lessons_total`

**Score rules**:
- `UNKNOWN` — file missing
- `NASCENT` — `lessons_total < 5`
- `OK` — `lessons_total ≥ 5` AND `lessons_superseded ≥ 1` (at least one cycle happened)
- `DEGRADED` — `lessons_total ≥ 5` AND `lessons_superseded == 0` (lessons accumulate without synthesis)

---

### Axis 4 — `synthesis_compounding`

**Question**: Is consolidation actually producing compounding synthesis pages?

**Sources**:
- `~/.elnath/wiki/synthesis/**/*.md` (file count + mtime)
- `~/.elnath/data/consolidation_state.json` (run/success counters)

**Metrics**:
- `synthesis_count` — total `.md` files under synthesis/
- `run_count`, `success_count` — from state.json
- `last_success_at` — from state.json
- `supersession_ratio` — same value as Axis 3 (`lessons_superseded / lessons_total`); replicated here for axis self-containment

**Score rules**:
- `UNKNOWN` — `consolidation_state.json` missing
- `NASCENT` — `run_count == 0`
- `OK` — `synthesis_count ≥ 1` AND `success_count ≥ 1` AND `supersession_ratio > 0`
- `DEGRADED` — `run_count ≥ 1` AND (`success_count == 0` OR `synthesis_count == 0`)

---

## Overall Score

The overall score is a **simple aggregation** (not a weighted formula):
- If any axis is `DEGRADED` → overall `DEGRADED`
- Else if all four axes are `OK` → overall `OK`
- Else if any axis is `UNKNOWN` → overall `UNKNOWN`
- Else → `NASCENT`

Rationale: a single degraded axis is a signal the flywheel regressed; silence about it would hide the signal. Mixed OK/NASCENT is the honest "in progress" state.

---

## Score Semantics

- **`OK`** — the axis is functioning as designed within the current data range. Not a quality claim about knowledge; only that gears turned.
- **`NASCENT`** — sample too small to judge. Not a failure. Expected at project start.
- **`DEGRADED`** — previously working signals absent or regressing. Investigate.
- **`UNKNOWN`** — required data source missing. Infrastructure gap, not a score about behavior.

Thresholds (10, 5, 7 days, etc.) are **starting values**. They will be tuned after 2–3 runs when the trend shape is visible.

---

## Output

### JSON — source of truth

**Location**: `~/.elnath/data/scorecard/2026-04-17.jsonl`
**Mode**: append-only; one file per calendar day; multiple runs in a day append to the same file.

**Why JSONL per day, not one global file**: daily files keep each day self-contained (easy to diff, easy to prune); multiple runs per day (manual + future scheduled) are first-class supported.

**Schema v1.0**:

```json
{
  "timestamp": "2026-04-17T08:15:00+09:00",
  "schema_version": "1.0",
  "elnath_version": "0.6.0",
  "overall": "NASCENT",
  "axes": {
    "routing_adaptation": {
      "score": "NASCENT",
      "metrics": {
        "outcomes_total": 2,
        "preference_used_count": 0,
        "preference_used_pct": 0.0,
        "success_rate": 1.0,
        "trend": null
      },
      "reason": "only 2 outcomes; need ≥10 for trend"
    },
    "outcome_recording": {
      "score": "NASCENT",
      "metrics": {
        "outcomes_total": 2,
        "success_count": 2,
        "error_count": 0,
        "distinct_days_last_7": 1,
        "last_record_at": "2026-04-16T21:12:40Z"
      },
      "reason": "outcomes_total < 5"
    },
    "lesson_extraction": {
      "score": "OK",
      "metrics": {
        "lessons_total": 12,
        "lessons_active": 2,
        "lessons_superseded": 10,
        "supersession_ratio": 0.833
      },
      "reason": "12 lessons, 10 superseded"
    },
    "synthesis_compounding": {
      "score": "OK",
      "metrics": {
        "synthesis_count": 2,
        "run_count": 1,
        "success_count": 1,
        "last_success_at": "2026-04-17T07:01:28+09:00",
        "supersession_ratio": 0.833
      },
      "reason": "2 syntheses, 1 successful run, supersession 83%"
    }
  },
  "sources": {
    "outcomes_path": "/Users/stello/.elnath/data/outcomes.jsonl",
    "lessons_path": "/Users/stello/.elnath/data/lessons.jsonl",
    "synthesis_dir": "/Users/stello/.elnath/wiki/synthesis",
    "state_path": "/Users/stello/.elnath/data/consolidation_state.json"
  }
}
```

### Markdown — human report

Printed to stdout after JSON is appended. Derived from the JSON above — **no independent computation**.

Example:

```
Maturity Scorecard — 2026-04-17 08:15

  Overall:                NASCENT

  routing_adaptation      NASCENT   only 2 outcomes; need ≥10 for trend
  outcome_recording       NASCENT   outcomes_total < 5
  lesson_extraction       OK        12 lessons, 10 superseded
  synthesis_compounding   OK        2 syntheses, 1 successful run, supersession 83%

  Raw metrics:
    outcomes:     2 total (2 success, 0 error, 1 day in last 7)
    lessons:      12 total (2 active, 10 superseded)
    synthesis:    2 pages, run_count=1, success_count=1

  Next run will append to: /Users/stello/.elnath/data/scorecard/2026-04-17.jsonl
```

---

## CLI

### Commands

- `elnath debug scorecard` — run + append JSON + print Markdown (default)
- `elnath debug scorecard --json` — run + append JSON + print JSON (no Markdown)

Exit code: `0` always. Scorecard is a **reporting tool, not a gate**. Even `DEGRADED` returns 0; caller inspects the JSON if programmatic use.

### Deferred (future stages)

- `--show` — print last entry without running
- `--diff` — show delta between last two entries
- `elnath debug scorecard history` — list all entries across files

These are natural follow-ups but not required for the baseline.

---

## Package Layout

### New: `internal/scorecard/`

```
internal/scorecard/
├── scorecard.go           # Report, Score, Axis interface, Compute()
├── axes_routing.go        # RoutingAdaptationAxis
├── axes_outcome.go        # OutcomeRecordingAxis
├── axes_lesson.go         # LessonExtractionAxis
├── axes_synthesis.go      # SynthesisCompoundingAxis
├── markdown.go            # Markdown rendering from Report
└── scorecard_test.go      # table-driven per-axis + end-to-end
```

### New: `cmd/elnath/scorecard.go`

CLI dispatcher matching the existing `debug consolidation` pattern.

### No changes to

- Daemon code (no scheduling, no shared state)
- Outcome/lesson/wiki writers (read-only consumer)
- Existing CLI commands

---

## Implementation Phases

Implementation will be broken into phases in the subsequent plan doc, but the expected shape:

1. **Package skeleton + Report struct + Axis interface** (30 min)
2. **Each axis implementation + unit tests** (one file per axis, ~45 min each) — can parallelize
3. **Markdown renderer + golden test** (20 min)
4. **JSON append + day-file handling** (20 min)
5. **CLI wiring + integration test** (30 min)
6. **Live probe: run once, eyeball match, commit JSON entry as baseline** (15 min)

Total estimate: one focused session, ~4 hours.

---

## Testing Strategy

### Unit (per axis)

Table-driven tests over fixture data. Each table row: `{name, fixture, expected_score, expected_metrics}`. Fixtures are small inline strings, not external files.

### Integration

One `TestScorecardEndToEnd` that:
- Sets up tmpdir with fixture `outcomes.jsonl`, `lessons.jsonl`, `synthesis/*.md`, `consolidation_state.json`
- Runs `scorecard.Compute()`
- Asserts Report matches expected JSON (golden file)
- Runs Markdown renderer, asserts contains expected lines

### Live probe

Single manual run:
```
./elnath debug scorecard
```
Verify:
1. New file exists: `~/.elnath/data/scorecard/2026-04-17.jsonl`
2. JSON parses
3. Overall == `NASCENT` (given current data)
4. Axes 1-2 are `NASCENT`, axes 3-4 are `OK`
5. Markdown output mirrors JSON

Live probe is the real acceptance gate, per the project principle: **Real-data live probe is the true evidence; unit tests verify wiring only.**

---

## Baseline Expectation (2026-04-17)

Given current data state:

| Axis | Expected Score | Reason |
|---|---|---|
| routing_adaptation | `NASCENT` | 2 outcomes < 10 |
| outcome_recording | `NASCENT` | 2 outcomes < 5 |
| lesson_extraction | `OK` | 12 lessons, 10 superseded |
| synthesis_compounding | `OK` | 2 syntheses, run_count=1 |
| **overall** | `NASCENT` | mixed OK/NASCENT |

This is the **honest baseline**. Gears are turning (axes 3, 4) but data volume is small (axes 1, 2). The scorecard will correctly reflect that synthesis is compounding ahead of routing feedback — which matches what actually happened in Stage A vs B.

Stage C will improve Axis 2 (ChatResponder outcome recording → `error_count` growth, more distinct days). Natural dog-food traffic will slowly improve Axis 1.

---

## Open Questions / Thresholds to Revisit

These are **explicitly left to future iterations**:

1. **Trend window size** — currently "first half vs second half of all records". A sliding 7-day window may be better once volume grows.
2. **`NASCENT` threshold for Axis 1** — 10 records. Arbitrary; tune after seeing real distribution.
3. **Success rate regression threshold** — `-10%`. May need domain-specific tuning.
4. **Orphan synthesis detection** — a synthesis page with zero superseding lessons could be a smell. Not captured in v1.
5. **Weighted overall score** — v1 is simple aggregation. A weighted formula (e.g., Axis 4 weighted higher) could be useful once trend data exists.

---

## Success Criteria

- [ ] `elnath debug scorecard` command builds and runs without error
- [ ] JSON file created at correct location, valid schema v1.0
- [ ] Markdown report prints and matches JSON
- [ ] All unit tests pass with `-race`
- [ ] One live probe run today → baseline v1 committed alongside code
- [ ] `lint` clean (no new staticcheck issues)

---

## Follow-ups (non-blocking)

- **FU-A**: `--show` / `--diff` / `history` subcommands (after 2-3 runs exist)
- **FU-B**: Cross-reference with `consolidation_state.json` history (once Stage C lands history.jsonl)
- **FU-C**: Scorecard consumed by RoutingAdvisor for self-healing (Phase 7.3+)
- **FU-D**: Quality axis requiring human signal (override/undo count)

---

## Rationale Summary

- **Why not a one-time doc?** Dead artifact; violates LLM Wiki "compounding knowledge" ethos.
- **Why not a full self-consumption loop now?** Scope explosion; YAGNI until trend data exists.
- **Why JSONL per-day, not a single global file?** Daily isolation; multiple runs per day are first-class.
- **Why a simple aggregation for overall?** Weighted formula requires empirical calibration we don't have yet; honest simplicity beats premature optimization.
- **Why `NASCENT` instead of `PENDING` or `INSUFFICIENT`?** Captures the "early state, not failed" semantics in one word; aligned with ambient autonomy vocabulary.
