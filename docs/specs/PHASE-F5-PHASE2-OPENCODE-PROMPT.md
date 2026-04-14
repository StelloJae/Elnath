# Phase F-5 Phase 2 — OpenCode Prompt (Anthropic Haiku + Breaker + Compact Summary)

## Context

Elnath Phase F-5 Phase 1 (`c97d24f`) 은 schema + mock extractor + wiring 을 완성했다. Config flag `llm_extraction.enabled` 가 true 여도 현재는 `MockLLMExtractor{}` 만 주입되어 실제 LLM 호출이 일어나지 않는다. Phase 2 는 Anthropic Haiku 를 연결하고 Phase 1 의 임시 `FailCounter` 를 time-windowed `Breaker` 로 교체한다.

상세 spec: `docs/specs/PHASE-F5-LLM-LESSON-EXTRACTION.md` §4 (Phase 2 Implementation). Phase 1 구현 코드는 `internal/learning/` + `internal/orchestrator/learning.go` + `cmd/elnath/runtime.go` 참고.

설계 결정 (Q1-Q10 Phase 1 과 공유, locked):
- Q5 **A**: Haiku `claude-haiku-4-5-20251213` (registry alias at `internal/llm/registry.go:89`)
- Q10 **A+C**: fail-closed + time-windowed Breaker (5 fails / 10m → 10m pause)

## Scope

### 신규 파일 (3)
- `internal/learning/extractor_anthropic.go` — `AnthropicExtractor` + `buildExtractionPrompt` + `parseLessonResponse`
- `internal/learning/extractor_anthropic_test.go`
- `internal/learning/breaker.go` + `_test.go` — time-windowed circuit breaker

### 신규 또는 확장 파일 (1)
- `internal/conversation/compact_summary.go` (신규) 또는 `internal/conversation/context.go` 확장 — `CompactLessonSummary(msgs []Message, toolStats []agent.ToolStat) (text string, lastLine int)` 구현. 어느 쪽이든 helper 가 secret redactor 를 받아 함께 실행해야 함.

### 수정 파일 (~6)
- `internal/learning/lesson.go` — (변경 없을 가능성 크지만, Phase 2 에서 Breaker 상태를 lessons stats 에 노출하려고 필드 추가할지 검토. 현재 기준 변경 없음.)
- `internal/orchestrator/learning.go` — `LearningDeps.FailCounter` 필드 제거, `LearningDeps.Breaker *learning.Breaker` 추가. `applyAgentLearning` 에서 `FailCounter.Allow/Record` 호출을 `Breaker.Allow/Record` 로 교체.
- `cmd/elnath/runtime.go` — `cfg.LLMExtraction.Enabled == true && provider != nil` 시 `AnthropicExtractor` 주입 (기존 `MockLLMExtractor{}` 교체). `Breaker` 주입. Provider 선택: Anthropic provider 가 준비되면 사용, 없으면 mock 유지 + WARN 로그.
- `cmd/elnath/cmd_daemon.go` — runtime 과 동일.
- `cmd/elnath/cmd_lessons.go` — `lessons stats` 출력에 "LLM extraction:" 섹션 추가 (enabled/model/breaker status/last run timestamp).
- `internal/config/config.go` — 변경 없거나 `LLMExtractionConfig.Timeout` / `BreakerThreshold` 같은 선택 field. 기본값 유지 원칙 → 건드리지 않는 쪽 권장.

### 삭제 파일 (2)
- `internal/learning/fail_counter.go` — Breaker 가 대체.
- `internal/learning/fail_counter_test.go`.

## Task

### 1. `internal/learning/breaker.go` (신규)

Time-windowed circuit breaker. 10분 rolling window 에서 5회 연속 fail → 10분 pause. 성공 시 failures slice reset.

