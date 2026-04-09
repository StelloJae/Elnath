# Telegram Outbound Rehearsal Evidence

Date: 2026-04-09

## Scope
This artifact records the externally exercised portion of the closed-alpha launch confidence pass using a real Telegram bot token and chat ID.

## What was exercised
1. Bot credential validation via `getMe`
2. Direct bot delivery via `sendMessage`
3. Start `elnath daemon start` with a Telegram-enabled rehearsal config
4. Start `elnath telegram shell` against the same config
5. Submit a real daemon task (`say hello in one short sentence`)
6. Confirm daemon task reaches `done`
7. Confirm Telegram shell records delivered completion state

## Evidence summary
- `getMe`: ok=true
- `sendMessage`: ok=true
- daemon submit: task created successfully (`Task submitted: ID 1`)
- daemon status: task reached `done`
- shell state: `{"notified_completion_ids":[1]}`

## Interpretation
This proves the **outbound** Telegram/operator path is real:
- bot token works
- bot can deliver to the target chat
- the thin Telegram operator shell can notify real completion events from a running daemon task

## Remaining gap
This artifact does **not** prove inbound operator control from the real Telegram chat. The following still need one live rehearsal:
- `/status`
- `/approvals` / `/approve` or `/deny`
- `/followup` or `/resume`
- shared CLI → Telegram → CLI continuity after that interaction
