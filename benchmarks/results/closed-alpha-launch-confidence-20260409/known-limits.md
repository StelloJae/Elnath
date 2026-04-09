# Elnath Closed Alpha Known Limits (Current)

- Telegram remains a thin operator shell only.
- Repo-level readiness and one live operator rehearsal are both now proven, but repeated rehearsals are still advised before widening beyond a small invite wave.
- Some telemetry confidence depends on the freshness/migration state of the local data store.
- Closed alpha should stay small (2–10 power users) until repeated real operator rehearsals are uneventful.
- The live rehearsal exposed a separate planner/subtask JSON parsing weakness on one ambiguous CLI-seeded task; that is not a Telegram-shell blocker, but it should be hardened before widening beyond the first small cohort.