```go
package learning

import (
    "log/slog"
    "sync"
    "time"
)

// Breaker is a time-windowed circuit breaker for LLM extraction (Q10-C).
// 기본: 10분 window 내 5회 연속 실패 시 10분 pause. Phase 2 replaces Phase 1's
// FailCounter with time-aware semantics.
type Breaker struct {
    mu            sync.Mutex
    failures      []time.Time
    pauseUntil    time.Time
    windowSize    time.Duration
    failThreshold int
    pauseDur      time.Duration
    logger        *slog.Logger
}

type BreakerConfig struct {
    WindowSize    time.Duration // default 10 * time.Minute
    FailThreshold int           // default 5
    PauseDuration time.Duration // default 10 * time.Minute
}

func NewBreaker(logger *slog.Logger, cfg BreakerConfig) *Breaker { /* default 채우고 struct 반환 */ }

func (b *Breaker) Allow() bool { /* nil safe, pauseUntil 비교 */ }
func (b *Breaker) Record(err error) { /* err==nil → failures reset. err!=nil → append, prune window, threshold 도달 시 pauseUntil 세팅 + logger.Warn */ }

// Status 는 lessons stats 용 요약.
type BreakerStatus struct {
    Open        bool
    PauseUntil  time.Time
    RecentFails int  // 현재 window 내
    Threshold   int
}

func (b *Breaker) Status() BreakerStatus { /* snapshot */ }
```

**Test** (`breaker_test.go`):
- Fresh → Allow true, RecentFails 0.
- 5회 연속 Record(err) → Allow false, pauseUntil == now + 10m (tolerance 100ms).
- 중간에 Record(nil) (성공) → failures reset → Allow 계속 true.
- Failures outside 10m window pruned (fake clock 주입 필요 → Breaker 에 `now func() time.Time` 옵션 추가 권장, default `time.Now`).
- pauseUntil 지나면 Allow 다시 true.
- Concurrent Record with `-race`.
- Status() → 필드 값이 상태 반영.

### 2. `internal/learning/extractor_anthropic.go` (신규)

```go
package learning

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"

    "github.com/stello/elnath/internal/llm"
)

const defaultAnthropicExtractorModel = "claude-haiku-4-5-20251213"

type AnthropicExtractor struct {
    provider llm.Provider
    model    string
    // optional: logger, temperature override
}

func NewAnthropicExtractor(p llm.Provider, model string) *AnthropicExtractor {
    if model == "" { model = defaultAnthropicExtractorModel }
    return &AnthropicExtractor{provider: p, model: model}
}

func (a *AnthropicExtractor) Extract(ctx context.Context, req ExtractRequest) ([]Lesson, error) {
    system, user := buildExtractionPrompt(req)
    resp, err := a.provider.Chat(ctx, llm.ChatRequest{
        Model:       a.model,
        System:      system,
        Messages:    []llm.Message{{Role: "user", Content: user}},
        MaxTokens:   1024,
        Temperature: 0,
        EnableCache: true,
    })
    if err != nil {
        return nil, fmt.Errorf("anthropic extract: %w", err)
    }
    return parseLessonResponse(resp.Content, req)
}
```

**System prompt** (`buildExtractionPrompt` 의 system 부분 — stable, cache-friendly):

```
You extract lessons from agent runs for Elnath's learning store.
Output STRICT JSON matching the lessons schema. No commentary, no code fences.

Rules:
- 0-3 lessons per run. Prefer emitting none over forcing a weak lesson.
- lesson.text <= 200 chars. lesson.rationale <= 200 chars.
- Update-not-duplicate: if your finding already exists in the provided
  existing-lessons manifest (by topic+text similarity), do not emit.
- confidence: "high" only when evidence is direct and repeatable.
  "medium" otherwise. "low" rarely.
- persona_param MUST be one of: caution | persistence | verbosity | curiosity.
- persona_direction MUST be one of: increase | decrease | neutral.
- persona_magnitude MUST be one of: small | medium | large.
- Include persona fields only when a behavior change is clearly supported.
- Never emit absolute numeric delta values.

Schema (top-level):
{ "lessons": [ { "topic": string, "text": string, "rationale": string,
                 "evidence": [string] (optional), "confidence": "high"|"medium"|"low",
                 "persona_param": string (optional),
                 "persona_direction": string (optional),
                 "persona_magnitude": string (optional) } ] }
```

