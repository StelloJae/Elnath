# Elnath Closed Alpha Onboarding Checklist

## Operator setup
- [ ] Confirm `~/.elnath/config.yaml` exists
- [ ] Add Telegram credentials/config (`enabled`, bot token, chat id, optional API base URL / poll timeout)
- [ ] Verify daemon socket/config paths
- [ ] Verify wiki/data directories writable

## First-run checks
- [ ] `./elnath version`
- [ ] `./elnath daemon start`
- [x] `./elnath telegram shell`
- [x] `./elnath daemon status`

## First operator rehearsal
- [x] Start a task from CLI
- [x] See Telegram completion notification
- [x] Exercise one approval in Telegram
- [x] Exercise one `/followup` action in Telegram
- [x] Confirm shared session continuity in CLI/runtime after Telegram interaction

## Artifacts to keep
- [x] daemon status output
- [x] Telegram rehearsal artifact(s)
- [x] one completion summary artifact
- [x] one alpha telemetry / launch-confidence artifact

## Fail-closed rule
If any of the above fail, do not expand invites; fix and rerun rehearsal first.
