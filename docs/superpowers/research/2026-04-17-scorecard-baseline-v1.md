# Scorecard Baseline v1 — 2026-04-17

First run of `elnath debug scorecard` against real data, captured right after
Stage A (outcome recording) and Stage B (lesson consolidation) landed on
main. Purpose: establish the reference point against which every future
scorecard is compared when making claims about "시간이 갈수록 더 똑똑해진다".

## Summary

| Axis | Score | Key metric |
|---|---|---|
| routing_adaptation | `NASCENT` | 2 outcomes; need ≥10 for trend |
| outcome_recording | `NASCENT` | outcomes_total=2 < 5, error_count=0 |
| lesson_extraction | `OK` | 12 lessons, 10 superseded (ratio 0.83) |
| synthesis_compounding | `OK` | 2 syntheses, 1 successful run (ratio 0.83) |
| **overall** | **`NASCENT`** | mixed OK/NASCENT — honest early state |

This matches the prediction written into the design spec: gears are turning
on the compounding side (lessons → synthesis) but routing/outcome volume is
still too small to judge adaptation. Stage C (ChatResponder outcome
recording, F2) and natural dog-food traffic will shift axes 1–2 upward.

## Snapshot (first JSON line of `~/.elnath/data/scorecard/2026-04-17.jsonl`)

```json
{
    "timestamp": "2026-04-17T08:09:08.555456+09:00",
    "schema_version": "1.0",
    "elnath_version": "0.6.0",
    "overall": "NASCENT",
    "axes": {
        "routing_adaptation": {
            "score": "NASCENT",
            "metrics": {
                "outcomes_total": 2,
                "preference_used_count": 0,
                "preference_used_pct": 0,
                "success_rate": 1,
                "trend": null
            },
            "reason": "2 outcomes; need >=10 for trend"
        },
        "outcome_recording": {
            "score": "NASCENT",
            "metrics": {
                "distinct_days_last_7": 1,
                "error_count": 0,
                "last_record_at": "2026-04-16T21:12:40.677991Z",
                "outcomes_total": 2,
                "success_count": 2
            },
            "reason": "outcomes_total=2 < 5"
        },
        "lesson_extraction": {
            "score": "OK",
            "metrics": {
                "lessons_active": 2,
                "lessons_superseded": 10,
                "lessons_total": 12,
                "supersession_ratio": 0.8333333333333334
            },
            "reason": "12 lessons, 10 superseded (ratio=0.83)"
        },
        "synthesis_compounding": {
            "score": "OK",
            "metrics": {
                "last_success_at": "2026-04-17T07:01:28.051639+09:00",
                "run_count": 1,
                "success_count": 1,
                "supersession_ratio": 0.8333333333333334,
                "synthesis_count": 2
            },
            "reason": "2 syntheses, 1 successful run(s), supersession=0.83"
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

## Cross-check (against `elnath debug consolidation show`)

| Field | Scorecard | consolidation show | Match |
|---|---|---|---|
| Lessons active | 2 | 2 | ✓ |
| Lessons superseded | 10 | 10 | ✓ |
| Synthesis pages | 2 | 2 | ✓ |
| Run count | 1 | 1 | ✓ |
| Success count | 1 | 1 | ✓ |

Two independent code paths (scorecard and consolidation show) read the
same underlying stores and agree. Confidence: high.

## What's expected to change

- **Axis 1 (routing_adaptation)** shifts to `OK` once outcomes.jsonl crosses
  10 records AND at least one RoutingAdvisor preference fires. Expected on
  natural dog-food cadence over days, not a code change.
- **Axis 2 (outcome_recording)** shifts to `OK` after Stage C lands (F2:
  ChatResponder writes error outcomes) AND ≥3 distinct days accumulate.
- **Axis 3 (lesson_extraction)** and **Axis 4 (synthesis_compounding)**
  should hold `OK` across future runs unless consolidation regresses.

Re-run after 2026-04-18 04:05 (first scheduled daily fire) to see the
second baseline row.