**User prompt** (per-run, concatenated):

```
## Existing lessons (recent 50)
{JSON array of {id, topic, text}}

## This run
Topic: {topic}
Workflow: {workflow}
Finish reason: {finish_reason}
Iterations: {iter}/{max_iter}
Retry count: {retry}

## Tool stats
{table: name | calls | errors | total_ms}

## Compact summary
{req.CompactSummary}

Return JSON only.
```

**`parseLessonResponse`**:
- `json.Unmarshal` into `{"lessons": [...]}` envelope. 실패 시 wrapped error.
- 각 entry 의 required fields 검증. 허용 값 enum 위반 (direction 이 "sideways" 등) → 해당 lesson skip (fail-soft), 나머지 유지.
- `lesson.text` / `lesson.rationale` 200 char 초과 → `truncate(...)` 적용 (기존 `extractor.go` 의 truncate 재사용).
- 결과에 `Source`, `Confidence` (enum 정규화), `PersonaParam/Direction/Magnitude` 매핑. `Created: time.Now().UTC()`.
- `applyPersonaHint` 는 orchestrator 층에서 실행 → 여기서는 raw fields 만 채움.

**Test** (`extractor_anthropic_test.go`):
- Mock `llm.Provider` 가 유효한 JSON 반환 → expected Lessons 파싱.
- Malformed JSON → parse error 반환 (nil lessons).
- JSON with schema violation (magnitude="huge") → 해당 lesson skip, 나머지 유지.
- Empty `lessons` array → 빈 slice + nil error.
- Provider timeout / ctx cancel → error wrapping.
- `text > 200` 자동 truncate 확인.
- `EnableCache=true` 가 provider 호출에 실제 전달되는지 spy 확인 (Mock provider 의 마지막 `ChatRequest` 검사).

### 3. `internal/conversation/compact_summary.go` (신규 또는 `context.go` 확장)

```go
// CompactLessonSummary returns a ~2K token compact view of an agent run for
// LLM lesson extraction input (Q2-B). Structure:
//
//   Tool stats:
//     bash: 7 calls / 0 errors / 1.2s
//     write_file: 3 calls / 1 error / 280ms
//   Last 10 messages:
//     [user] ... (200-char truncate)
//     [assistant] ...
//     [tool_result] ...
//
// Secret redactor 가 nil 이 아니면 각 content/tool name/stats 에 적용.
// lastLine 은 원본 session JSONL 의 최대 처리된 line 번호 (cursor 저장용).
func CompactLessonSummary(
    messages []Message,
    toolStats []ToolStatSummary,
    redact func(string) string,
) (text string, lastLine int)
```

`ToolStatSummary` 는 `internal/agent/agent.go` 의 `ToolStat` 또는 `internal/learning/agent_extractor.go` 의 `AgentToolStat` 중 가져오기 쉬운 쪽. Phase 1 `orchestrator/learning.go` 의 `toAgentToolStats` 패턴 참고.

**주의**:
- `lastLine` 은 caller 가 계산 가능한 값일 경우에만 반환. Session JSONL 파일 line 번호를 message 에 담지 않으면, conversation.Manager / daemon 이 별도로 추적 필요. Phase 2 에서는 runtime/daemon 이 `SessionID → lastLineAtExtraction` map 을 유지해서 callback 에서 반환.
- CompactSummary callback 시그니처는 Phase 1 에서 이미 `func() (text string, lastLine int)` 로 확정됨 (`internal/orchestrator/types.go`). Phase 2 는 실제 구현을 채우기만 함.

**Test** (`compact_summary_test.go`):
- 10 미만 msg → 전부 포함, truncate 없음.
- 10 초과 msg → 최근 10개만.
- 각 msg content 200 chars 초과 → truncate.
- `redact != nil` 이면 `AKIA...` 류 secret 치환 확인.
- `toolStats nil/empty` → "Tool stats: (none)" 또는 섹션 생략.
- `lastLine` 은 caller 가 전달한 기대값 반환.

### 4. `internal/orchestrator/learning.go` — FailCounter → Breaker 교체

