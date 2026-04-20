# Self-Healing Observe-Only Infrastructure (Phase 0) — Design Spec

- **Date**: 2026-04-20 KST
- **Status**: Draft — brainstorm complete, awaiting implementation plan
- **Scope lock**: Phase 0 observe-only infra only. Retry / lesson compounding / injector / scorecard axis는 **후속 세션**.
- **Brainstorm partner**: Claude Opus 4.7 + Jay (partner mode)
- **Critic lap**: 1회 수행, VERDICT=REVISE 반영 완료 (3 CRITICAL + 5 MAJOR + 4 MEDIUM drained)
- **Source prompt**: `.omc/self-healing-brainstorm-prompt.md`

---

## 0. Summary / Intent

Elnath의 "단순 LLM wrapper → self-improving AI platform" pivot의 first-step. 실패 / 부분 실패 task를 **관찰만 하여** 후속 retry / lesson compounding 설계의 empirical foundation을 확보한다.

**Explicit**: Phase 0는 *관찰 infra*만. SuggestedStrategy는 기록되나 실행되지 않는다. Retry는 Phase 1에서, 조건부 진입.

---

## 1. Motivation & Context

### 1.1 계기
2026-04-20 세션 말미 사용자 질문 — *"Elnath에 self-healing 기능이 있나? 뭔가 안되면 잘못한 건 되돌리고 다시 한다던가..."*. 이것이 architectural discovery 계기가 되어 별도 brainstorm session 분리.

### 1.2 기존 기반 (재활용 축)
- Continuity Pillar: 대화/메모리 프로세스 재시작 persist (완료)
- Karpathy-style learning: outcomes.jsonl (278 records), lessons.jsonl (87 / 27 superseded = 0.31), RoutingAdvisor, maturity scorecard (4 axes)
- Error classification: 13 categories + recovery hints (`internal/agent/errorclass/`)
- 2026-04-20 세션에서 `FinishReasonPartialSuccess` 신호 구축 (FU-TeamSubtaskRetry P3)

### 1.3 이미 존재하는 partial self-healing
| 레벨 | 메커니즘 | 위치 |
|---|---|---|
| Provider transient error | `streamWithRetry` 3회 exp-backoff | `internal/agent/agent.go:398` |
| Workflow fail | team → single fallback | `internal/orchestrator/team.go:62` |
| Tool IsError | agent loop 자연 복구 | agent loop 전역 |
| DB | SQLite `tx.Rollback()` | `conversation/history.go`, `daemon/queue.go` |
| Error classify | 13 categories + recovery hints | `errorclass/classify.go` |
| User-driven undo | Telegram `/undo` | `telegram/shell.go:452` |
| Long-term adaptation | RoutingAdvisor | `internal/learning/*` |

### 1.4 미보유 (사용자 의도에 근접하나 부재)
- Atomic task-level rollback (`tools.Reversible()` flag만 있고 엔진 부재)
- Semantic retry with strategy change
- Self-diagnosing probe loop
- Meta-retry with reflection on team partial
- `elnath doctor --fix`

### 1.5 Critic pushback 요약 (ADVERSARIAL verdict=REVISE)

원래 5-layer "Reflexive Learning Loop" (Option C+A 하이브리드) 설계는 critic lap에서 다음 결함들이 드러났다:

- **C1**: `internal/learning/outcome.go:36` `IsSuccessful(partial_success)==true`. Trigger로 쓰면 outcome 이중 기록 → RoutingAdvisor 편향 → self-improving이 self-degrading으로 역진.
- **C2**: Free-text reflection hint는 in-band prompt injection vector. Tool output (bash/web)이 transcript를 오염 → hint가 retry system prompt에 elevated trust로 promoted → 새 destructive action 유발 가능.
- **C3**: Lesson injection path는 `prompt/lessons_node.go`의 `Topic==ProjectID` filter + 1000-char cap + recency sort. "Fingerprint-gated prioritization" 인프라가 현 코드에 부재.
- **Skeptic 주장**: Reflexion / Self-Refine 연구 gain은 verifiable tasks에서만. Elnath task mix은 largely unverifiable → reflection은 confidence amplifier이지 correctness amplifier 아님.
- **M3**: FU-TeamSubtaskRetry(in-flight)와 double-ownership risk.
- **Med3**: scope-fence 문화 (`docs/month4-closed-alpha-readiness.md:233`) — default-on retry는 문화 위반.

