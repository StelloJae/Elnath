# Runtime / Operator UX Batch Readiness

Date: 2026-05-18 KST

Branch:

- `codex/runtime-progress-status`

Commits:

- `9aa818d` `feat(runtime): surface runtime progress phases`
- `6d6ba9d` `feat(skill): expose curator schedule status`
- `d3e40b8` `feat(task): add interactive question answers`

Goal lane:

- Codex-Claude-Hermes convergence
- Product/runtime first
- Benchmark second

## Summary

This branch batches three product/runtime improvements that make Elnath feel
less hidden and more operator-ready during long-running autonomous work.

The batch does not run benchmarks and does not claim public superiority. It
improves daily-driver runtime surfaces.

## Included Milestones

### Runtime Progress / Alive Status

Artifact:

- `.omc/research/runtime-progress-alive-status-2026-05-18.md`

Adds:

- typed `event.RuntimeProgressEvent`;
- daemon progress kind `runtime`;
- runtime phase forwarding to CLI, daemon progress observers, and legacy CLI
  callbacks;
- phase events for prompt build, workflow run, completion check, session
  persistence, completion retry, and verification retry;
- parsed `progress_event` metadata in task monitor JSON surfaces.

### Skill Curator CLI Status / Install

Artifact:

- `.omc/research/skill-curator-cli-status-install-2026-05-18.md`

Adds:

- `elnath skill curator status`;
- `elnath skill curator status --json`;
- `elnath skill curator install`;
- `elnath skill curator install --interval DURATION`;
- `elnath skill curator install --run-on-start`;
- `elnath skill curator install --json`.

### Terminal User Question Interactive Answer

Artifact:

- `.omc/research/terminal-user-question-interactive-answer-2026-05-18.md`

Adds:

- `elnath task answer --interactive`;
- session/request-filtered pending question lookup;
- terminal question display with numbered options;
- reuse of existing receipt-backed `user_question_answer` validation and
  queue-backed resume.

## Reference Impact

Codex / Claude Code:

- moves Elnath toward visible long-running execution and interactive terminal
  answer flows;
- does not claim full TUI/modal parity.

Hermes:

- moves Elnath toward continuity-first operator UX and skill lifecycle
  surfacing;
- does not claim full Hermes streaming gateway or automatic skill curator
  parity.

Elnath-native boundary:

- no proprietary code, prompts, or error text copied;
- changes are Go-native and reuse existing event, daemon, scheduler, skill, and
  user-question receipts.

## Verification

Focused milestone verification:

- `go test ./internal/event ./internal/daemon ./cmd/elnath -run 'TestRuntimeProgressEvent|TestOnTextToSinkRuntimeProgressEncodesJSON|TestProgressObserverDispatchesRepresentativeEventTypesAndIgnoresUnknown|TestLegacyCallbackObserverDispatchesRepresentativeEventTypesAndIgnoresUnknown|TestExecutionRuntimeRunTaskEmitsRuntimePhaseProgress|TestDeliveryRouter_OnProgressParsesAndRoutes|TestCmdDaemonStatusRendersStructuredProgressEnvelope|TestTaskMonitorToolReturnsParsedProgressEvent|TestCmdTaskMonitorWithQueueJSONIncludesParsedProgressEvent' -count=1`
  passed.
- `go test ./cmd/elnath -run 'TestCmdSkill(Curator|Proposals)' -count=1`
  passed.
- `go test ./cmd/elnath -run 'TestCmdTaskAnswerWithQueue(InteractiveChoice|AcceptsChoiceFlag|EnqueuesBoundAnswer|RejectsStaleRequest|RejectsTimedOutRequest)' -count=1`
  passed.

Branch-level proportional verification:

- `go test ./cmd/elnath ./internal/event ./internal/daemon ./internal/skill ./internal/scheduler -count=1`
  passed.
- `go vet ./...` passed.
- `git diff --check` passed.

## Benchmark Boundary

No benchmark lane was run.

- Full v8 benchmark: not run
- Baseline: not run
- Codex comparison: not run
- Claude comparison: not run
- Corpus mutation: none
- Baseline mutation: none

## Claim Boundary

Allowed:

- Elnath now exposes runtime phase progress in CLI/daemon progress surfaces.
- Elnath operators can inspect/install the skill curator schedule.
- Elnath terminal operators can answer pending user questions interactively.

Not claimed:

- full Claude Code TUI parity;
- full Hermes gateway/curator parity;
- automatic skill rewrite quality;
- live schedule hot reload;
- benchmark success;
- Codex/Claude/Hermes superiority.

## Remaining Risk

- Runtime progress is phase-level, not a rich transcript timeline.
- Skill curator install uses static scheduler config and takes effect after
  daemon restart.
- Interactive question answer is stdin/stdout, not a full terminal modal.

## Next Recommendation

Open one coherent PR for this branch after a final status check, or add one
small session handoff/resume recap slice only if keeping the batch local is
still preferred.