```go
type LearningDeps struct {
    // ... 기존 ...
    Breaker *learning.Breaker  // H6 Phase 1 의 FailCounter 대체. nil 허용 (no breaker).
    // FailCounter 필드 제거.
}
```

`applyAgentLearning` 내부:
```go
// 기존:
if deps.FailCounter != nil && !deps.FailCounter.Allow() { ... }
// ...
if deps.FailCounter != nil { deps.FailCounter.Record(err) }

// 변경:
if deps.Breaker != nil && !deps.Breaker.Allow() {
    log.Debug("llm lesson: breaker open", "session_id", deps.SessionID)
    return
}
// ...
if deps.Breaker != nil {
    deps.Breaker.Record(err)
}
```

**Phase 1 테스트 갱신**:
- `TestApplyAgentLearning_LLMPath_FailCounter*` → `TestApplyAgentLearning_LLMPath_Breaker*` 로 rename + fake clock 사용.

### 5. `cmd/elnath/runtime.go` Phase 2 wiring

```go
var llmExtractor learning.LLMExtractor
var breaker *learning.Breaker

if cfg.LLMExtraction.Enabled {
    // Provider 선택: anthropic 이 설정된 경우 real, 아니면 mock.
    if anthropicProvider != nil {  // anthropicProvider 는 runtime 에서 이미 llm.Provider 로 인스턴스화 되어있을 것
        llmExtractor = learning.NewAnthropicExtractor(anthropicProvider, cfg.LLMExtraction.Model)
        logger.Info("llm lesson: anthropic extractor enabled", "model", cfg.LLMExtraction.Model)
    } else {
        llmExtractor = &learning.MockLLMExtractor{}
        logger.Warn("llm lesson: enabled but no anthropic provider — falling back to mock")
    }
    breaker = learning.NewBreaker(logger, learning.BreakerConfig{})  // defaults
}
```

`LearningDeps` 주입에서 `FailCounter: failCounter` 를 `Breaker: breaker` 로 교체.

**CompactSummary callback** 도 Phase 2 에서 실제 구현으로 교체:
```go
// Phase 1 placeholder:
// deps.CompactSummary = func() (string, int) { return "", 0 }

// Phase 2:
deps.CompactSummary = func() (string, int) {
    return conversation.CompactLessonSummary(
        currentSession.Messages(),
        currentSession.ToolStats(),
        secretDetector.RedactString,
    )
}
```

정확한 접근 경로는 `internal/orchestrator/single.go` 의 Run 시점에 이미 session 에 접근 가능한지에 따라 조정. Phase 1 에서는 single.go 가 `func() (string, int) { return "", 0 }` 를 주입했으므로 Phase 2 는 그 자리에 실 구현 주입.

### 6. `cmd/elnath/cmd_daemon.go` Phase 2 wiring

runtime.go 와 동일 패턴.

### 7. `cmd/elnath/cmd_lessons.go` — stats 출력 확장

`lessonsStats` 내부, 기존 "By source:" 섹션 뒤 또는 맨 아래에 추가:

```go
if stats.LLMExtraction != nil {
    fmt.Println()
    fmt.Println("LLM extraction:")
    if stats.LLMExtraction.Enabled {
        fmt.Printf("  Enabled (model=%s)\n", stats.LLMExtraction.Model)
    } else {
        fmt.Println("  Disabled")
    }
    if stats.LLMExtraction.Breaker != nil {
        bs := stats.LLMExtraction.Breaker
        if bs.Open {
            fmt.Printf("  Breaker: OPEN (resumes %s)\n", bs.PauseUntil.Format(time.RFC3339))
        } else {
            fmt.Printf("  Breaker: closed (%d/%d fails in window)\n", bs.RecentFails, bs.Threshold)
        }
    }
    if !stats.LLMExtraction.LastRun.IsZero() {
        fmt.Printf("  Last run: %s\n", stats.LLMExtraction.LastRun.UTC().Format(time.RFC3339))
    }
}
```

