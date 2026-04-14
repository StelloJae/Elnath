# Phase F-5 LLM-based Lesson Extraction

**Predecessor:** Phase F-4 (Lessons by-source stats + list filter) DONE
**Status:** SPEC (locked)
**Scope:** ~710 LOC total — Phase 1 (~300) + Phase 2 (~410)
**Branch:** `feat/telegram-redesign`

---

## 0. Goal

Rule-based extractor 9개가 놓치는 패턴을 LLM pass 로 보완. **가장 큰 gap: 성공 패턴 학습** (rule은 failure signal 편향 — Rule C 외 8개 rule 전부 실패 지표).

**Why**: 프로덕션 `lessons.jsonl` 가 2026-04-11~14 기간 task 2-11 완료 기록 있음에도 0 bytes. Rule A-E 가 성공 run 대부분에서 fire 안 함. LLM 이 성공 맥락 (tool 순서·조합·project 관습) 을 추출해야 personalization (`internal/self/persona`) + prompt 주입 (`LessonsNode`) 이 의미 있는 signal 확보.

**Why now**: F-3.2 가 단일 공통 hook (`orchestrator.applyAgentLearning`) 확보 → LLM extractor 주입점 1곳. F-4 Source 세분화 → LLM lesson 출처 표기 (`agent:llm:*`) 즉시 활용 가능.

---

## 1. Decisions (F-5 Design Questions Q1-Q10 결과)

| ID | Question | Answer | Rationale |
|----|----------|--------|-----------|
| Q1 | Trigger | **B** complexity-gated per-run | `num_messages ≥ 5 AND has_tool_call` — p50 stub 세션에서 base cost의 94% 방어 |
| Q2 | Input mode | **B** compact summary | 직전 10 messages + tool-stats 헤더. p75 ~2K tok, Haiku+compact 50/day = $5.70/월 |
| Q3 | Cursor | **A** last-processed line per session | `~/.elnath/data/lesson_cursors.jsonl` (append-only, session_id → last_line_number) |
| Q4 | Existing-lessons manifest | **A** full inject | 현재 0 → 수십 개는 ~1-2K tok 추가. Q9 consolidation 미포함이라 LLM 이 "update-not-duplicate" 스스로 판단 |
| Q5 | Model | **A** Haiku 4.5 고정 | `claude-haiku-4-5-20251213` (registry alias, `internal/llm/registry.go:89`). Config flag 로 1줄 교체 가능 |
| Q6 | Rule×LLM 공존 | **B** parallel + SHA256 dedupe | Rule 은 항상 fire, LLM 은 gate 통과 시만. 의미적 중복은 F-5.2 consolidation 담당 |
| Q7 | Schema | **B** Lesson 확장 | `Rationale string`, `Evidence []string`, `PersonaParam string`, `PersonaDirection string`, `PersonaMagnitude string` 추가. JSONL 하위호환 (`omitempty`) |
| Q8 | Persona delta | **B** 정성 힌트 + 코드 매핑 | LLM 응답에 `persona_param`/`persona_direction`/`persona_magnitude` 평면 필드. 코드가 `{small: ±0.01, medium: ±0.03, large: ±0.06}` 로 매핑하고 `PersonaDelta` 슬라이스 synthesize |
| Q9 | Consolidation | **A** 생략 (F-5.2) | 현재 lesson 수 소량 → dead code 방지 |
| Q10 | Fail handling | **A+C** fail-closed + circuit breaker | 단일 fail = 해당 run LLM 결과 0 (rule 결과는 보존). 10분 내 5회 연속 fail → 10분 pause |

---

## 2. Architecture

```
orchestrator.applyAgentLearning(deps, info)
│
├─ Rule path (기존 F-3.2)
│   ExtractAgent(info) → []Lesson with Source="agent:<workflow>"
│   Store.Append (SHA256 dedupe)
│   SelfState.ApplyLessons(PersonaDelta)
│
└─ LLM path (F-5 신규)
    complexity gate (msgs ≥ 5 AND has_tool_call) → skip on fail
    breaker.Allow() → skip on pause
    cursor.Get(session_id) → since_line
    build compact summary + existing-lessons manifest
    LLMExtractor.Extract(ctx, req) → []Lesson with Source="agent:llm:<workflow>"
    apply PersonaDirection/Magnitude → PersonaDelta
    Store.Append (같은 store — SHA256 dedupe 자연 병합)
    cursor.Update(session_id, last_line)
    breaker.Record(err)
```

