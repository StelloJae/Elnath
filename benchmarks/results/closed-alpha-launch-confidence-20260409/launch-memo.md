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
The real Telegram/operator confidence pass is now **proven**.

What was proven with real credentials:
- Telegram bot token is valid (`getMe` succeeded)
- direct bot delivery to the configured chat works (`sendMessage` succeeded)
- a real `elnath telegram shell` process can start against a Telegram-enabled config
- a real daemon task can complete while the Telegram shell is running
- the Telegram shell recorded completion delivery in `telegram-shell-state.json` (`notified_completion_ids: [1]`)
- inbound operator interaction from the real Telegram chat worked (`/status`, `/approvals`, `/approve 1`, `/followup ... continue`)
- one CLI → Telegram → shared-session continuation cycle was exercised with a real carried session id

Evidence:
- `benchmarks/results/closed-alpha-launch-confidence-20260409/telegram-outbound-rehearsal.md`
- `benchmarks/results/closed-alpha-launch-confidence-20260409/telegram-inbound-rehearsal.md`
- DB evidence from `~/.elnath/rehearsal-live/elnath.db` showed approval `#1` approved and Telegram follow-up task `#2` queued/running for session `bc3c9877-4cea-4f0e-806b-6489793d2f50`
- shell state advanced `next_update_offset` to `766608538`

## Interpretation
This means:
- **Repo-level readiness:** PASS
- **External-environment confidence:** PROVEN

So the current state is:
> Elnath has now crossed both the repo gate and the live operator confidence boundary for a tightly controlled closed alpha.

## Go / No-Go recommendation
### Recommendation: GO (small closed alpha)
Proceed with the first **2–10 power-user invites**.

Conditions:
- keep the invite wave small
- keep Telegram thin/operator-only
- keep the runbook and known-limits doc attached to the invite process
- continue treating any new Telegram/runtime regressions as launch blockers for wider rollout

## Final rehearsal coverage
1. Start daemon / runtime from CLI ✅
2. Launch a task from CLI ✅
3. Confirm Telegram receives completion delivery ✅
4. Exercise an approval-required action via Telegram ✅
5. Exercise `/status` plus `/followup` from the real Telegram chat ✅
6. Continue on the same shared session without full re-priming ✅
7. Capture artifact/log proof of this run ✅

## Onboarding checklist
- [x] Telegram bot token configured
- [x] Telegram chat ID configured
- [x] `elnath telegram shell` can start without config errors
- [x] daemon is running and reachable
- [x] one background task completion notification observed in Telegram
- [x] one approval flow observed in Telegram
- [x] one resume/follow-up flow observed from Telegram back into shared session/task state
- [x] operator knows where the runbook and known limits live

## Known limits summary
- Telegram is still intentionally thin and operator-only, not a broad companion surface
- Live external confidence is now proven for one real operator rehearsal, but repeated rehearsals are still recommended as the invite pool grows
- Some telemetry confidence depends on the freshness/migration state of the local data store
- Closed alpha should stay small (2–10 power users) until repeated real operator rehearsals are uneventful

## Final decision
- **Repo gate:** PASS
- **Launch confidence gate:** GO
- **Final recommendation:** proceed with a small closed alpha invite wave now.
