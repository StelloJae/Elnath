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
- [ ] Start a long-running task from CLI
- [ ] See Telegram status/progress notification
- [ ] See Telegram completion notification
- [ ] Exercise one approval in Telegram
- [ ] Exercise one `/followup` or `/resume` action in Telegram
- [ ] Confirm shared session continuity in CLI after Telegram interaction

## Artifacts to keep
- [ ] daemon status output
- [ ] Telegram shell transcript / screenshots / log excerpt
- [ ] one completion summary artifact
- [ ] one alpha telemetry report artifact

## Fail-closed rule
If any of the above fail, do not expand invites; fix and rerun rehearsal first.
