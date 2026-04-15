# Gate Retry v8: Research Findings

**Status**: RESEARCH COMPLETE — corpus authoring + implementation deferred until dog-food signal lands
**Phase**: Post v7 PASS, pre-corpus-expansion
**Date**: 2026-04-16
**Supersedes**: n/a (complements v2 spec `2026-04-12-gate-retry-benchmark-optimization.md`)
**Depends on**: 2-4 week dog-food observation for corpus case selection

## Purpose

Scope v8 without writing cases. Capture what the v2→v7 cycle proved, what residuals still sit on the table, and which decisions genuinely need dog-food evidence before we commit. Corpus authoring (7 → 25) is held until real task patterns emerge from the deployed F-6 build; this document exists so the next authoring session can start immediately without re-discovery.

## v7 Baseline (current)

- Gate 3.2 PASS via `3eb9291 feat(eval): Gate 3.2 PASS — targeted BUG prompt fixes`
- 3-run aggregate: BUG 9/9 (100 %), BF 7/12 (58.3 %)
- Corpus size 7: 4 brownfield (`GO-BF-001/002`, `TS-BF-001/002`) + 3 bugfix (`GO-BUG-001/002`, `TS-BUG-001`)
- Gate thresholds are relative to external Claude Code baseline with a multiplicative margin (`internal/eval/gate.go:254` `thresholdTrackAtLeastWithMargin`). v8 does NOT redefine thresholds; it broadens the evidence pool feeding them.

## Spec v2 Adoption Audit

Spec v2 listed 7 root causes and ~10 implementation items. Current codebase state (verified 2026-04-16):

| Item | Status | Evidence |
|------|--------|----------|
| P0-1 Tool description rewrite | ✅ Shipped | `internal/tools/*.go` expanded descriptions |
| P0-2 Read-dedup + loop blocking | ✅ Shipped | `internal/tools/read_tracker.go` |
| P0-3 Dedup reset on compression | ❌ Missing | No `DedupReset` / compression-hook reference in `internal/tools` or `internal/conversation` |
| P0-4 Post-write timestamp refresh | ❔ Unverified | Needs code trace (tool-level touch on write) |
| P1-1 Tool result size cap | ✅ Shipped | `agent.go:602 toolResultPerToolLimit = 50_000` |
| P1-2 Budget pressure injection | ✅ Shipped | `agent.go:167-172` BUDGET WARNING / BUDGET messages |
| P1-3 Ack-continuation detection | ✅ Shipped | `agent.go isAckOnly` + retry nudge |
| P2-1 System prompt quality (ant P1–P4) | ✅ Partial | `internal/prompt/brownfield_node.go` covers ant P1/P2/P3/P4 + Bugfix Discipline, Go/TS specific guidance. **Greenfield path lacks these** — BrownfieldNode only renders when `state.ExistingCode == true` |
| P2-2 BenchmarkMode prompt skip | ✅ Shipped | All `internal/prompt/*_node.go` check `state.BenchmarkMode` |
| P2-3 MaxIterations env control | ✅ Shipped | `agent.go WithMaxIterations` option + `defaultMaxIterations = 50` |

v7 load-bearing prompt is `BrownfieldNode.Render` (74 lines of execution discipline + Bugfix Discipline + Go/TS-specific Viper/Next.js guidance). Any regression in brownfield/bugfix prompts will be immediately visible; any new ant/Hermes items should extend rather than replace it.

## Residual Hermes / Ant Audit Items

18 patterns documented in `memory/reference_claude_code_ant_audit.md` + research memory `project_elnath_gate_retry_research.md`. Adoption matrix:

| # | Pattern | Status | v8 priority | Rationale |
|---|---------|--------|-------------|-----------|
| 1 | Tool description behavior-guidance | ✅ | — | shipped |
| 2 | Read-dedup + consecutive block | ✅ | — | shipped |
| 3 | Dedup reset on compression | ❌ | **MED** | Benchmark tasks rarely hit compression, but real dog-food tasks will. If compression triggers mid-task and dedup persists, re-reading now-relevant files is blocked. Dog-food signal needed. |
| 4 | Post-write timestamp refresh | ❔ | LOW | Need code trace. If missing, write→immediate-read cycle reports false staleness. |
| 5 | Ack-continuation detection | ✅ | — | shipped |
| 6 | Budget pressure 70/90 % | ✅ | — | shipped |
| 7 | Max iterations handler (tools kv strip) | ❔ | LOW | `agent.go:228` ErrMaxIterations exists; tool-slot stripping at ceiling unverified |
| 8 | 3-layer tool result control | ✅ partial | MED | per-tool truncation at 50K shipped; per-result persistence + per-turn budget unverified |
| 9 | 9-section structured compression summary | ❌ | LOW | `internal/conversation` has 3-stage compression already; rewriting template without baseline comparison is speculative |
| 10 | Dynamic schema patching | ❌ | LOW | `execute_code` / `browser` N/A in Elnath |
| 11 | Tool argument type coercion | ❔ | LOW | String→int/bool coercion prevents minor tool-call failures; check if any observed failure came from this |
| 12 | 13-category error classifier | ❌ | **MED** | Benchmark bugfix recovery currently opaque. A classifier could turn "test failed" → structured category → targeted recovery prompt. Highest ROI if wired into agent.go retry path. |
| 13 | Decorrelated jitter backoff | ❌ | LOW | Current `agent.go:267 delay *= 2` deterministic. Variance gain in multi-run gate is marginal. |
| 14 | 17 gateway platform adapters | ❌ | — | Out of scope for Elnath |
| 15 | Plugin lifecycle hooks (10 stages) | ❌ partial | LOW | Elnath `HookRegistry` has PreToolUse/PostToolUse/OnStop. Adding pre_llm_call is feasible; value needs dog-food evidence. |
| 16 | Doctor --fix self-healing | ❌ | LOW | Orthogonal to gate. |
| 17 | SWE benchmark runner | ❌ | LOW | We already have `internal/eval` — porting mini_swe_runner is a rewrite with uncertain gain. |
| 18 | Tirith security scanner | ❌ | — | Out of scope |

Ant-specific residuals (from `reference_claude_code_ant_audit.md` 199 callsites):

- Most prompt-side ant items already live in `BrownfieldNode`. Remaining un-adopted ant items are execution/binary-level (DCE flags, USER_TYPE gate) which don't translate to a Go agent.
- Greenfield path is the true gap: `BrownfieldNode.Render` returns empty when `state.ExistingCode == false`. Ant discipline applies equally to greenfield tasks; v7 didn't fail greenfield, but v8 broader corpus will expose it if present.

## v8 Target Space

### 1. Corpus expansion 7 → 25

Current matrix (tight coverage):
- tracks: brownfield_feature (4), bugfix (3) → 2 tracks
- languages: go (4), typescript (3) → 2 languages
- repo_class: service_backend × 7 → 1 class

Expansion hypothesis (balanced matrix, requires dog-food case selection):

```
        | service_backend | library | cli_tool
--------+-----------------+---------+---------
BF   go |        2        |    1    |    1     = 4 BF go
BF   ts |        2        |    1    |    1     = 4 BF ts
BF   py |        1        |    1    |    1     = 3 BF py  (NEW language)
BUG  go |        2        |    1    |    1     = 4 BUG go
BUG  ts |        2        |    1    |    1     = 4 BUG ts
BUG  py |        1        |    1    |    0     = 2 BUG py (NEW language)
REG  go |        0        |    2    |    0     = 2 regression (NEW track)
REG  ts |        0        |    2    |    0     = 2 regression
--------+-----------------+---------+---------
                                   total = 25
```

Proposed additions (placeholders — actual repos/commits picked after dog-food):

- **brownfield, python**: Django request-id middleware, Flask extension lifecycle, click CLI flag wiring
- **bugfix, python**: Poetry lockfile drift, pytest fixture teardown, SQLAlchemy session close
- **regression track** (NEW): bisect a historical commit, replay the fix in a constrained prompt — measures whether the agent can use `git log` / `git bisect` signals. Hermes mini_swe_runner pattern without the full runner.
- **repo_class = library**: smaller surface area, tests more tightly scoped; likely produces different token-budget profiles than `service_backend`.
- **repo_class = cli_tool**: stdin/stdout contract, less framework noise.

Why defer case selection:
- v6/v7 showed wrapper-specific BUG fixes (Viper `WatchConfig`, Next.js find-config). Generalizable? Unknown until case #8-25 comes from **observed** task patterns, not imagined ones.
- `feedback_research_before_spec.md`: "얕은 조사 금지, 두 번 실패한 교훈". Writing Python/regression cases without seeing what patterns actually break in production use repeats that failure mode.

Minimum dog-food signal to unblock case authoring: ≥ 20 real tasks observed (mix of BF/BUG/refactor) across ≥ 2 weeks. Track in `.elnath/data/onboarding_metric.json` + daemon task log.

### 2. Prompt tuning hypotheses