Rule path 와 LLM path 는 독립적으로 실행. 실패는 격리 — LLM fail 이 rule 결과 무효화 안 함 (Q10-A fail-closed).

---

## 3. Implementation — Phase 1 (Schema + Mock)

**Scope**: Lesson 스키마 확장 + `LLMExtractor` 인터페이스 + mock 구현 + orchestrator wiring + complexity gate + cursor store + config flag. 실제 Anthropic provider 호출은 Phase 2.

**LOC 추정**: ~300

### 3.1 `internal/learning/lesson.go` 확장

```go
type Lesson struct {
    ID               string        `json:"id"`
    Text             string        `json:"text"`
    Topic            string        `json:"topic,omitempty"`
    Source           string        `json:"source"`
    Confidence       string        `json:"confidence"`
    PersonaDelta     []self.Lesson `json:"persona_delta,omitempty"`
    Rationale        string        `json:"rationale,omitempty"`          // NEW: Why this lesson (Claude Code `Why:` 등가)
    Evidence         []string      `json:"evidence,omitempty"`           // NEW: 원문 transcript snippet (optional, Phase 2+)
    PersonaParam     string        `json:"persona_param,omitempty"`      // NEW: target param (caution/persistence/verbosity/curiosity) — Q8-B
    PersonaDirection string        `json:"persona_direction,omitempty"`  // NEW: "increase"/"decrease"/"neutral"
    PersonaMagnitude string        `json:"persona_magnitude,omitempty"`  // NEW: "small"/"medium"/"large"
    Created          time.Time     `json:"created"`
}
```

신규 필드 전부 `omitempty` — 기존 0-byte lessons.jsonl 및 향후 rule-only lesson 과 하위호환.

### 3.2 `internal/learning/extractor_llm.go` (신규, ~80 LOC)

```go
// LLMExtractor is the abstraction for the F-5 LLM lesson extractor. Phase 1
// ships a mock; Phase 2 wires the Anthropic provider.
type LLMExtractor interface {
    Extract(ctx context.Context, req ExtractRequest) ([]Lesson, error)
}

type ExtractRequest struct {
    SessionID        string
    Topic            string
    Workflow         string
    CompactSummary   string     // 직전 10 msgs + tool-stats 헤더
    ToolStats        []AgentToolStat
    FinishReason     string
    Iterations       int
    MaxIterations    int
    RetryCount       int
    ExistingLessons  []LessonManifestEntry // Q4-A
    SinceLine        int                   // cursor offset for traceability
}

type LessonManifestEntry struct {
    ID    string
    Topic string
    Text  string
}

// MockLLMExtractor returns a deterministic stub lesson. Used in tests and
// as the default until Phase 2 wires real provider.
type MockLLMExtractor struct {
    Lessons []Lesson
    Err     error
}

func (m *MockLLMExtractor) Extract(ctx context.Context, req ExtractRequest) ([]Lesson, error) {
    if m.Err != nil {
        return nil, m.Err
    }
    return append([]Lesson(nil), m.Lessons...), nil
}
```

### 3.3 `internal/learning/persona_mapping.go` (신규, ~30 LOC)

```go
// PersonaDeltaFromHint converts Q8-B qualitative hints to numeric deltas.
func PersonaDeltaFromHint(direction, magnitude string) float64 {
    base := 0.0
    switch strings.ToLower(magnitude) {
    case "small":  base = 0.01
    case "medium": base = 0.03
    case "large":  base = 0.06
    default:       return 0.0  // unknown magnitude → no delta
    }
    switch strings.ToLower(direction) {
    case "increase": return +base
    case "decrease": return -base
    case "neutral":  return 0.0
    }
    return 0.0
}
```

### 3.4 `internal/learning/complexity.go` (신규, ~40 LOC)

```go
// ComplexityGate evaluates whether a run should receive LLM extraction.
// Q1-B: ≥ 5 messages AND at least one tool call.
type ComplexityGate struct {
    MinMessages int
    RequireToolCall bool
}

var DefaultComplexityGate = ComplexityGate{MinMessages: 5, RequireToolCall: true}

func (g ComplexityGate) ShouldExtract(msgCount, toolCalls int) bool {
    if msgCount < g.MinMessages {
        return false
    }
    if g.RequireToolCall && toolCalls == 0 {
        return false
    }
    return true
}
```