결과: 원래 10h scope → **observe-only 4h scope**로 축소. Retry는 실전 데이터 수집 후 별도 세션으로 분리.

---

## 2. Phase -1: Blocker 결정 (설계만, 코드 없음)

### 2.1 Fingerprint spec

**결정**: `hash(subject + tool_names)`

- Input normalization: `subject` lowercase + trim, `toolNames` sort
- Algorithm: SHA-256 → base32, first 12 chars
- Properties: deterministic, concurrency-safe (pure function)

**Rationale**: subject는 user intent 근사, tool_names는 상대적으로 stable.

**Rejected**:
- `hash(system_prompt + subject)` — prompt 변경마다 fingerprint 깨짐, 재시도 과다
- `hash(subject + first-3-tool-args)` — args 직렬화 노이즈 민감

### 2.2 Persistent store

**결정**: 신규 `~/.elnath/data/self_heal_attempts.jsonl`

- Append-only JSONL, `outcomes.jsonl` 패턴 재활용
- Continuity Pillar 준수 (process restart persist)
- Mutex-protected concurrent writes

**Rationale**: JSONL pattern이 outcomes / lessons / consolidation_state 3개 파일에서 검증됨.

**Rejected**:
- `elnath.db` table 추가 — schema migration overhead
- `outcomes.jsonl` field embed — 의미 섞임, C1 재발 risk

### 2.3 FU-TeamSubtaskRetry 경계

**결정**: team-layer retry는 FU-TeamSubtaskRetry 소유, reflection engine은 **agent-level only**.

- Team-layer partial-success trigger 이 feature에서 건드리지 않음
- Agent.Run 내부 loop 종결만 관찰 대상

**Rationale**: M3 반영, double-ownership 회피.

---

## 3. Phase 0: Observe-only infra

### 3.1 Architecture (3 layer)

#### [1] Trigger Layer (agent loop 종료 직전)

**Triggered**:
- `FinishReasonError`
- `FinishReasonBudgetExceeded` (M2)
- `FinishReasonAckLoop` (M2)

**NOT triggered (명시적 제외)**:
- `FinishReasonPartialSuccess` — 이미 success로 기록됨 (C1)
- `FinishReasonStop` — 정상 종결
- tool `IsError` 개별 — **[verified 2026-04-20]** `FinishReasonToolError` 상수 부재 (agent.go:238-249). tool IsError는 loop 자연 복구이며, loop가 소진되면 `FinishReasonError` 또는 `Stop`으로 종결되어 상위 trigger에 포섭됨.

**Skip rules (observe-only에도 적용)**:
- `errorclass.Category` ∈ {RateLimit, Auth, AuthPermanent, Billing, Overloaded} (M4)
  - **[verified 2026-04-20]** `AuthPermanent`, `Billing` 추가 (category.go:7-10 확인). 둘 다 `ShouldFallback` flag → reflection은 agent-level 신호 중복이므로 skip.
- `ctx.Done` / 사용자 cancel signal (Med4)
- destructive tool + `user_approved` 조합

#### [2] Reflection Engine (신규 package `internal/agent/reflection`)

**Input**:
- Transcript last N turns (N=20, per-turn 1KB cap)
- `errorclass.Summary`
- Task meta
- Fingerprint