| Hypothesis | Confidence | Activation condition |
|------------|------------|----------------------|
| H1: Greenfield discipline node (unconditional ant P1/P2/P3/P4, lighter than BrownfieldNode) | MED | Wait for v8 corpus runs to show greenfield regression vs v7; only act if greenfield pass-rate < BF baseline |
| H2: Split `BrownfieldNode` per-language into dedicated nodes (GoBrownfieldNode, TSBrownfieldNode, PythonBrownfieldNode) | LOW | Only if line count in BrownfieldNode >120 and benchmark shows language-specific drift |
| H3: Structured error classifier injected into retry prompt | MED | Instrument retry path to log failure categories in v8 runs; adopt if ≥2 recovery cases benefit |
| H4: Dedup reset on compression signal | MED | Instrument compression trigger rate in dog-food; adopt if ≥5 % of sessions hit compression |
| H5: Per-result persistence tracking (Hermes 3-layer result control) | LOW | Only if token budget post-ceiling removal still overflows |

All hypotheses are **deferred until v8 corpus runs produce differential evidence**. Implementing H1-H5 pre-emptively repeats the v6 mistake of stacking changes without measurement.

### 3. Agent loop efficiency

v2 listed `164s mean vs 27s baseline` as a problem. v7 did not report duration. Hypotheses for v8 latency wall:

- **L1**: Tool result truncation at 50K may still produce oversized `tool_result` blocks at scale (multiple large results in one turn). Measure via per-turn byte total.
- **L2**: Benchmark mode currently skips WikiRAG/Persona/SessionSummary but still loads LessonsNode. Check whether lesson injection adds measurable latency.
- **L3**: Retry delays (`retryBaseDelay = time.Second`, 2× growth) compound: 3 attempts = 3s min. Not significant unless empty-response loop triggers repeatedly.

No action items until v8 corpus adds duration measurement per task. Runner already captures `WallClockMS` (`internal/eval/types.go`); add p50/p95 breakdown by track to scorecard if missing.

## Open Questions (require dog-food evidence)

1. Does compression trigger during typical Telegram/daemon workflows? Frequency?
2. Which tool categories dominate a real dog-food session (bash/file/git/web)? Rebalance tool description token budget accordingly.
3. Do F8 locale-mixed prompts confuse benchmark tasks if we later add Korean/mixed corpus? (v8 corpus in English only — defer locale variance to v9.)
4. Does LB7 fault injection need benchmark integration (fault-under-load scorecard)? Currently separate.
5. Does F7 onboarding metric reveal friction that correlates with task success? Cross-reference first-run task outcomes.

## Decisions Made (without dog-food)

- **Corpus target = 25 cases** — locked. Fewer = variance unchanged vs v7; more = authoring effort not recoverable in 1 session.
- **New languages** = Python only. Rust fold-in deferred to v9 (crate ecosystem differs enough to warrant its own track audit).
- **New track** = regression. Refactor / performance tracks deferred (hard to define pass criteria without human review).
- **Gate thresholds unchanged** — same multiplicative margin relative to Claude Code external baseline. v8 is hardening, not threshold movement.
- **Prompt changes deferred** — BrownfieldNode is load-bearing. No pre-corpus prompt rewrites. H1-H5 decisions happen in a v8-review session after first full run.

## Deliverables When Dog-Food Completes

A v8 implementation spec (separate doc) should contain:

1. Concrete 18 new cases with chosen repos + commits + acceptance criteria (authored from observed dog-food task patterns, not imagined)
2. Scorecard migration: per-track breakdown × per-language × per-repo_class
3. Conditional prompt adjustments (if corpus run reveals specific regressions, apply H1-H5 in priority order — one at a time with measurement between)
4. Duration p50/p95 per track added to gate report

Until then: keep v7 frozen, accumulate dog-food task logs, return to this document with evidence.

## Cross-references

- Spec v2 (implementation-focused, mostly shipped): `docs/superpowers/plans/2026-04-12-gate-retry-benchmark-optimization.md`
- 4-source audit: `docs/research/2026-04-12-claude-code-ant-vs-external-exhaustive-audit.md`, `docs/research/2026-04-12-hermes-exhaustive-audit.md`
- Memory: `project_elnath_gate_retry_research.md`, `reference_claude_code_ant_audit.md`
- Load-bearing prompt: `internal/prompt/brownfield_node.go:27-77`
- Gate rules: `internal/eval/gate.go:123-145` (hard gate), `254-296` (margin gate)
- Corpus file: `benchmarks/public-corpus.v1.json` (task schema reference)
