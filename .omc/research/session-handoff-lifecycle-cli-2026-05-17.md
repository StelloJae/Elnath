# Session Handoff Lifecycle CLI

Date: 2026-05-17 KST

Branch:

- `codex/user-input-operator-ux`

## Goal

Improve Elnath's Hermes-style continuity and handoff product path without
benchmark work.

The concrete gap:

- Session handoff metadata already supported states:
  `requested`, `claimed`, `running`, `completed`, `failed`.
- `elnath task handoff <id>` could generate a recap and record
  `--request SURFACE`.
- Operators could not directly mark later lifecycle states from the CLI.

## References Inspected

Elnath:

- `cmd/elnath/cmd_task_handoff.go`
- `cmd/elnath/cmd_task_resume_context.go`
- `cmd/elnath/cmd_task_test.go`
- `internal/agent/session.go`
- `internal/agent/session_test.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/tests/hermes_cli/test_session_handoff.py`
- local release/gap-map notes around `/handoff` live session transfer.

Claude Code:

- local session/remote-session reference mapping from the convergence gap map.

Use boundary:

- Flow reference only.
- No proprietary source, prompts, or error strings copied.

## Change

`elnath task handoff <id>` now supports explicit lifecycle recording:

- `--state STATE`
- `--surface SURFACE`
- `--reason TEXT`

Example:

```bash
elnath task handoff 42 --state claimed --surface cli --reason "claimed by local operator"
```

Behavior:

- records a session handoff event through the existing session JSONL metadata
  path;
- uses a local CLI principal for explicit state changes;
- preserves existing `--request SURFACE` behavior;
- rejects using `--request` and `--state` together;
- renders the latest handoff state through existing plain text, JSON, and
  markdown recap paths.

## Changed Files

- `cmd/elnath/cmd_task_handoff.go`
- `cmd/elnath/cmd_task_test.go`

## Verification

Red first:

- `go test ./cmd/elnath -run TestCmdTaskHandoffWithQueueRecordsLifecycleState -count=1`
  failed before implementation with `unknown task handoff flag: --state`.

Green:

- `go test ./cmd/elnath -run 'TestCmdTaskHandoffWithQueue(RecordsLifecycleState|RequestRecordsHandoffState|PrintsResumeRecap|MarkdownOutput|SaveWritesMarkdown)|TestBuildTaskResumeHandoffContextIncludesCompactRecap' -count=1`
  passed.
- `go test ./cmd/elnath -count=1` passed.
- `go test ./internal/agent -run 'TestRecordHandoffAndLoadStatus|TestRecordHandoffRejectsUnknownState|TestLoadSessionSkipsResumeLines' -count=1`
  passed.

## Benchmark / Corpus Boundary

- No benchmark run.
- No baseline run.
- No corpus mutation.
- No public superiority claim.

## Claim Boundary

Allowed:

- Operators can record non-request handoff lifecycle states from
  `elnath task handoff`.
- Handoff recaps now surface the lifecycle state after explicit CLI state
  updates.
- This improves Elnath's Hermes-style continuity path in a narrow Elnath-native
  CLI slice.

Forbidden:

- Do not claim full Hermes `/handoff` live transfer parity.
- Do not claim remote session transfer or cross-device atomic handoff.
- Do not claim Elnath product completion from this slice alone.

## Remaining Risk

- This records lifecycle state; it does not move a live runtime between
  processes.
- No signed remote claimant identity exists yet.
- Gateway surfaces other than CLI do not yet expose these state transitions.

## Next Recommendation

Keep batching local product/runtime UX work. Good next candidates:

1. progress/alive-status polish for long local/daemon work;
2. terminal-native user-input choice UX;
3. gateway exposure for handoff state only after CLI behavior is stable.
