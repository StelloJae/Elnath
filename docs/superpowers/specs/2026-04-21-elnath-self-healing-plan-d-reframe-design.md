# Self-Healing Plan D Reframe — Design Spec (2026-04-21)

- **Date**: 2026-04-21 KST
- **Status**: Draft — brainstorm 완료, critic lap 예정
- **Supersedes**: `.omc/self-healing-brainstorm-prompt.md` (보류 prompt, 이번 세션으로 소진)
- **Relates to**: `docs/superpowers/specs/2026-04-20-self-healing-observe-only-phase0-design.md` (Phase 0 observe-only infra, already landed)
- **Brainstorm partner**: Claude Opus 4.7 (1M context) + Jay (partner mode)
- **Source prompt**: `.omc/next-session-prompt.md` v10 (Self-Healing brainstorm kickoff)

---

## 0. Summary / Intent

Self-healing brainstorm kickoff 세션이 진단 단계에서 **전략 리프레임**으로 전환됐다. 배경:

1. 2026-04-20에 이미 Phase 0 observe-only infra가 spec + critic lap 완료로 landed.
2. Phase 1 retry 진입 조건 4개 (records ≥ 50 / schema pass ≥ 80% / recurrent fingerprint ≥ 5 / skip-false 0)가 spec에 명시돼 있으며 현재 **전부 미충족** (records = 0).
3. 자연 reflection-eligible error 발생률 = **1건/4일** (0.34%). 현 속도로 gate 충족까지 ~200일 — strategic timeframe 내 불가능.
4. 파트너 주력 surface가 Telegram임이 이번 세션에서 확인됨. CLI/TUI는 Phase 8로 reclassify.

결론: self-healing 구현 진입보다 **Plan D (우선순위 재조정)** 우선. Plan C (shadow retry)는 deferred with 재진입 조건 3개로 박는다.

**이 spec은 코드 변경 없음.** 설계 + 전략 재조정 + 재진입 조건 명문화.

---

## 1. Plan E 진단 결과

### 1.1 294 outcomes / 4 days 분포

| Finish reason | Count | Reflection eligible? | 비고 |
|---|---|---|---|
| `stop` | 277 | — | 성공 |
| `partial_success` | 1 | no | Phase 0 spec §3.1 명시 제외 (`IsSuccessful==true`, outcome.go:36-38) |
| `load_session_failed` | 7 | no | `wantsReflection` switch 대상 아님 |
| `error` (iter=0) | 8 | no | agent loop 진입 전 또는 iter-0 종료 |
| `error` (iter ≥ 1) | 1 | **yes** | 2026-04-20 07:50 team, tool_stats=[glob], in=6227/out=298 |

- 일평균 outcome: 73/day
- Reflection-eligible 자연 발생률: 1/4일 (0.34%)

### 1.2 Wire 상태 검증 — 정상

- `internal/orchestrator/single.go:102-103`: `cfg.ReflectionEnqueuer != nil` → `agent.WithReflection(...)` append
- `cmd/elnath/runtime.go:864`: `cfg.ReflectionEnqueuer = rt.buildReflectionEnqueuer(sess, userInput)`
- `cmd/elnath/runtime.go:614-660`: `buildReflectionEnqueuer` returns closure forwarding to `rt.reflectPool.Enqueue`
- `internal/agent/agent.go:149-150`: `WithReflection` → `reflectEnqueue` setter
- `./elnath daemon status --self-heal`: `status=enabled`, store path 정합, `total=0`

**Hook wire bug 가설 기각.**

### 1.3 확인된 wire gap — Plan C prerequisite (critic lap 결과)

2026-04-20 07:50 team workflow 1건 (iter=2, finish_reason=error, session d654d8dd) 이 record=0인 원인을 critic lap (v2 revision, 2026-04-21) 이 확정:

**Root cause**: `internal/orchestrator/team.go:393-400` `runOne`이 subtask용 `WorkflowConfig`를 새로 구성하면서 `ReflectionEnqueuer` 필드를 **복사하지 않음**. 결과:

- `internal/orchestrator/single.go:102-103` `agentOptions`는 `cfg.ReflectionEnqueuer != nil` 일 때만 `agent.WithReflection(...)`을 append
- `team.go:393-400` `runOne`이 새 `WorkflowConfig` 생성 시 Model / MaxIterations / SystemPrompt / Hooks / Permission / ToolExecutor 만 복사, `ReflectionEnqueuer` 제외
- → team subtask agent는 `reflectEnqueue == nil` 로 동작 → `fireReflectionHook` early return (`agent.go:480`)

즉 **team-workflow subtask error는 Phase 0 observe에 invisible**. 이건 "수수께끼"가 아니라 **known wire gap**.