**LLM call**:
- Single call via `llm.Provider` (provider-agnostic). **[verified 2026-04-20]** `agent.ApiClient` 부재; provider interface는 `internal/llm/provider.go:105`. `ChatRequest`에 `response_format`/`json_schema` 필드 없음 → prompt-embedded JSON contract + 응답 파싱 방식 채택.
- Output JSON 파싱 → schema validation; 파싱 실패 / enum 범위 초과 → `SuggestedStrategy=unknown` (관찰 레코드는 여전히 기록됨)
- Timeout: 15s (independent context from parent)

**Output (`ReflectionReport`)**:
```go
type Report struct {
    Fingerprint       Fingerprint
    FinishReason      string
    ErrorCategory     string
    SuggestedStrategy Strategy
    Reasoning         string   // 기록 전용, 주입 금지
    TaskSummary       string
}
```

**Strategy enum (closed, C2 대응)**:
- `retry_smaller_scope`
- `fallback_provider`
- `compress_context`
- `abort`
- `unknown` (schema fallback)

#### [3] Observation Layer

Append-only to `~/.elnath/data/self_heal_attempts.jsonl`:

```json
{
  "ts": "2026-04-20T17:54:00Z",
  "task_id": "341",
  "session_id": "596baccc-1101-...",
  "fingerprint": "Q7XB2JFKEMVG",
  "finish_reason": "error",
  "error_category": "tool_execution",
  "suggested_strategy": "retry_smaller_scope",
  "reasoning": "...",
  "task_summary": "..."
}
```

**Explicit NO**: retry 실행 없음, lesson write 없음, RoutingAdvisor 주입 없음.

### 3.2 Components (Go interfaces)

#### `reflection/fingerprint.go`
```go
package reflection

type Fingerprint string

// Pure function, concurrency-safe.
func ComputeFingerprint(subject string, toolNames []string) Fingerprint
```

#### `reflection/trigger.go`
```go
func ShouldReflect(
    finishReason agent.FinishReason,
    errCategory errorclass.Category,
    userCancelled bool,
    destructiveUserApproved bool,
) bool
```

#### `reflection/engine.go`
```go
type Strategy string

const (
    StrategyRetrySmallerScope Strategy = "retry_smaller_scope"
    StrategyFallbackProvider  Strategy = "fallback_provider"
    StrategyCompressContext   Strategy = "compress_context"
    StrategyAbort             Strategy = "abort"
    StrategyUnknown           Strategy = "unknown"
)

type Report struct { /* §3.1 */ }

type Input struct {
    Transcript   []llm.Message   // [verified] agent.Turn 없음; llm.Message 직사용
    ErrorSummary string
    TaskMeta     TaskMeta
    Fingerprint  Fingerprint
}

type Engine interface {
    Reflect(ctx context.Context, in Input) (Report, error)
}

// Default impl: LLMEngine{provider llm.Provider, model string}
// Uses provider.Chat with JSON-contract instructions; parses output to Report.
```

#### `reflection/store.go`
```go
type Store interface {
    Append(ctx context.Context, report Report, meta StoreMeta) error
}

type StoreMeta struct {
    TS        time.Time
    TaskID    string
    SessionID string
}

type FileStore struct {
    path string
    mu   sync.Mutex
}
```

#### `reflection/pool.go`
```go
type Pool struct {
    engine      Engine
    store       Store
    sem         chan struct{}     // max concurrent
    queue       chan job          // bounded backpressure
    wg          sync.WaitGroup
    shutdownCtx context.Context
}

func NewPool(engine Engine, store Store, maxConcurrent, queueSize int) *Pool
func (p *Pool) Enqueue(in Input, meta StoreMeta) bool
func (p *Pool) Shutdown(ctx context.Context) error
```

**Config defaults**: `MaxConcurrent=2`, `QueueSize=10`, `ShutdownGrace=30s`.

#### `internal/agent/agent.go` hook (Run 종료 직전)
```go
if reflectionCfg.Enabled &&
   reflection.ShouldReflect(finishReason, errCategory, cancelled, destructiveApproved) {
    input := reflection.Input{ /* build from local state */ }
    reflectPool.Enqueue(input, meta)  // non-blocking
}
return finishReason, lastErr
```