`Stats` struct (`internal/learning/store.go`) 확장:
```go
type Stats struct {
    // ... 기존 ...
    LLMExtraction *LLMStatsSnapshot `json:"llm_extraction,omitempty"`
}

type LLMStatsSnapshot struct {
    Enabled bool            `json:"enabled"`
    Model   string          `json:"model,omitempty"`
    Breaker *BreakerStatus  `json:"breaker,omitempty"`
    LastRun time.Time       `json:"last_run,omitempty"`
}
```

이 snapshot 은 `Summary()` 에서는 채우지 않고 (store 가 runtime 상태를 모름), caller (`cmd/elnath/cmd_lessons.go`) 에서 runtime 의 breaker + config 을 직접 읽어 `stats.LLMExtraction` 을 채운 뒤 출력.

또는 더 간단: `lessonsStats` 내부에서 runtime 에 `llmStatus` getter 를 호출해서 바로 출력 (Stats struct 변경 없이). JSON 출력 호환 고려 시 Stats 확장이 깔끔.

**권장**: Stats 확장 + JSON tag `llm_extraction,omitempty`. Phase 1 의 F-4 패턴 일관성.

### 8. FailCounter 제거

- `internal/learning/fail_counter.go` 삭제
- `internal/learning/fail_counter_test.go` 삭제
- `internal/orchestrator/learning.go` 및 test 에서 `FailCounter` 참조 전부 `Breaker` 로
- `cmd/elnath/runtime.go` / `cmd_daemon.go` wiring 갱신

## Tests

### 신규

- `internal/learning/breaker_test.go` (위 §1)
- `internal/learning/extractor_anthropic_test.go` (위 §2)
- `internal/conversation/compact_summary_test.go` (위 §3)

### 갱신

- `internal/orchestrator/learning_test.go` — FailCounter 기반 테스트 → Breaker 기반으로 변환. 예: `TestApplyAgentLearning_LLMPath_BreakerOpenSkips`.
- `cmd/elnath/runtime_test.go` — `TestExecutionRuntimeLearningDepsLLMEnabled` 를 Breaker 주입으로 수정 + anthropic provider 없을 때 mock 유지 확인 테스트 추가.
- `cmd/elnath/cmd_lessons_test.go` — `TestLessonsStats_LLMSectionEnabled` + `TestLessonsStats_LLMSectionDisabled` + `TestLessonsStats_LLMBreakerOpen` 세 케이스.

## Constraints

- **Schema 변경 최소화**: Phase 1 Lesson struct 는 그대로. `Stats` 만 `LLMExtraction` optional field 추가 (json omitempty — 기존 JSON consumer 호환).
- **Provider fallback**: `cfg.LLMExtraction.Enabled==true` 인데 Anthropic provider 인스턴스화 실패 (API key 없음 등) 하면 mock 유지 + WARN 로그. 에러로 startup 막지 말 것.
- **Breaker nil-safe**: `deps.Breaker == nil` 이면 모든 run 허용. 테스트 커버.
- **CompactSummary 비용**: callback 안에서 대용량 JSONL 읽지 말 것. Run 종료 시점의 in-memory message array + tool stats 만 사용. `lastLine` 계산도 in-memory 로 해결.
- **Context propagation**: AnthropicExtractor 의 `Extract(ctx, ...)` 는 caller 가 30s WithTimeout 으로 묶은 ctx 를 받음. provider.Chat 내부에서 ctx 존중하는지 확인.
- **Cache strategy**: `llm.ChatRequest{EnableCache: true}` 세팅. System prompt + manifest 가 안정적이라 cache hit 기대. Provider 구현이 cache_control 을 Anthropic header 로 전달하는지 확인 (grep `EnableCache` 처리 경로).
- **Determinism**: `Temperature: 0` 고정. Tests 는 Mock provider 로 결정론 확보.
- **Prompt injection 방어**: User prompt 의 `CompactSummary` 는 이미 redactor 처리됨. System prompt 는 "Output STRICT JSON" 고정 → fence 거부. ToolStats 이름은 enum 검증 생략 (raw tool name 신뢰).
- **Rate / cost**: 30s timeout, MaxTokens 1024. 1 run 평균 ~2K input + ~300 output = ~$0.0047 at Haiku ($1/$5 MTok). 50 runs/day = $7/월. 예상 범위.
- **Migration**: 기존 lessons.jsonl 마이그레이션 불필요 (0 bytes). Phase 1 이 이미 schema 확장.
- **FailCounter 제거 시 git rm**: 파일 삭제는 `git rm internal/learning/fail_counter.go internal/learning/fail_counter_test.go`. OpenCode 가 `rm` 만 하고 `git add` 안 하면 pre-commit 단계에서 stello 가 수동 처리.