**처리**: 이 파일(`orchestrator/team.go`) 은 Plan D scope fence 범위 안 (§3 "버튼" 과 구조적으로 연결). 이번 세션에서는 수정하지 않는다. Plan C 재진입 preflight에서 필수 fix 항목으로 분류 (§2.2 재진입 preflight 목록 보강).

- **기존 Phase 0 observation=0** 은 일부 wire gap 때문일 수도 있음 (정확 비율 미상)
- team outcome `error` 자연 발생이 전체의 얼마인지는 `internal/orchestrator/team.go` 전체 run count 측정 필요 (기록 없음 — 추정용 Phase 7.4 real runner 착수 시 부산물로 확보 가능)

### 1.4 Gate 산술

- Phase 1 진입: records ≥ 50
- 현 속도 1/4일: 50 records = ~200일
- **Plan A (organic wait) 기각** — strategic timeframe 내 충족 불가능

---

## 2. 전략 리프레임 — Plan D 우선 + Plan C deferred

### 2.1 Plan D (우선순위 재조정, 1차 target)

#### D-1 Telegram sprint — 파트너 주력 surface, split commits

| Sub-phase | 내용 | 추정 | Commit |
|---|---|---|---|
| **D-1a** | **FU-CR2** — ChatResponder tool subset 주입 | 2–3h | 독립 commit |
| **D-1b** | **FU-TgReactions** — Telegram reactions API | 1.5h | 독립 commit |

**묶음 session이지만 commit 분리.** 근거:
- D-1a는 `internal/telegram/` ChatResponder 구조 수정 (architectural risk)
- D-1b는 self-contained feature (reactions API 추가)
- 묶으면 regression 시 rollback 범위 불명확
- CLAUDE.md "PHASED EXECUTION" 원칙과 일치

진행 순서: D-1a → `make test` pass → D-1b → `make test` pass → 2 commits.

#### D-2 Benchmark 축 완성

