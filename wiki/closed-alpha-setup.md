---
title: Closed Alpha Setup Guide
type: source
tags:
  - month4
  - alpha
  - onboarding
  - runtime
created: 2026-04-09T00:00:00Z
updated: 2026-04-09T00:00:00Z
confidence: high
---

# Closed Alpha Setup Guide

This guide is the lane-4 operator path for Month 4 closed-alpha rehearsals.

## Goal

A technical alpha user should be able to go from a clean machine to a first successful CLI task without bespoke hand-holding.

This onboarding path is intentionally narrow: it proves the existing CLI and thin Telegram operator flow, not a broader Telegram product surface. Hardening work here should reduce operator friction without expanding features.

## Prerequisites

- Go 1.25+
- One configured provider key (`ELNATH_ANTHROPIC_API_KEY`, `ELNATH_OPENAI_API_KEY`, or Codex OAuth)
- Writable data directory (default: `~/.elnath/data`)
- Writable wiki directory (default: `~/.elnath/wiki`)

## Install and verify

```bash
make build
./elnath version
make lint
make test
```

If you want the full Month 4 bundle in one command:

```bash
bash scripts/run_month4_closed_alpha_checks.sh --report-out artifacts/month4-alpha-report.json
```

## First-run onboarding path

Interactive terminals automatically enter the Bubble Tea onboarding flow when no config exists.

```bash
rm -f ~/.elnath/config.yaml
./elnath run
```

Non-interactive environments can still exercise the underlying onboarding defaults through tests:

```bash
go test ./internal/config ./internal/onboarding
```

## First successful task path

1. Build the binary.
2. Start the daemon.
3. Submit one small task.
4. Confirm `daemon status` shows a human-readable progress line and a completion summary.
5. Capture the telemetry snapshot.

```bash
./elnath daemon start
./elnath daemon submit "summarize the project structure"
./elnath daemon status
bash scripts/alpha_telemetry_report.sh --out artifacts/month4-alpha-report.json
./elnath daemon stop
```

## Evidence to capture for each rehearsal

- `make lint`
- `make test`
- `make build`
- one `daemon status` sample showing rendered progress text
- one archived `alpha_telemetry_report.sh --out ...` JSON snapshot

## Exit criteria

Treat the rehearsal as passing only when the operator can complete the flow above without manual source edits or ad-hoc debugging.