Threshold는 향후 tune 가능하도록 public field — config wire up 은 Phase 2 에 포함.

### 3.5 `internal/learning/cursor.go` (신규, ~80 LOC)

```go
// CursorStore tracks the last-processed JSONL line per session, so LLM
// extraction is incremental (Q3-A). Append-only file for crash safety.
type CursorStore struct {
    mu   sync.Mutex
    path string
}

type cursorRecord struct {
    SessionID string    `json:"session_id"`
    LastLine  int       `json:"last_line"`
    UpdatedAt time.Time `json:"updated_at"`
}

func NewCursorStore(path string) *CursorStore { return &CursorStore{path: path} }

func (c *CursorStore) Get(sessionID string) (int, error) { /* scan file, return latest per session */ }
func (c *CursorStore) Update(sessionID string, line int) error { /* append JSON record */ }
```

Append-only 선택 이유: lessons.jsonl 과 같은 crash-safety 모델 유지. Compaction 은 rotate 로직 재활용 가능 (Phase 2 또는 F-5.2).

### 3.6 `internal/orchestrator/learning.go` 확장

```go
type LearningDeps struct {
    Store     *learning.Store
    SelfState *self.SelfState
    Logger    *slog.Logger
    // NEW:
    LLMExtractor   learning.LLMExtractor   // nil → LLM path disabled
    CursorStore    *learning.CursorStore
    Breaker        *learning.Breaker       // Phase 2 추가. Phase 1 에서는 nil 허용
    ComplexityGate learning.ComplexityGate
    // NEW: session context for compact summary (run-scoped, 매 invocation shallow copy 필수 — H1)
    SessionID      string
    MessageCount   int
    ToolCallCount  int
    // CompactSummary returns (text, lastLineProcessed). lastLine 은 cursor 에 저장되는 JSONL
    // line number (H1/C3) — MessageCount 가 아님. Phase 1 mock 은 ("", 0) 반환 가능.
    CompactSummary func() (text string, lastLine int)
}
```

`applyAgentLearning` 확장 (설계 의도 요약 — 정확한 구현은 `PHASE-F5-PHASE1-OPENCODE-PROMPT.md` §6 참고):

1. **Rule path 먼저** (기존 F-3.2 그대로): `ExtractAgent(info)` → `Store.Append` → `SelfState.ApplyLessons(PersonaDelta)` → **즉시 `SelfState.Save()`** (H2: LLM path panic 시 rule 결과 보존).
2. **LLM path 가드**: `LLMExtractor == nil` / complexity gate / `FailCounter.Allow()` 3중 체크. 하나라도 실패 시 early return.
3. **ExtractRequest 조립**: CursorStore.Get → SinceLine. `deps.CompactSummary()` → `(text, lastLine)`. Manifest = `Store.Recent(50)` 매핑.
4. **LLM 호출**: `context.WithTimeout(30s)`. `FailCounter.Record(err)` 무조건 호출 (성공 시 reset, 실패 시 counter++).
5. **실패 → fail-closed**: `log.Warn` 후 early return. Rule 결과는 이미 persistence 됨.
6. **성공 → post-processing**:
   - `Source = llmSourceFor(workflow)` 주입 (`agent:llm:<workflow>`)
   - `applyPersonaHint` 로 `PersonaParam`/`PersonaDirection`/`PersonaMagnitude` → `PersonaDelta` synthesize (C2 fix)
   - `appendAndApply` 로 Store 저장 + persona 적용
   - `lastLine > 0` 일 때만 `CursorStore.Update(sessionID, lastLine)` (C3 fix: MessageCount 쓰지 않음)
   - `llmChanged` 이면 `SelfState.Save()` 호출

**H1 per-run copy 제약**: `applyAgentLearning` 은 포인터 받지만 workflow 호출측에서 shallow-copy 후 mutate (SessionID/MessageCount/ToolCallCount/CompactSummary). Daemon concurrent run 에서 cross-talk 방지.

(과거 이 섹션에 있던 예시 코드는 critic 레드팀 수정을 거치며 prompt §6 으로 이동. spec 은 의도만 유지.)

(legacy 예시 코드 제거 — prompt §6 에서 최신 구현 명세.)

