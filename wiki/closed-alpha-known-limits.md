---
title: Closed Alpha Known Limits
type: analysis
tags:
  - month4
  - alpha
  - limits
  - telegram
created: 2026-04-09T00:00:00Z
updated: 2026-04-09T00:00:00Z
confidence: high
---

# Closed Alpha Known Limits

These are intentional Month 4 constraints, not launch surprises.

## Product-surface limits

- CLI remains the primary operator surface.
- Telegram must stay a thin companion shell for status, approvals, completion notifications, and resume/follow-up triggers only.
- Broad conversational companion behavior is explicitly out of scope for closed alpha.
- Hermes-style pairing and richer Telegram adapter orchestration are intentionally deferred while Elnath remains single-chat and operator-only.

## Telemetry limits

- The lane-4 telemetry reporter summarizes local SQLite state and can archive JSON snapshots, but it is not a hosted dashboard.
- Repeat-use is currently approximated from persisted conversation sessions and recent activity windows.
- Approval counts and queued continuation/follow-up counts are now visible in the local telemetry report, but they are still local SQLite summaries rather than hosted product analytics.
- Latest-session resume selection is now hardened across JSONL transcripts and SQLite-backed history metadata, but resume-success telemetry still depends on the continuity-runtime workstream emitting durable session/task state consistently.

## Rehearsal limits

- Live daemon/task smoke tests still require a configured model provider.
- `scripts/run_month4_closed_alpha_checks.sh` verifies build/test/lint and telemetry coverage, but it does not replace a real operator rehearsal with a configured runtime.
- Telegram polling conflicts are now surfaced as explicit operator errors; Elnath does not try to coordinate multiple shells for the same bot token automatically.
- Historical rehearsal artifacts may mention the older raw planner/subtask-JSON failure class; treat those notes as date-scoped evidence, not as the expected current operator experience after hardening.
- The alpha gate should remain closed if rehearsals need bespoke source edits or manual DB inspection.