**Invariant**: Run의 return value/timing에 영향 없음.

### 3.3 Data Flow

#### Happy path timeline

```
T0  user submits task → daemon queue
T1  daemon.dispatch → agent.Run
T2  loop: tool calls, LLM turns
T3  loop ends with FinishReason=Error
T4  ShouldReflect (pure fn, <1µs) → true
T5  build Input
T6  pool.Enqueue(in, meta)  ← non-blocking
T7  agent.Run returns to caller  ← 사용자 응답 완료
─────────────────────────────────
T8  [background] Engine.Reflect  ~2-5s
T9  [background] Store.Append    <1ms
```

#### Async 결정 근거

Sync vs Async trade-off에서 async 선택: observe-only 단계의 사용자 UX penalty 정당화 불가 (2-5s 추가는 유의미).

#### Safeguards

- Pool이 자체 `context.WithTimeout(background, 15s)` 소유 — parent cancel 무관
- Queue full → non-blocking select default → drop + `slog.Warn`
- Graceful shutdown 30s grace drain → 그 후 drop

#### 동시성 모델 (daemon env)

- Max concurrent reflections: 2 (config)
- Queue depth: 10 (config)
- JSONL 동시 write → `FileStore.mu` (sync.Mutex)로 직렬화
- `outcomes.jsonl` writer pattern 재활용 예정 (impl 직전 소스 직독 검증)

### 3.4 Error Handling

#### Severity ladder

| Level | 범위 | 예 | 전파 |
|---|---|---|---|
| L1 | reflection 단일 실패 | LLM timeout, schema invalid, JSONL write 실패 | 없음 (`slog.Warn`) |
| L2 | Pool degrade | queue 포화 | 없음 (backpressure warn) |
| L3 | reflection infra 불능 | Engine/Store init 실패 | daemon startup warn + `Enabled=false` auto-degrade |
| L4 | daemon critical | — | 해당 없음 |

#### Recovery matrix — **retry 금지 원칙**

| 실패 | Degrade |
|---|---|
| LLM transient error | `Strategy=unknown` + store |
| LLM schema invalid | `Strategy=unknown` + store |
| LLM rate-limit (reflection 자체) | drop + warn (재귀 방지) |
| JSONL write 실패 | drop + warn |
| Pool queue full | drop newest + warn |
| Engine/Store init 실패 | `Enabled=false` (startup) |
| Context cancel (shutdown) | 30s drain or drop |

Phase 0에서 reflection infra 자체는 **retry 일체 금지**. Cascade risk (rate-limit burst 증폭) 원천 차단.

#### Privacy

- JSONL은 `~/.elnath/data/` (local-private home dir)
- Phase 0: PII 마스킹 안 함 — 로컬 머신 내부 파일이므로 허용 범위
- Cloud sync / telemetry 연동은 **Phase 2 이후 반드시 sanitization 선행** (gate)

#### Retention

- Phase 0: rotation 없음
- 예상 1-2 MB/month → 1년 ~15-25 MB (허용 범위)
- 50 MB 초과 시 Phase 1+에서 archive rotation 도입

### 3.5 Config

```go
// internal/daemon/config.go 확장
type SelfHealingConfig struct {
    Enabled      bool          `toml:"enabled"`       // default true
    ObserveOnly  bool          `toml:"observe_only"`  // default true (Phase 0)
    MaxTurns     int           `toml:"max_turns"`     // default 20
    Timeout      time.Duration `toml:"timeout"`       // default 15s
}
```

CLI: `--no-self-heal` 전역 off flag.

### 3.6 Observability

#### `elnath daemon status --self-heal` 확장

```
Self-Heal Observations (Phase 0)
  total attempts:         N
  dropped (queue full):   M
  by finish_reason:       error=X, budget=Y, ack_loop=Z, tool_error=W
  by error_category:      ...
  strategy distribution:  retry_smaller_scope=A, fallback_provider=B,
                          compress_context=C, abort=D, unknown=E
  schema fail rate:       E/N = x%
  sample window:          first_ts → last_ts
```