### 3.7 Runtime/Daemon wiring (Phase 1 에서는 mock 주입)

- `cmd/elnath/runtime.go` — `learningStore` 옆에 `cursorStore := learning.NewCursorStore(filepath.Join(cfg.DataDir, "lesson_cursors.jsonl"))` 추가. `LearningDeps` 에 `MockLLMExtractor{}` 주입 (empty lessons → no-op). Config flag `ExperimentalLLMExtraction` 추가 (default false) — true 일 때만 wiring.
- `cmd/elnath/cmd_daemon.go` — runtime 과 동일 패턴.

Phase 1 에서 실제 LLM 호출은 안 되지만, mock 이 non-nil lessons 반환하도록 test 에서 주입 가능 → schema/wiring 검증.

### 3.8 CompactSummary 생성

`internal/conversation/summarize.go` 확장 (또는 신규):

```go
func CompactLessonSummary(msgs []Message, toolStats []agent.ToolStat) string {
    // 1) Header: tool stat lines ("bash: 7 calls / 0 errors / 1.2s total")
    // 2) Separator
    // 3) Last 10 messages (role, truncated content 200 chars)
    // 4) Redaction: secret detector 통과
}
```

약 2K tok 목표. Phase 1 test 에서는 고정 fixture 사용 가능 — 실제 production 호출은 Phase 2 에서.

---

## 4. Implementation — Phase 2 (Haiku Provider + Circuit Breaker)

**Scope**: Anthropic Haiku 호출, prompt engineering, circuit breaker, real wiring, production rollout.

**LOC 추정**: ~410

### 4.1 `internal/learning/extractor_anthropic.go` (신규, ~150 LOC)

```go
type AnthropicExtractor struct {
    provider llm.Provider
    model    string // "claude-haiku-4-5-20251213"
    logger   *slog.Logger
}

func NewAnthropicExtractor(p llm.Provider, model string) *AnthropicExtractor {
    return &AnthropicExtractor{provider: p, model: model}
}

func (a *AnthropicExtractor) Extract(ctx context.Context, req ExtractRequest) ([]Lesson, error) {
    prompt := buildExtractionPrompt(req)
    response, err := a.provider.Chat(ctx, llm.ChatRequest{
        Model:       a.model,
        System:      systemPrompt,
        Messages:    []llm.Message{{Role: "user", Content: prompt}},
        MaxTokens:   1024,       // output ≤ 3 lessons × 200 tok + JSON envelope
        Temperature: 0.0,         // determinism
        EnableCache: true,        // system prompt + manifest 가 stable
    })
    if err != nil {
        return nil, fmt.Errorf("anthropic extract: %w", err)
    }
    return parseLessonResponse(response.Content, req)
}

// Response schema (LLM 이 반환해야 하는 JSON — flat persona fields per C2 fix):
// {
//   "lessons": [
//     {
//       "topic": "string",
//       "text": "string (<=200 chars)",
//       "rationale": "string (<=200 chars) — why this lesson",
//       "evidence": ["optional quoted snippets"],
//       "confidence": "high|medium|low",
//       "persona_param": "caution|persistence|verbosity|curiosity",
//       "persona_direction": "increase|decrease|neutral",
//       "persona_magnitude": "small|medium|large"
//     }
//   ]
// }
// parseLessonResponse 는 위 3개 persona 필드를 Lesson 에 그대로 매핑. applyPersonaHint 가
// PersonaParam 기반으로 PersonaDelta 슬라이스를 synthesize (C2 fix).
```

### 4.2 `internal/learning/breaker.go` (신규, ~80 LOC)

```go
// Breaker is a simple in-process circuit breaker (Q10-C).
// 10-minute rolling window, 5 consecutive failures → 10-minute pause.
type Breaker struct {
    mu           sync.Mutex
    failures     []time.Time
    pauseUntil   time.Time
    windowSize   time.Duration // 10 * time.Minute
    failThreshold int           // 5
    pauseDur     time.Duration // 10 * time.Minute
    logger       *slog.Logger
}

func NewBreaker(logger *slog.Logger) *Breaker {
    return &Breaker{
        windowSize:   10 * time.Minute,
        failThreshold: 5,
        pauseDur:     10 * time.Minute,
        logger:       logger,
    }
}

func (b *Breaker) Allow() bool {
    b.mu.Lock(); defer b.mu.Unlock()
    return time.Now().After(b.pauseUntil)
}

func (b *Breaker) Record(err error) {
    b.mu.Lock(); defer b.mu.Unlock()
    now := time.Now()
    if err == nil {
        b.failures = b.failures[:0]  // reset on success
        return
    }
    // prune outside window
    cutoff := now.Add(-b.windowSize)
    kept := b.failures[:0]
    for _, t := range b.failures {
        if t.After(cutoff) { kept = append(kept, t) }
    }
    kept = append(kept, now)
    b.failures = kept
    if len(b.failures) >= b.failThreshold {
        b.pauseUntil = now.Add(b.pauseDur)
        b.logger.Warn("llm lesson: breaker open", "pause_until", b.pauseUntil)
    }
}
```

