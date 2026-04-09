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
3. If you are exercising the thin Telegram operator shell, configure it explicitly first.
4. Submit one small task.
5. Confirm `daemon status` shows a human-readable progress line and a completion summary.
6. Capture the telemetry snapshot.

### Optional thin Telegram operator setup

Keep Telegram thin and operator-only. Configure exactly one poller per bot token.

```yaml
telegram:
  enabled: true
  bot_token: "${ELNATH_TELEGRAM_BOT_TOKEN}"
  chat_id: "${ELNATH_TELEGRAM_CHAT_ID}"
  api_base_url: ""
  poll_timeout_seconds: 30
```

Environment-only operators can also use:

- `ELNATH_TELEGRAM_ENABLED=true`
- `ELNATH_TELEGRAM_BOT_TOKEN=...`
- `ELNATH_TELEGRAM_CHAT_ID=...`
- `ELNATH_TELEGRAM_POLL_TIMEOUT_SECONDS=30`

Then run:

```bash
./elnath telegram shell
```

If Telegram returns a polling-conflict error, stop the other active poller for that bot token before retrying.

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
- if Telegram is enabled, one note confirming whether the operator shell started cleanly or hit a polling-conflict guardrail

## Exit criteria

Treat the rehearsal as passing only when the operator can complete the flow above without manual source edits or ad-hoc debugging.