이 명령어가 **Phase 1 전환 판정 도구**.

#### `slog` structured fields

```go
slog.Info("reflection completed",
    "fingerprint", fp, "finish_reason", reason,
    "error_category", cat, "suggested_strategy", strategy,
    "duration_ms", dur.Milliseconds())

slog.Warn("reflection queue full", "dropped_fingerprint", fp)

slog.Warn("reflection failed",
    "err", err, "fingerprint", fp, "stage", "engine|store")
```

---

## 4. Testing (~25 tests, coverage 80%+)

### 4.1 단위

| 파일 | 핵심 테스트 |
|---|---|
| `fingerprint_test.go` | Stable, ToolOrder (sort), Normalization (case/whitespace), EmptyTools |
| `trigger_test.go` | Error/Budget/AckLoop/ToolError → true. Stop → false. RateLimit/Auth/Overloaded skip. Cancel skip. Destructive-approved skip. **`TestShouldReflect_PartialSuccess_NOT_Triggered` 필수 (C1 regression)**. |
| `engine_test.go` | ValidResponse, SchemaInvalid → `unknown`, Timeout, EnumOutOfRange → `unknown`, ApiClientError 전파 |
| `store_test.go` | Append, Concurrent 10-goroutine, DirAutoCreate, PermissionDenied |
| `pool_test.go` | EnqueueProcessed, QueueFullDropsNewest, ShutdownDrains, ShutdownTimeout |

### 4.2 통합 (fake engine/store 주입)

- `TestAgentReflectionHook_TriggeredOnError` — end-to-end
- `TestAgentReflectionHook_NotTriggeredOnStop`
- `TestAgentReflectionHook_NonBlockingReturn` — timing assert (Run return < reflection 완료)
- `TestAgentReflectionHook_DisabledFlag` — config off → pool 미호출

### 4.3 회귀 방지

- 기존 `agent_test.go` 전체 통과 (reflection disabled 경로 변경 없음)
- `go test -race` pass (Pool/Store concurrency)

---

## 5. Open Questions — impl 직전 verify 목록

**"Verify before coding" memory rule의 실행 지점**. Brainstorm 단계에선 가정, impl 진입 전 실제 소스 직독으로 확정.

**Verified 2026-04-20** — impl 세션 Phase A 결과 기록.

| # | 확인 대상 | 직독 파일 | Verified 결과 |
|---|---|---|---|
| 1 | `agent.FinishReason` enum 상수명/값 | `internal/agent/agent.go:236-249` | ✓ `Stop`/`BudgetExceeded`/`AckLoop`/`Error`/`PartialSuccess`. `ToolError` **없음** — tool IsError trigger 제거 |
| 2 | `agent.ApiClient` structured output | `internal/llm/*.go` (**경로 정정**: codex는 `internal/llm/codex_oauth.go`) | ✗ `agent.ApiClient` 부재. `llm.Provider` interface 사용. `ChatRequest`에 `response_format` 없음 → prompt-embedded JSON contract + 출력 파싱 |
| 3 | `outcomes.jsonl` append pattern | `internal/learning/outcome_store.go` | ✓ `sync.Mutex` + `MkdirAll 0o755` + `O_APPEND\|O_CREATE\|O_WRONLY 0o600` + `json.NewEncoder` 재활용 |
| 4 | `errorclass.Category` 상수명 | `internal/agent/errorclass/category.go:5-19` | ✓ `Auth`/`RateLimit`/`Overloaded` 확인 + `AuthPermanent`/`Billing` 추가 발견 → skip rule에 포함 |
| 5 | `agent.Turn` 구조 | `internal/agent/agent.go`, `internal/llm/message.go` | ✗ `agent.Turn` 부재. transcript = `[]llm.Message` (Content: TextBlock/ToolUseBlock/ToolResultBlock/ThinkingBlock/ImageBlock) |
| 6 | Pool daemon DI 경로 | `internal/daemon/daemon.go`, `cmd/elnath/runtime.go:1091` | ✓ `newDaemonTaskRunner` closure 주변에 Pool 주입. `buildExecutionRuntime`에 Pool field 추가 |

