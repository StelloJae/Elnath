# Phase C-2 W1 Foundation Notes

## Implemented

- `internal/skill` foundation completed:
  - `Skill.Status`, `Skill.Source`
  - draft filtering in `Registry.Load()`
  - shared contracts in `interfaces.go`
  - JSONL `Tracker`
  - wiki-backed `Creator` with draft promote hot-reload
- CLI surface added:
  - `elnath skill list`
  - `elnath skill show <name>`
  - `elnath skill create <name>`
  - `elnath skill edit <name>`
  - `elnath skill delete <name>`
  - `elnath skill stats`
- Telegram shell additions:
  - `/skill-list`
  - `/skill-create <name>` creates a draft scaffold
- Runtime wiring added:
  - `skillTracker`, `skillCreator` stored in `executionRuntime`
  - direct skill execution records usage JSONL
  - `skill-promote` scheduled tasks now round-trip as a distinct daemon task type

## Intentionally Deferred

- `create_skill` tool registration
- `SkillGuidanceNode` prompt registration
- real draft consolidation/promotion logic

These belong to W2/W4 or the final integration step in `docs/specs/PHASE-C2-IMPL-PLAN.md`.