`Breaker.Status()` 메서드 추가 → `lessons stats` 에 "LLM extraction: paused (resumes HH:MM)" 노출.

### 4.3 Prompt engineering

System prompt (stable, cache-friendly):

```
You extract lessons from agent runs for Elnath's learning store.
Output STRICT JSON matching the lessons schema. No commentary.
Rules:
- 0-3 lessons per run. Prefer none over forced.
- Lesson text <= 200 chars. Rationale <= 200 chars.
- Update-not-duplicate: if your finding already exists in the manifest, do not emit.
- confidence: high only when evidence is direct and repeatable; medium otherwise.
- persona: include only when behavior change is actionable. Never emit absolute numeric delta.
```

User prompt (per-run):

```
## Existing lessons (recent 50)
{manifest JSON}

## This run
Topic: {topic}
Workflow: {workflow}
Finish reason: {finish_reason}
Iterations: {iter}/{max_iter}
Retry count: {retry}

## Tool stats
{tool stats table}

## Last 10 messages (compact)
{compact summary}

Return JSON.
```

### 4.4 Runtime/Daemon wiring (Phase 2)

- `cmd/elnath/runtime.go` — `cfg.ExperimentalLLMExtraction == true` 시 `NewAnthropicExtractor(provider, "claude-haiku-4-5-20251213")` 주입. `NewBreaker(logger)` 주입. ComplexityGate config 로 파라미터화.
- Provider 선택: config.yaml 의 `anthropic.api_key` 존재 시 Anthropic provider 사용. 없으면 mock 유지 + 로그 warn.
- Config 에 `llm_extraction.enabled`, `llm_extraction.model`, `llm_extraction.min_messages` 추가.

### 4.5 `cmd/elnath/cmd_lessons.go` — stats 출력 확장

```
LLM extraction: enabled (model=claude-haiku-4-5-20251213)
  Last run: 2026-04-14T19:32Z (ok)
  Breaker: closed (0/5 failures in last 10m)
  Sessions processed: 23 (since cursor)
```

또는 breaker open:
```
  Breaker: open (resumes 2026-04-14T19:42Z)
```

### 4.6 `cmd/elnath/cmd_daemon.go`

runtime.go 와 동일한 wiring. daemon 경로는 이미 `research.WithRunnerLearning(rt.learningStore)` 패턴 있음 → LLM extractor 도 동일 옵션 주입.

---

## 5. Tests

### 5.1 Phase 1 tests

**`internal/learning/lesson_test.go`**:
- `TestLesson_JSONRoundtrip_NewFieldsOptional` — rationale/evidence/persona_direction 없는 기존 lesson 이 unmarshal 실패 안 함.
- `TestLesson_JSONRoundtrip_NewFieldsPopulated` — 필드 있는 lesson 의 roundtrip.

**`internal/learning/persona_mapping_test.go`**:
- Table-driven: `(direction, magnitude)` × 9 조합 → 기대 delta.
- Unknown magnitude → 0.0.

**`internal/learning/complexity_test.go`**:
- `msgs=4, tools=1` → false. `msgs=5, tools=0` → false (RequireToolCall). `msgs=5, tools=1` → true. `msgs=100, tools=0, RequireToolCall=false` → true.

**`internal/learning/cursor_test.go`**:
- Get on empty file → 0.
- Update + Get → latest line.
- Multiple sessions interleaved → correct per-session latest.
- Concurrent updates (-race) → no data race.

**`internal/learning/extractor_llm_test.go`**:
- MockLLMExtractor with `Lessons=[...]` → returns copy (mutation isolation).
- MockLLMExtractor with `Err` → returns error.