- **Phase 7.4 real runner** — `internal/eval/v2_runner.go` stub → 실제 task runner 연결. Spearman harness 유지.
- 추정: 8–12h
- 근거: 6개월 로드맵 Month 3 refocus gate의 **입력 데이터**. surface 무관한 strategic weight.
- 완료 판정: real execution Spearman ≥ 0.5 on 10-run cycle (기존 plan `.omc/plans/phase-7-3-benchmark-v2.md` §4 Risk #4)

#### Plan D 내 기타 FU 재분류

- **FU-TUI → Phase 8로 folded**. 파트너 명시적 의도: "Phase 8 전까지는 Telegram 위주 사용, 그 뒤에 TUI/native app 전면 개편"
- **FU-Cancel** (MEDIUM 1.5h), **FU-RuntimeReload** (MEDIUM 3–4h), **FU-Obs** (LOW 1h) — 기존 우선순위 유지, D-1/D-2 이후 평가

### 2.2 Plan C (shadow retry) — deferred with 재진입 조건

#### 재진입 시 실행할 설계 (지금은 코드 아님)

- **Phase 0 invariant 유지**: production loop은 그대로 error로 종료 (user-facing impact 0)
- **Shadow sandbox**: trigger 조건 충족 시 reflection engine의 `SuggestedStrategy`를 **분리된 agent.Run** 에서 적용해 재시도
- **Observation 확장**: shadow outcome을 `self_heal_attempts.jsonl` 에 `shadow_retry_success: bool` 필드로 부가. Phase 0 schema addendum.
- **진입 기준**: 기존 4개 (records/schema/fingerprint/skip-false) + **shadow efficacy ≥ 30% on ≥ 10 shadow attempts**
- **추정 구현 scope**: 6–8h + critic 재랩

#### 재진입 조건 (3개 중 먼저 충족되는 것으로 진입)

1. **Phase 0 observations ≥ 15** — 데이터 기반. `wc -l ~/.elnath/data/self_heal_attempts.jsonl ≥ 15`.
   - **Sampling bias 주의 (critic lap)**: 현재 counter는 single-workflow error만 잡고, team subtask error는 §1.3 wire gap 때문에 invisible. Preflight에서 gap 수정 후 counter 해석 재평가 필요 (기존 수치를 유지할지 gap 수정 직후부터 재카운트할지 진입 시 판단).

2. **Plan D D-1 완료 + 30일 경과** — 시간 기반. Telegram sprint ship 후 사용 pattern 변화 관찰. **이 조건이 최종 backstop** (다른 두 조건이 sampling / subjective bias로 실패해도 시간은 객관적).

3. **파트너 주관 "self-heal 있었으면" 경험 ≥ 5건 (rolling 60-day window)** — 체감 기반.
   - 기록 위치: `.omc/self-heal-pain-log.jsonl` (append-only, 시간순)
   - 형식: `{"ts":"<ISO8601>","note":"<짧은 맥락>"}` 한 줄에 하나
   - 평가: 최근 60일 내 5건 이상 누적되면 충족
   - 이 형식은 recall/recency bias를 시간창으로 제한

#### 재진입 시 preflight (critic lap 결과 반영)

- **§1.3 wire gap 선 수정** — `team.go:393-400` `runOne`의 `WorkflowConfig` 에 `ReflectionEnqueuer` 복사 추가. TDD (기존 `internal/orchestrator/single_test.go` 패턴 참고). 이게 fix되기 전 Plan C 진입 무효.
- **Phase 0 spec §3.1 invariant addendum** 작성: "observed but not executed" → "observed, shadow-executed in isolated sandbox, production loop untouched"
- **`destructiveUserApproved` param 불일치 확인**: `reflection/trigger.go:34`는 4-param, `agent.go:454` `wantsReflection`은 3-param. 현재 양쪽이 skip 대상을 동등하게 커버하지만 destructive-approved scenario가 생기면 agent-side가 skip 못 할 리스크. Plan C 진입 시 통일.
- Critic lap 재수행

### 2.3 Phase 8 scope reference (long-term, 별도 brainstorm 필요)

파트너 명시적 의도: "Claude Code app 비슷하게 카피하는 것도 생각 중 (non-CLI)". 2026-04-14 Claude Code desktop 전면 재설계를 참고 source로 기록. 별도 2–3h deep brainstorm session 예약.

> **이 섹션은 reference snapshot (2026-04-21 기준). Commitment 아님.** Phase 8 진입 시점에 Claude Code 그 시점 버전 + 파트너 우선순위 + Elnath 상태로 재평가. 아래 feature 목록은 "지금 이걸 빌드하자" 가 아니라 "지금 이 설계 공간이 있다" 라는 map.

#### Claude Code 2026-04 주요 feature (reference)

- **Multi-session sidebar** (status/project/environment 필터, 그룹화)
- **Drag-drop 8-pane grid**: chat · diff · preview · terminal · file · plan · tasks · subagent
- **Integrated terminal** (tests/builds)
- **In-app file editor** (spot edits)
- **Rebuilt diff viewer** (대용량 changeset)
- **Preview pane** (HTML, PDF, local app server)
- **Side chat** (Cmd+;) — 실행 중 task에 branch 질문
- **3 view modes**: Verbose / Normal / Summary
- **`/tui`** fullscreen rendering (flicker-free)
- **Monitor tool** — background log streaming
- **Ultraplan** — cloud plan draft + web editor
- **Model picker + `/effort` slider** (xhigh/high/max, auto-fallback)
- **Mobile push notifications** (Remote Control + "Push when Claude decides")
- **Slash vs Skills** — slash 수동 / skill context-based auto-invoke

#### Phase 8 scope 예상

- **GUI framework 선택** — Electron / Tauri / native Swift/SwiftUI
- **Multi-pane layout engine**
- **8 pane type 매핑** (chat/diff/preview/terminal/file/plan/tasks/subagent)
- **Slash commands + skill auto-invoke**
- **Model switching inline + effort slider**
- **Streaming feedback** (Monitor-style)
- **Mobile push notifications** → Telegram surface 기존 인프라 연결

**Phase 8은 이번 세션 범위 밖.** 3–6주 규모. 파트너가 ready 신호 주면 별도 spec 진행.

#### 참고 소스

- [Claude Code What's new](https://code.claude.com/docs/en/whats-new)
- [Anthropic: Redesigning Claude Code on desktop for parallel agents](https://claude.com/blog/claude-code-desktop-redesign)
- [MacRumors: Anthropic Rebuilds Claude Code Desktop App](https://www.macrumors.com/2026/04/15/anthropic-rebuilds-claude-code-desktop-app/)
- [Claude Code Skills docs](https://code.claude.com/docs/en/skills)
- [Use Claude Code Desktop docs](https://code.claude.com/docs/en/desktop)

---

## 3. Scope fence — Phase 0 invariant 유지

Plan D 작업 기간 동안 **절대 건드리지 않을** 지점 (Phase 0 observe-only guarantee 유지):

- `internal/agent/reflection/**` 내부 판정 로직 (trigger / evaluation / skip / engine / strategy)
- `internal/agent/agent.go` `fireReflectionHook` / `wantsReflection`
- `internal/agent/agent.go` `reflectionSkipCategories` 상수
- `self_heal_attempts.jsonl` record schema
- `reflection/engine.go` closed-enum Strategy (retry_smaller_scope / fallback_provider / compress_context / abort / unknown)

Plan D 작업이 이 파일들에 **우연히라도** 닿으면 그 작업은 별도 self-healing subtask로 재분류. 재검토 필수.

단, `buildReflectionEnqueuer` 주변 enrichment (Principal / ProjectID 이외 passthrough-only 필드 추가)는 허용. 이 범주는 2026-04-20 FU-ReflectPrincipalFlow 선례 있음.

---

## 4. 이번 세션 산출물 (concrete)

1. **이 spec** — `docs/superpowers/specs/2026-04-21-elnath-self-healing-plan-d-reframe-design.md`
2. **Wiki analyses** — `/Users/stello/llm_memory/Claude Valut/wiki/analyses/2026-04-21-elnath-self-healing-reframe.md` (축약판, decision log 중심)
3. **Next session prompt v11** — `.omc/next-session-prompt.md` v11 (**Telegram sprint D-1a FU-CR2 kickoff**)
4. **Memory 갱신**:
   - `user_profile.md` — Telegram primary surface, Phase 8 vision 추가
   - `project_phase_8_native_app.md` (**new**) — Phase 8 scope + Claude Code 2026-04 parity reference
   - `project_elnath_remaining.md` v11 — Plan D 우선 재배치, Plan C deferred 명시
   - `project_elnath_status.md` v11 — Plan E 진단 결과 addendum
   - `MEMORY.md` — 새 entry 1개 추가

---

## 5. 다음 세션 성공 판정

v11 prompt 읽은 후 파트너가 **"D-1a FU-CR2 TDD 착수 가능"** 상태. `internal/telegram/` ChatResponder 구조 파악, tool subset 분류 기준 합의, TDD red/green 첫 루프 준비까지가 다음 세션 입구.

**이 spec 의 성공 판정**: 파트너가 spec 읽고 "D-1a 착수 OK, D-1b 후속, Plan C deferred 재진입 조건 동의" 3가지를 명시적으로 승인.

---

## 6. Critic lap plan

이 spec이 touch하는 subsystem:
- Self-healing strategy (defer 결정)
- Telegram surface (sprint 진입)
- Benchmark (Phase 7.4 lane)
- Phase 8 (new lane 선언)
- Memory / wiki / session prompt infrastructure

5+ subsystem cross-cutting → `critic` agent (opus, read-only) 1회 lap 필수 (`feedback_brainstorm_critic_lap.md` + CLAUDE.md 프로젝트 원칙).

### Critic focus areas

1. **Plan C 재진입 조건 3개의 완전성** — "영원히 deferred" 회피 충분? 조건이 서로 orthogonal하고 실제 측정 가능한가?
2. **Telegram sprint commit 분리 전략의 안전성** — D-1a / D-1b 경계가 실제로 clean하게 분리되는가? 공유 state가 있으면 어떻게 처리?
3. **Phase 8 scope reference를 지금 박는 것의 premature optimization risk** — 3–6주 규모 lane을 브레인 저장만 하고 진입 안 하는 게 기억 부채 유발하는가?
4. **Plan E 진단의 수수께끼 1건** (iter=2 record=0) watch-item 처리가 충분한가, 아니면 Plan D 착수 전 필수 진단인가?
5. **Scope fence 문구** — silent self-healing guarantee 방지 원칙 (`docs/month4-closed-alpha-readiness.md:233`) 준수 충분한가?
6. **파트너 주관 "self-heal 있었으면 경험 카운트"** — 3번째 재진입 조건이 subjective bias의 함정인가?

### Critic verdict 반영 절차

- `ACCEPT`: spec 그대로 진행, §4 산출물 생성
- `REVISE`: critical/major 이슈를 이 spec §1–3 반영, revision note를 §7 신설해서 기록
- `REJECT`: 세션 멈추고 파트너에게 재설계 필요 보고

---

## 7. Revision history

- **v1** (2026-04-21 KST) — initial draft post-brainstorm, critic pending
- **v2** (2026-04-21 KST) — critic lap complete, verdict **ACCEPT-WITH-RESERVATIONS**. Edits applied:
  1. §1.3 "mystery" 프레이밍 → `team.go:393-400` confirmed wire gap, Plan C prerequisite
  2. §2.2 재진입 조건 #1 에 sampling bias 주의 추가
  3. §2.2 재진입 조건 #3 format `.txt` counter → `.jsonl` timestamped + 60-day rolling window
  4. §2.2 preflight 에 `destructiveUserApproved` param 불일치 open question 추가
  5. §2.3 Phase 8 에 "reference snapshot, not commitment" marker 추가

  **Skeptic challenge (spec 이 다루지 않은 것)**: Plan D 작업 (Telegram sprint) 자체가 reflection-eligible error를 유발할 수 있는데 `team.go` wire gap 때문에 신호가 숨겨짐. 재진입 조건 #2 (30일 time-bound) 가 backstop 역할. 미래 의도적 Plan D 외 team 워크플로 사용 증가 시 gap 수정 우선순위 재평가.
