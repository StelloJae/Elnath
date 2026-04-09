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
The requested real Telegram/operator end-to-end rehearsal could **not** be executed in this environment because the local runtime configuration does not expose Telegram credentials/config needed for a live shell session.

Evidence:
- Config path resolved to `~/.elnath/config.yaml`
- No Telegram block/keys were found in the accessible runtime config search
- No `ELNATH_TELEGRAM_*` or related Telegram env vars were present in the environment

## Interpretation
This means:
- **Repo-level readiness:** PASS
- **External-environment confidence:** NOT YET PROVEN

So the current state is:
> Elnath is technically ready enough for a controlled closed alpha candidate, but the final confidence pass remains incomplete until one real Telegram/operator rehearsal is run with valid credentials.

## Go / No-Go recommendation
### Recommendation: CONDITIONAL GO
Invite the first 2–10 power users **only if** the operator controls the Telegram credentials and can execute one live rehearsal immediately before or during the first invite wave.

Otherwise, treat this as **NO-GO / HOLD** until the live Telegram rehearsal is completed.

## Required final rehearsal before broad invite
1. Start daemon / runtime from CLI
2. Launch a long-running task from CLI
3. Confirm Telegram receives progress and completion notifications
4. Exercise an approval-required action via Telegram
5. Resume / follow up on the same task/session without full re-priming
6. Capture artifact/log proof of this run

## Onboarding checklist
- [ ] Telegram bot token configured
- [ ] Telegram chat ID configured
- [ ] `elnath telegram shell` can start without config errors
- [ ] daemon is running and reachable
- [ ] one background task completion notification observed in Telegram
- [ ] one approval flow observed in Telegram
- [ ] one resume/follow-up flow observed from Telegram back into shared session/task state
- [ ] operator knows where the runbook and known limits live

## Known limits summary
- Live Telegram/operator flow is not yet proven in this environment
- Repo-level gate can pass before external-env confidence is proven
- Telegram shell is intentionally thin and operator-only, not a broad companion surface
- Live telemetry confidence is still partly dependent on real environment/state freshness

## Final decision
- **Repo gate:** PASS
- **Launch confidence gate:** CONDITIONAL
- **Final recommendation:** run one real Telegram/operator rehearsal with live credentials, then proceed with the first 2–10 power-user invites if that rehearsal is clean.