**`internal/orchestrator/learning_test.go`** 확장:
- `TestApplyAgentLearning_LLMPath_ComplexityGateBlocks` — msgs=3 → LLM 호출 안 됨.
- `TestApplyAgentLearning_LLMPath_MockLessonsAppended` — mock returns 2 lessons → Store 에 저장됨 (Source="agent:llm:single").
- `TestApplyAgentLearning_LLMPath_FailClosed` — mock err → rule lesson 은 저장, LLM 결과 0.
- `TestApplyAgentLearning_RuleAndLLMParallel` — rule 1개 + LLM 2개 → Store 에 3개.

### 5.2 Phase 2 tests

**`internal/learning/extractor_anthropic_test.go`**:
- Mock `llm.Provider` 반환값으로 다양한 JSON 응답 케이스 파싱.
- Malformed JSON → parse error.
- JSON with schema violation (magnitude="huge") → graceful degradation (해당 lesson skip).
- Empty lessons array → 빈 slice 반환 (no error).
- Provider timeout → wrapped error.

**`internal/learning/breaker_test.go`**:
- Fresh breaker → `Allow() == true`.
- 5 consecutive fails → `Allow() == false`.
- Success clears failures.
- Fails outside window pruned.
- After pause duration → `Allow() == true` again.
- Concurrent Record (-race).

**`cmd/elnath/runtime_test.go`** 확장:
- `TestRuntime_LLMExtractionDisabled_NoCalls` — config 기본값에서 provider 호출 안 함.
- `TestRuntime_LLMExtractionEnabled_MockProvider` — config 활성화 + mock provider 주입 → lesson 저장 확인.

---

## 6. Scope boundaries

**In scope (Phase 1)**:
- Lesson 스키마 확장 + JSON 하위호환
- `LLMExtractor` 인터페이스 + MockLLMExtractor
- `ComplexityGate`
- `CursorStore`
- `PersonaDeltaFromHint` 매핑
- orchestrator/learning.go 의 LLM path (mock 만)
- config flag `llm_extraction.enabled` (default false)
- 위 tests

**In scope (Phase 2)**:
- `AnthropicExtractor` (Haiku 호출)
- `Breaker` + stats 노출
- CompactLessonSummary 실 구현
- Prompt engineering (system + user)
- Runtime/Daemon wiring with real provider
- `lessons stats` 에 LLM section
- 위 tests

**Out of scope** (모두 별도 phase 후보):
- Consolidation (autoDream 등가) — F-5.2
- Research path (`extractor.go` R1-R3) 의 LLM 확장 — F-5.3
- Prompt cache 최적화 (88% hit rate 목표) — F-5.4
- Subagent 별 LLM 호출 (현재는 team-level 1회만) — F-6
- Telegram shell 상시 open 세션의 idle-timeout trigger — F-5.1
- Wiki lesson export / reverse-ingest — F-6+
- `Evidence` 필드 실제 quote 추출 (현재 schema only)

---

## 7. Verification gates

### 7.1 Phase 1

```bash
cd /Users/stello/elnath
go vet ./internal/learning/... ./internal/orchestrator/... ./cmd/elnath/...
go test -race ./internal/learning/... ./internal/orchestrator/... ./cmd/elnath/...
make build
./elnath lessons stats  # 기존 출력 호환 + (LLM extraction: disabled) 라인
```

### 7.2 Phase 2

```bash
cd /Users/stello/elnath
go vet ./...
go test -race ./...
make build

# config 에 llm_extraction.enabled: true + Anthropic key 주입 후 dog-food 1 run
./elnath run
# 이후:
./elnath lessons stats
# "LLM extraction: enabled" 섹션 + "Last run: ... (ok)" 확인
./elnath lessons list --source agent:llm:  # LLM lesson 전체
./elnath lessons list --source agent:llm:single
```

**Manual breaker test**: Anthropic key 를 의도적으로 invalid 로 바꾸고 5회 agent run → stats 에 "Breaker: open" 확인 → 10분 후 자동 resume 확인.

---

## 8. Commit message template