---

## 6. Out of Scope (explicit)

Phase 0 **제외** — 혼란 방지용 명시:

- **Retry execution** — `SuggestedStrategy`를 실제 실행으로 연결하지 않음. 관찰만.
- **Lesson compounding** — ReflectionReport를 `lessons.jsonl`에 쓰지 않음.
- **Injector filter extension** — `prompt/lessons_node.go` 수정 없음.
- **Scorecard 5번째 axis (`self_healing`)** — Phase 0 후 2-4주 dogfood 후 재평가.
- **Atomic rollback (Option B)** — 전체 feature 범위 밖.
- **`elnath doctor --fix` (Option D)** — 별개 initiative.
- **Team-layer trigger** — FU-TeamSubtaskRetry 소유.

---

## 7. Phase 1 전환 기준 (4 조건 모두 충족 시 retry brainstorm 재개)

| 기준 | 조건 | 근거 |
|---|---|---|
| Sample size | records ≥ 50 | 통계 유의미성 |
| Strategy 품질 | schema pass ≥ 80%, `unknown` < 20% | LLM structured output 실전 신뢰도 |
| Recurrence | 같은 fingerprint 3회+ 재발 ≥ 5건 | retry 가치 있는 패턴 증거 |
| Scope fence | RateLimit/Auth/Cancel skip 규칙 false trigger 0건 | skip rule 실전 정확도 |

미달 시 Phase 0 연장 + trigger/skip 조정 후 재수집. **4개 모두 충족 → Phase 1 retry brainstorm 새 세션** (verifiability 전략 포함, 이 문서 범위 밖).

---

## 8. Implementation Roadmap (Phase 0, 다음 세션)

Scope: **~4h**, TDD 진행.

| Step | 내용 | 예상 |
|---|---|---|
| 1 | Open Questions 6개 소스 직독 검증 (pre-impl verify) | 0.5h |
| 2 | `ComputeFingerprint` + `ShouldReflect` TDD 구현 | 1.0h |
| 3 | `Engine` interface + `LLMEngine` + `Store` + `Pool` 구현 | 1.5h |
| 4 | `agent.go` hook + config struct + CLI flag | 0.5h |
| 5 | Integration test + `daemon status --self-heal` 확장 | 0.5h |

실제 step-by-step implementation plan은 `writing-plans` skill 결과물로 생성.

---

## 9. Related

### 9.1 Memory links (auto-loaded next session)
- `project_elnath_status.md`
- `project_elnath_remaining.md`
- `feedback_verify_before_coding.md` — updated this session (brainstorm scope 포함)
- `feedback_brainstorm_critic_lap.md` — created this session

### 9.2 Session artifact
- Brainstorm prompt: `.omc/self-healing-brainstorm-prompt.md`
- Next session prompt (갱신 예정): `.omc/next-session-prompt.md`

### 9.3 Reference files (code semantics verified in this brainstorm)
- `internal/learning/outcome.go:36` — `IsSuccessful(partial_success)==true` (C1 근거)
- `internal/prompt/lessons_node.go:64` — `Topic==ProjectID` filter (C3 근거)
- `internal/agent/errorclass/category.go:59-62` — `ShouldRotateCred`/`ShouldFallback` flags
- `internal/agent/agent.go:398` — `streamWithRetry` exp-backoff
- `internal/orchestrator/team.go:62` — team → single fallback
- `docs/month4-closed-alpha-readiness.md:233` — "silent self-healing guarantee 금지" 문화 선언

---

## 10. Changelog

- 2026-04-20 — brainstorm session, critic lap 반영 (3 CRITICAL + 5 MAJOR + 4 MEDIUM), scope 축소 확정.
