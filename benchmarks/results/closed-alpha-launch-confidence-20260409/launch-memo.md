# Elnath Closed Alpha Launch Confidence Memo

Date: 2026-04-09
Mode: Ralph closeout

## Scope
This memo closes the current launch-confidence pass for closed alpha.

## What was verified inside the repo
- Month 4 repo-level alpha readiness gate is PASS.
- Shared continuity/runtime substrate exists in code.
- Thin Telegram operator shell exists in code and its tests pass.
- Alpha telemetry/reporting paths exist and are verified in repo.
- Month 3 bugfix signal is restored strongly.
- Month 3 canary weaknesses were narrowed and targeted repairs proved them repairable.

## External-environment confidence step status
The real Telegram/operator confidence pass is now **partially exercised**.

What was proven with real credentials:
- Telegram bot token is valid (`getMe` succeeded)
- direct bot delivery to the configured chat works (`sendMessage` succeeded)
- a real `elnath telegram shell` process can start against a Telegram-enabled config
- a real daemon task can complete while the Telegram shell is running
- the Telegram shell recorded completion delivery in `telegram-shell-state.json` (`notified_completion_ids: [1]`)

What is still not proven:
- inbound operator interaction from the real Telegram chat (`/status`, `/approvals`, `/approve`, `/followup` / `/resume`)
- one full CLI → Telegram → CLI continuation cycle with real user/operator input

Evidence:
- config path resolved and a rehearsal-specific config was created from `~/.elnath/config.yaml`
- bot API checks succeeded
- daemon status reached `done`
- shell state recorded the delivered completion notification

## Interpretation
This means:
- **Repo-level readiness:** PASS
- **External-environment confidence:** PARTIALLY PROVEN

So the current state is:
> Elnath is technically ready enough for a controlled closed alpha candidate, but the final confidence pass remains incomplete until one real Telegram/operator rehearsal exercises inbound operator commands and shared-session continuation.

## Go / No-Go recommendation
### Recommendation: CONDITIONAL GO
Invite the first 2–10 power users **only if** the operator can execute one final inbound Telegram/operator rehearsal immediately before or during the first invite wave.

Otherwise, treat this as **NO-GO / HOLD** until the live inbound Telegram rehearsal is completed.

## Required final rehearsal before broad invite
1. Start daemon / runtime from CLI
2. Launch a long-running task from CLI
3. Confirm Telegram receives progress and completion notifications
4. Exercise an approval-required action via Telegram
5. Exercise `/status` plus `/followup` or `/resume` from the real Telegram chat
6. Resume / follow up on the same task/session without full re-priming
7. Capture artifact/log proof of this run

## Onboarding checklist
- [x] Telegram bot token configured
- [x] Telegram chat ID configured
- [x] `elnath telegram shell` can start without config errors
- [x] daemon is running and reachable
- [x] one background task completion notification observed in Telegram
- [ ] one approval flow observed in Telegram
- [ ] one resume/follow-up flow observed from Telegram back into shared session/task state
- [ ] operator knows where the runbook and known limits live

## Known limits summary
- Outbound Telegram delivery is proven, but inbound operator interaction is not yet proven in this environment
- Repo-level gate can pass before external-env confidence is proven
- Telegram shell is intentionally thin and operator-only, not a broad companion surface
- Live telemetry confidence is still partly dependent on real environment/state freshness

## Final decision
- **Repo gate:** PASS
- **Launch confidence gate:** CONDITIONAL
- **Final recommendation:** run one real inbound Telegram/operator rehearsal, then proceed with the first 2–10 power-user invites if that rehearsal is clean.