## Verification gates

```bash
cd /Users/stello/elnath
go vet ./internal/learning/... ./internal/orchestrator/... ./internal/conversation/... ./cmd/elnath/...
go test -race ./internal/learning/... ./internal/orchestrator/... ./internal/conversation/... ./cmd/elnath/...
make build

# Smoke: config enabled + no anthropic key → mock fallback
./elnath lessons stats
# 기대: "LLM extraction: Enabled (model=claude-haiku-4-5-20251213)" + "Breaker: closed"
# (mock provider 라 실제 LLM 호출 없음)

# Smoke: agent run 후 lesson 저장 확인 (사용자 선택)
./elnath run  # 짧은 코딩 task
./elnath lessons list --source agent:llm:single
```

전부 exit 0 + 기존 테스트 regression 0.

**추가 수동 검증**: Anthropic key 를 의도적으로 invalid 하게 만들고 5회 agent run → `lessons stats` 에 "Breaker: OPEN" 확인 → 10분 후 자동 closed.

## Scope limits

- Consolidation pass (autoDream 등가) 금지 — F-5.2.
- Research path (`extractor.go` R1-R3) 확장 금지 — F-5.3.
- Subagent 별 LLM 호출 금지 — team/ralph/autopilot 은 Phase 1 상태 유지.
- Telegram shell idle-timeout cursor trigger 금지 — F-5.1.
- Wiki lesson export 금지 — F-6+.
- Rule A-E 로직 건드리지 말 것.
- `deriveID` 변경 금지 — Phase 1 확정.
- `Lesson` struct 에 새 필드 추가 금지 — Phase 1 확정.
- OpenAI / Ollama provider 확장 금지 — Anthropic 만 Phase 2.
- Prompt cache hit-rate tuning 금지 — F-5.4 후보.

## 완료 보고 형식

작업 종료 시:
1. 수정/추가/삭제 파일 목록 (신규 4 + 수정 ~6 + 삭제 2)
2. `go test -race ./internal/learning/... ./internal/orchestrator/... ./internal/conversation/... ./cmd/elnath/...` PASS 요약 (신규 테스트 개수)
3. `go vet` + `make build` 결과
4. `./elnath lessons stats` 출력 확인 (LLM extraction 섹션 포함)
5. 예상 commit message (spec §8 Phase 2 템플릿 사용)

커밋은 하지 마라. stello 가 직접 commit.

## Open items (구현 중 질문 생기면 보고)

- `anthropicProvider` 가 `cmd/elnath/runtime.go` 에서 이미 인스턴스화 되어있는 변수명이 무엇인지 확인 (grep `llm.Anthropic` 또는 `anthropic.New`). 아니면 추가 생성.
- `BreakerConfig` 를 `internal/config/config.go` 의 `LLMExtractionConfig` 에 노출할지 (yaml flag `window_size_min`, `fail_threshold`, `pause_min`). 기본값 hardcode 유지 가능 → 스코프 최소화.
- `CompactLessonSummary` 가 session JSONL 의 line 번호를 받을 수 있는 API 가 `conversation.Manager` 에 있는지 확인. 없으면 placeholder 로 `len(messages)` 반환하고 lastLine 의미 재정의 필요.
- `Stats.LLMExtraction` snapshot 을 `Summary()` 가 채울지, caller (cmd_lessons) 가 post-facto 채울지 결정. 후자 권장 (Store 가 runtime 상태 모름).
