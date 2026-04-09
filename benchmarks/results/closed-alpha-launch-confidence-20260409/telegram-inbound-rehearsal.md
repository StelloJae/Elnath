# Elnath Telegram Inbound Rehearsal

Date: 2026-04-09
Mode: Ralph closeout

## Goal
Prove the real Telegram inbound operator path, not just outbound delivery.

## Live setup
- daemon started with rehearsal-specific config
- `elnath telegram shell` started with the same config
- real Telegram bot token and real operator chat id configured via env
- rehearsal data dir: `~/.elnath/rehearsal-live`

## Exercised operator commands
The operator sent these commands from the real Telegram chat:
- `/status`
- `/approvals`
- `/approve 1`
- `/followup bc3c9877-4cea-4f0e-806b-6489793d2f50 continue`

## Proven effects
1. **Inbound updates were consumed by Elnath**
   - `telegram-shell-state.json` advanced `next_update_offset` to `766608538`
   - this is beyond the previous outbound-only rehearsal offset and reflects consumed inbound updates
2. **Telegram approval resolution worked**
   - `approval_requests.id=1` changed from `pending` to `approved`
3. **Telegram follow-up / resume path worked**
   - task `#2` was enqueued from Telegram with payload:
     - `{"prompt":"continue","session_id":"bc3c9877-4cea-4f0e-806b-6489793d2f50","surface":"telegram"}`
   - daemon logs showed the task running against the carried session and routing it into a resumed single-session workflow
4. **Shared-runtime continuity was preserved across surfaces**
   - the resumed Telegram follow-up referenced the original session id instead of starting a brand-new disconnected flow

## Notes
- The original CLI-seeded task failed for an unrelated planner/subtask-JSON parsing issue before this inbound rehearsal. That failure did not block Telegram shell completion delivery and does not negate the operator-path proof.
- The resumed follow-up task created a fresh approval request (`#2`), which is expected evidence that the resumed task re-entered the same permission-controlled runtime.

## Conclusion
The real Telegram inbound operator path is now proven:
- status/approval commands reached and were processed by Elnath
- Telegram can approve a live request
- Telegram can trigger a follow-up against an existing shared session

This closes the missing launch-confidence gap from the earlier outbound-only rehearsal.
