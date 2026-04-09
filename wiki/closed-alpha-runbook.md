---
title: Closed Alpha Rehearsal Runbook
type: analysis
tags:
  - month4
  - alpha
  - rehearsal
  - verification
created: 2026-04-09T00:00:00Z
updated: 2026-04-09T00:00:00Z
confidence: high
---

# Closed Alpha Rehearsal Runbook

Use this runbook for Week 3–4 Month 4 rehearsals.

## 1. Freeze the entry gate

Before running alpha rehearsals, confirm the Month 3 checkpoint is already frozen:

- confirmatory canary bundle exists
- bugfix restoration remains above baseline on success and verification
- remaining risks are written down explicitly

## 2. Run the lane-4 verification bundle

```bash
bash scripts/run_month4_closed_alpha_checks.sh
```

This gives one reproducible pass over lint, tests, build, and telemetry script coverage.

## 3. Rehearse the operator flow

```bash
./elnath daemon start
./elnath daemon submit "analyze the repository layout"
./elnath daemon status
bash scripts/alpha_telemetry_report.sh
./elnath daemon stop
```

What to verify:

- `daemon status` renders a readable progress message, not a raw JSON envelope
- completed tasks preserve a non-empty summary
- tasks bind to session ids when the runtime creates one
- timeout counters stay visible in the telemetry summary

## 4. Capture onboarding evidence

Run the onboarding-focused packages directly so dry runs remain repeatable in CI:

```bash
go test ./internal/config ./internal/onboarding
```

These tests cover first-run defaults, path creation, config writing, i18n, and post-setup smoke-test behavior.

## 5. Record the telemetry snapshot

The telemetry reporter currently reads local SQLite state and prints:

- total / pending / running / done / failed task counts
- session-bound task counts
- completion-contract coverage
- timeout recovery counts and false-timeout rate
- recent session activity summary from conversation history

Archive one report per rehearsal alongside the operator notes.

## 6. Escalation triggers

Stay in hardening if any of the following happen:

- onboarding still requires operator rescue
- `daemon status` only shows raw progress envelopes
- timeout recovery numbers move unexpectedly without an explained cause
- rehearsals cannot produce both a completion summary and a telemetry snapshot