Phase 1:
```
feat: phase F-5 Phase 1 LLM lesson extraction scaffolding

Schema + interface + mock + wiring. No real LLM calls yet.

- Lesson struct gains Rationale / Evidence / PersonaDirection
  (omitempty; backwards compatible with existing lessons.jsonl)
- LLMExtractor interface + MockLLMExtractor for tests and default
- ComplexityGate (msgs >= 5 AND has_tool_call)
- CursorStore (per-session last-processed line, append-only JSONL)
- PersonaDeltaFromHint (direction/magnitude -> numeric)
- orchestrator.applyAgentLearning: parallel rule + LLM paths,
  fail-closed on LLM error, SHA256 dedupe via Store.Append
- config flag llm_extraction.enabled (default false)
```

Phase 2:
```
feat: phase F-5 Phase 2 Anthropic Haiku lesson extractor

- AnthropicExtractor wires internal/llm provider with strict JSON
  schema and 1024-tok output cap. Haiku 4.5 default.
- Circuit breaker: 5 fails / 10m -> 10m pause. Status surfaced
  in `elnath lessons stats`.
- CompactLessonSummary: last 10 messages + tool-stats header,
  secret-redacted.
- Runtime/daemon wiring gated by config.llm_extraction.enabled.
- Response parsing tolerates malformed/partial JSON gracefully.
```

---

## 9. OpenCode prompts

- Phase 1: `docs/specs/PHASE-F5-PHASE1-OPENCODE-PROMPT.md`
- Phase 2: `docs/specs/PHASE-F5-PHASE2-OPENCODE-PROMPT.md` (Phase 1 완료 후 작성)

---

## 10. Critic red-team resolutions + deferred items

**Resolved in this revision** (from critic 2026-04-14):

- **C1 resolved**: `SelfState` 타입을 `*self.SelfState` 로 정정 (registry 실제 시그니처).
- **C2 resolved**: LLM 응답 schema 를 flat persona 필드로 변경 (`persona_param`, `persona_direction`, `persona_magnitude`). `applyPersonaHint` 가 `PersonaParam` 보고 `PersonaDelta` 슬라이스 synthesize.
- **C3 resolved**: `CompactSummary` callback 시그니처를 `func() (text string, lastLine int)` 로 확정. Phase 1 에서는 `("", 0)` 반환하고 `lastLine==0` 일 때 CursorStore.Update skip (다음 run 재처리).
- **C4 resolved**: 모델 ID 를 `claude-haiku-4-5-20251213` (registry alias) 로 통일. Phase 2 verification gate 에 `grep claude-haiku-4-5-20251213 internal/llm/usage.go` 추가.
- **H1 resolved**: `LearningDeps` 포인터는 workflow run 내부에서 shallow-copy 한 후 run-scoped 필드 mutate. 구현 프롬프트에서 pattern 명시.
- **H2 resolved**: Rule path 후 `SelfState.Save()` 즉시 호출 (LLM path panic 으로 rule 결과 손실 방지).
- **H5 resolved**: 구현 프롬프트에서 `internal/config/config.go` 의 `Normalize()` 함수에 default 주입 위치 anchor 지정.
- **H6 resolved**: Phase 1 에 간단한 fail counter (3회 연속 fail → process 내 LLM path 비활성화) 포함. Phase 2 에서 `Breaker` 로 교체.
- **M4 resolved**: `Store.Append` redactor 가 `Rationale` + `Evidence[]` 도 처리하도록 확장. Secret 유출 경로 봉쇄.

**Deferred to F-5.1** (critic HIGH but scope 외):

1. **H3 — CursorStore 저장소 교체**: append-only JSONL → SQLite table (`elnath.db`) 또는 in-memory map + write-through. 현 시점엔 lessons 0 bytes 라 압박 없음. F-5.1 에서 rotate / migrate.
2. **H4 — `Store.Recent(50)` 최적화**: 현재는 full load + sort. Rotation ceiling (5000 lessons / 1MB) 내에서 1회 run 당 1MB scan. `RecentIDs(n)` tail-read 로 교체 — F-5.1.

**Still open** (implementation 단계에서 결정):

3. **Manifest token-budget adaptive cap**: Q4-A 는 전체 주입이지만 `Recent(50)` 하드코드. 50 이상 쌓이면 LLM prompt 팽창 — F-5.1 에서 token-budget 기반 adaptive cap.
4. **Session 종료 없는 Telegram shell 세션**: Q3-A cursor 는 세션 종료 가정. Telegram shell 은 상시 open. Idle timeout / message count trigger → F-5.1.
