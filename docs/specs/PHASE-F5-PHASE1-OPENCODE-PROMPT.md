# Phase F-5 Phase 1 — OpenCode Prompt (LLM Lesson Extraction Scaffolding)

## Context

Elnath 는 Go 로 만든 자율 AI 비서 daemon (`/Users/stello/elnath/`, 브랜치 `feat/telegram-redesign`). Phase F-3.2 에서 rule-based lesson 추출이 `orchestrator.applyAgentLearning` 공통 hook 으로 통합됨. Phase F-5 는 rule 이 놓치는 패턴 (특히 **성공 패턴 학습**) 을 LLM pass 로 보완한다.

**Phase 1 scope**: 스키마 확장 + 인터페이스 + Mock 구현 + wiring. **실제 Anthropic provider 호출은 Phase 2** — Phase 1 에서는 MockLLMExtractor 만 사용하며 config flag 기본값 `false`.

상세 spec: `docs/specs/PHASE-F5-LLM-LESSON-EXTRACTION.md` (§3 Phase 1 구체 구현 읽을 것).

설계 결정 (Q1-Q10 locked):
- Q1 **B**: complexity-gated (`msgs ≥ 5 AND has_tool_call`)
- Q3 **A**: cursor tracking (per-session last-processed line, append-only JSONL)
- Q6 **B**: rule + LLM parallel, Store SHA256 dedupe
- Q7 **B**: Lesson 스키마 확장 (Rationale/Evidence/PersonaDirection/PersonaMagnitude, 전부 omitempty)
- Q8 **B**: LLM 은 direction+magnitude 정성 힌트, 코드가 수치 매핑 (small=0.01 / medium=0.03 / large=0.06)
- Q10 **A** (Phase 1): fail-closed — LLM error 시 rule 결과는 보존. Breaker 는 Phase 2.

## Scope

### 신규 파일 (5)
- `internal/learning/extractor_llm.go` — `LLMExtractor` interface + `ExtractRequest` + `LessonManifestEntry` + `MockLLMExtractor`
- `internal/learning/persona_mapping.go` — `PersonaDeltaFromHint(direction, magnitude) float64`
- `internal/learning/complexity.go` — `ComplexityGate` + `DefaultComplexityGate`
- `internal/learning/cursor.go` — `CursorStore` (append-only JSONL)
- `internal/learning/fail_counter.go` — `FailCounter` (H6 Phase 1 가드)

### 신규 테스트 파일 (5)
- `internal/learning/extractor_llm_test.go`
- `internal/learning/persona_mapping_test.go`
- `internal/learning/complexity_test.go`
- `internal/learning/cursor_test.go`
- `internal/learning/fail_counter_test.go`

### 수정 파일 (6+)
- `internal/learning/lesson.go` — 5개 필드 추가 (omitempty, `PersonaParam` 포함 — C2 fix)
- `internal/learning/store.go` — `Append` redactor 가 `Rationale`/`Evidence[]` 도 처리 (M4 fix)
- `internal/orchestrator/learning.go` — `LearningDeps` 확장 + `applyAgentLearning` 의 LLM path (H2: rule path 후 즉시 save)
- `internal/orchestrator/single.go` — `applyAgentLearning` 호출 직전 shallow-copy 후 run-scoped mutate (H1 per-run copy)
- `internal/orchestrator/learning_test.go` (또는 기존 test 파일 해당 위치) — 5+ 테스트 추가
- `cmd/elnath/runtime.go` — CursorStore/FailCounter 생성 + 기본 Mock 주입 + config flag 연결
- `cmd/elnath/cmd_daemon.go` — runtime 과 동일 wiring
- `internal/config/config.go` — `LLMExtractionConfig` + `Normalize()` default 주입 (H5 anchor)

## Task

### 1. `internal/learning/lesson.go` 스키마 확장

기존 Lesson struct 에 필드 5개 추가. **모두 `omitempty` — 기존 lessons.jsonl 하위호환 필수**:

```go
type Lesson struct {
    ID               string        `json:"id"`
    Text             string        `json:"text"`
    Topic            string        `json:"topic,omitempty"`
    Source           string        `json:"source"`
    Confidence       string        `json:"confidence"`
    PersonaDelta     []self.Lesson `json:"persona_delta,omitempty"`
    Rationale        string        `json:"rationale,omitempty"`
    Evidence         []string      `json:"evidence,omitempty"`
    PersonaParam     string        `json:"persona_param,omitempty"`      // C2 fix: target param name
    PersonaDirection string        `json:"persona_direction,omitempty"`
    PersonaMagnitude string        `json:"persona_magnitude,omitempty"`
    Created          time.Time     `json:"created"`
}
```

`deriveID` 시그니처 / 로직 건드리지 말 것. ID 는 여전히 `Text` SHA256 — `Rationale` 등 신규 필드는 ID 계산에 포함되지 않음 (중복 판정은 핵심 text 기준 유지).

**추가 M4 fix**: `internal/learning/store.go` 의 `Append` 에서 redactor 호출 라인을 확장. 현재는 `Text`/`Topic`/`Source` 3개만 redact — `Rationale` + `Evidence[i]` 도 포함:

```go
if s.redactor != nil {
    lesson.Text = s.redactor(lesson.Text)
    lesson.Topic = s.redactor(lesson.Topic)
    lesson.Source = s.redactor(lesson.Source)
    lesson.Rationale = s.redactor(lesson.Rationale)  // NEW
    for i := range lesson.Evidence {                 // NEW
        lesson.Evidence[i] = s.redactor(lesson.Evidence[i])
    }
}
```

이 수정은 ID 계산 (`Text` SHA256) 전에 일어나야 하므로 기존 redactor 블록 바로 확장. Test 추가: `TestStoreAppend_RedactsRationaleAndEvidence`.

### 2. `internal/learning/extractor_llm.go` (신규)

```go
package learning

import "context"

// LLMExtractor produces lessons from an agent run via LLM. Phase 1 ships a
// mock; Phase 2 wires an Anthropic Haiku provider.
type LLMExtractor interface {
    Extract(ctx context.Context, req ExtractRequest) ([]Lesson, error)
}

type ExtractRequest struct {
    SessionID       string
    Topic           string
    Workflow        string
    CompactSummary  string
    ToolStats       []AgentToolStat
    FinishReason    string
    Iterations      int
    MaxIterations   int
    RetryCount      int
    ExistingLessons []LessonManifestEntry
    SinceLine       int
}

type LessonManifestEntry struct {
    ID    string
    Topic string
    Text  string
}

// MockLLMExtractor returns a pre-configured list (or error). Used as the
// default until Phase 2 and in tests. Returns a defensive copy to prevent
// callers from mutating the fixture.
type MockLLMExtractor struct {
    Lessons []Lesson
    Err     error
}

func (m *MockLLMExtractor) Extract(ctx context.Context, _ ExtractRequest) ([]Lesson, error) {
    if m == nil {
        return nil, nil
    }
    if m.Err != nil {
        return nil, m.Err
    }
    out := make([]Lesson, len(m.Lessons))
    copy(out, m.Lessons)
    return out, nil
}
```

### 3. `internal/learning/persona_mapping.go` (신규)

```go
package learning

import "strings"

// PersonaDeltaFromHint converts qualitative direction+magnitude into a numeric
// delta. Unknown values return 0. Q8-B spec.
//
// magnitude: small=0.01, medium=0.03, large=0.06
// direction: increase=+, decrease=-, neutral=0
func PersonaDeltaFromHint(direction, magnitude string) float64 {
    var base float64
    switch strings.ToLower(strings.TrimSpace(magnitude)) {
    case "small":
        base = 0.01
    case "medium":
        base = 0.03
    case "large":
        base = 0.06
    default:
        return 0
    }
    switch strings.ToLower(strings.TrimSpace(direction)) {
    case "increase":
        return base
    case "decrease":
        return -base
    case "neutral":
        return 0
    default:
        return 0
    }
}
```

### 4. `internal/learning/complexity.go` (신규)

```go
package learning

// ComplexityGate decides whether a run is "complex enough" to warrant LLM
// extraction. Q1-B: msgs >= MinMessages AND (not RequireToolCall OR toolCalls > 0).
type ComplexityGate struct {
    MinMessages     int
    RequireToolCall bool
}

var DefaultComplexityGate = ComplexityGate{MinMessages: 5, RequireToolCall: true}

func (g ComplexityGate) ShouldExtract(msgCount, toolCalls int) bool {
    if msgCount < g.MinMessages {
        return false
    }
    if g.RequireToolCall && toolCalls <= 0 {
        return false
    }
    return true
}
```

### 4.1 `internal/learning/fail_counter.go` (신규, H6)

Phase 1 최소 보호장치 — 3회 연속 fail 이후 process 내 LLM path 비활성화. Phase 2 에서 full `Breaker` 로 교체. 성공 시 counter reset.

```go
package learning

import "sync"

// FailCounter is a minimal process-local guard for Phase 1. After
// consecutive-fail threshold, Allow() returns false permanently within the
// process. Phase 2 replaces with a time-windowed Breaker.
type FailCounter struct {
    mu            sync.Mutex
    threshold     int
    consecutive   int
    disabled      bool
}

func NewFailCounter(threshold int) *FailCounter {
    if threshold <= 0 {
        threshold = 3
    }
    return &FailCounter{threshold: threshold}
}

func (f *FailCounter) Allow() bool {
    if f == nil {
        return true
    }
    f.mu.Lock()
    defer f.mu.Unlock()
    return !f.disabled
}

func (f *FailCounter) Record(err error) {
    if f == nil {
        return
    }
    f.mu.Lock()
    defer f.mu.Unlock()
    if err == nil {
        f.consecutive = 0
        return
    }
    f.consecutive++
    if f.consecutive >= f.threshold {
        f.disabled = true
    }
}
```

Test (`fail_counter_test.go`): fresh → Allow true / 3회 fail → Allow false / success 후 reset 되는지 (실제로는 한 번 disabled 되면 회복 안 되는 것이 정상 — counter reset 은 threshold 도달 전 success 시에만) / concurrent Record (`-race`).

### 5. `internal/learning/cursor.go` (신규)

Append-only JSONL. 각 줄은 `{"session_id": "...", "last_line": N, "updated_at": "..."}`. Get 은 파일 전체를 스캔해서 같은 `session_id` 의 최신 레코드를 반환.

```go
package learning

import (
    "bufio"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "sync"
    "time"
)

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

// Get returns the latest last_line for the given session, or 0 if not found.
func (c *CursorStore) Get(sessionID string) (int, error) {
    if c == nil || c.path == "" || sessionID == "" {
        return 0, nil
    }
    c.mu.Lock()
    defer c.mu.Unlock()

    f, err := os.Open(c.path)
    if err != nil {
        if os.IsNotExist(err) {
            return 0, nil
        }
        return 0, fmt.Errorf("cursor store: open: %w", err)
    }
    defer f.Close()

    latest := 0
    scanner := bufio.NewScanner(f)
    scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
    for scanner.Scan() {
        var rec cursorRecord
        if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
            continue // skip malformed lines, don't fail
        }
        if rec.SessionID == sessionID && rec.LastLine > latest {
            latest = rec.LastLine
        }
    }
    if err := scanner.Err(); err != nil {
        return 0, fmt.Errorf("cursor store: scan: %w", err)
    }
    return latest, nil
}

// Update appends a new cursor record.
func (c *CursorStore) Update(sessionID string, lastLine int) error {
    if c == nil || c.path == "" || sessionID == "" {
        return nil
    }
    c.mu.Lock()
    defer c.mu.Unlock()

    if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
        return fmt.Errorf("cursor store: mkdir: %w", err)
    }
    f, err := os.OpenFile(c.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
    if err != nil {
        return fmt.Errorf("cursor store: open: %w", err)
    }
    defer f.Close()

    rec := cursorRecord{
        SessionID: sessionID,
        LastLine:  lastLine,
        UpdatedAt: time.Now().UTC(),
    }
    if err := json.NewEncoder(f).Encode(rec); err != nil {
        return fmt.Errorf("cursor store: encode: %w", err)
    }
    return nil
}
```

### 6. `internal/orchestrator/learning.go` 확장

**`LearningDeps` 확장** (C1 fix: `*self.SelfState`, C3 fix: CompactSummary 시그니처):
```go
type LearningDeps struct {
    Store     *learning.Store
    SelfState *self.SelfState         // C1: 실제 타입명 (registry *self.SelfState)
    Logger    *slog.Logger
    // Phase 1 신규 (전부 optional — nil 허용):
    LLMExtractor   learning.LLMExtractor
    CursorStore    *learning.CursorStore
    ComplexityGate learning.ComplexityGate
    // Run-scoped fields (매 workflow invocation 마다 shallow-copy 후 채움 — H1):
    SessionID      string
    MessageCount   int
    ToolCallCount  int
    // CompactSummary 는 (text, lastLine) 튜플 반환. lastLine 은 session JSONL 의 마지막
    // 처리된 line 번호 (cursor 저장용). MessageCount 와 혼동 금지 — C3.
    CompactSummary func() (text string, lastLine int)
    // Phase 1 최소 fail-guard (H6): 3회 연속 fail 시 process 내 LLM path 비활성화.
    // Phase 2 에서 Breaker 로 교체. nil 허용.
    FailCounter    *learning.FailCounter
}
```

**H1 per-run copy 패턴** (shared pointer mutation 금지):

`internal/orchestrator/single.go` (및 향후 team/ralph/autopilot) 에서 `applyAgentLearning` 호출 전:

```go
// input.Learning 은 runtime/daemon 에서 주입된 shared pointer.
// Run-scoped fields 를 mutate 해야 하므로 반드시 shallow-copy 후 로컬 주소 전달.
if input.Learning != nil {
    deps := *input.Learning          // H1: shallow copy
    deps.SessionID = sessionID
    deps.MessageCount = messageCount
    deps.ToolCallCount = totalCalls(info.ToolStats)
    deps.CompactSummary = func() (string, int) { return "", 0 }  // Phase 1 placeholder
    applyAgentLearning(&deps, info)
}
```

**기존 `applyAgentLearning`** 끝에 LLM path 추가 (rule path 뒤에):

```go
func applyAgentLearning(deps *LearningDeps, info learning.AgentResultInfo) {
    if deps == nil || deps.Store == nil {
        return
    }
    log := deps.Logger
    if log == nil { log = slog.Default() }

    // --- 기존 Rule path (변경 없음) ---
    ruleLessons := learning.ExtractAgent(info)
    ruleChanged := appendAndApply(deps, log, ruleLessons)

    // H2: Rule path persona 변경은 즉시 save. LLM path 에서 panic 나도 rule 결과 보존.
    if ruleChanged && deps.SelfState != nil {
        if err := deps.SelfState.Save(); err != nil {
            log.Warn("agent learning: selfState save (rule) failed", "error", err)
        }
    }

    // --- Phase 1 LLM path ---
    if deps.LLMExtractor == nil {
        return
    }
    if !deps.ComplexityGate.ShouldExtract(deps.MessageCount, deps.ToolCallCount) {
        return
    }
    // H6: 간단 fail counter — 3회 연속 fail 후 process 내 LLM 비활성.
    if deps.FailCounter != nil && !deps.FailCounter.Allow() {
        log.Debug("llm lesson: fail counter open, skip", "session_id", deps.SessionID)
        return
    }

    since := 0
    if deps.CursorStore != nil {
        since, _ = deps.CursorStore.Get(deps.SessionID)
    }

    summary := ""
    lastLine := 0
    if deps.CompactSummary != nil {
        summary, lastLine = deps.CompactSummary()
    }

    req := learning.ExtractRequest{
        SessionID:       deps.SessionID,
        Topic:           info.Topic,
        Workflow:        info.Workflow,
        CompactSummary:  summary,
        ToolStats:       info.ToolStats,
        FinishReason:    info.FinishReason,
        Iterations:      info.Iterations,
        MaxIterations:   info.MaxIterations,
        RetryCount:      info.RetryCount,
        ExistingLessons: buildLessonManifest(deps.Store, 50),
        SinceLine:       since,
    }

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    llmLessons, err := deps.LLMExtractor.Extract(ctx, req)
    cancel()

    if deps.FailCounter != nil {
        deps.FailCounter.Record(err)
    }
    if err != nil {
        log.Warn("llm lesson: extract failed", "error", err, "session_id", deps.SessionID)
        return // fail-closed: rule 결과는 이미 저장됨 (Q10-A)
    }

    for i := range llmLessons {
        llmLessons[i].Source = llmSourceFor(info.Workflow)
        applyPersonaHint(&llmLessons[i])
    }
    llmChanged := appendAndApply(deps, log, llmLessons)

    // C3 fix: lastLine > 0 일 때만 cursor update. Phase 1 mock 은 0 반환 → skip.
    if deps.CursorStore != nil && lastLine > 0 {
        if err := deps.CursorStore.Update(deps.SessionID, lastLine); err != nil {
            log.Warn("llm lesson: cursor update failed", "error", err)
        }
    }
    if llmChanged && deps.SelfState != nil {
        if err := deps.SelfState.Save(); err != nil {
            log.Warn("agent learning: selfState save (llm) failed", "error", err)
        }
    }
}

// appendAndApply returns true if any persona delta was applied.
func appendAndApply(deps *LearningDeps, log *slog.Logger, lessons []learning.Lesson) bool {
    personaChanged := false
    for _, lesson := range lessons {
        if err := deps.Store.Append(lesson); err != nil {
            log.Warn("agent learning: append failed", "error", err)
            continue
        }
        if deps.SelfState != nil && len(lesson.PersonaDelta) > 0 {
            deps.SelfState.ApplyLessons(lesson.PersonaDelta)
            personaChanged = true
        }
    }
    return personaChanged
}

func buildLessonManifest(store *learning.Store, maxEntries int) []learning.LessonManifestEntry {
    if store == nil {
        return nil
    }
    recent, err := store.Recent(maxEntries)
    if err != nil {
        return nil
    }
    out := make([]learning.LessonManifestEntry, 0, len(recent))
    for _, l := range recent {
        out = append(out, learning.LessonManifestEntry{
            ID: l.ID, Topic: l.Topic, Text: l.Text,
        })
    }
    return out
}

func llmSourceFor(workflow string) string {
    if workflow == "" {
        return "agent:llm"
    }
    return "agent:llm:" + workflow
}

// C2 fix: LLM 응답의 flat persona 필드 (PersonaParam/Direction/Magnitude) 를
// PersonaDelta 슬라이스로 synthesize. 기존 Rule 이 이미 채운 슬라이스가 있으면 해당
// 행의 Delta 만 덮어씀 (Param 은 건드리지 않음). 슬라이스가 비어있고 PersonaParam 이
// 있으면 새 엔트리 생성. 이전 구현의 chicken-and-egg (빈 슬라이스 → 매핑 no-op) 해결.
func applyPersonaHint(l *learning.Lesson) {
    if l.PersonaDirection == "" || l.PersonaMagnitude == "" {
        return
    }
    delta := learning.PersonaDeltaFromHint(l.PersonaDirection, l.PersonaMagnitude)
    if delta == 0 {
        return
    }
    if len(l.PersonaDelta) == 0 {
        if l.PersonaParam == "" {
            return // 방향/크기만 있고 param 없음 → 적용 대상 불명 → skip
        }
        l.PersonaDelta = []self.Lesson{{Param: l.PersonaParam, Delta: delta}}
        return
    }
    for i := range l.PersonaDelta {
        if l.PersonaDelta[i].Param == "" && l.PersonaParam != "" {
            l.PersonaDelta[i].Param = l.PersonaParam
        }
        l.PersonaDelta[i].Delta = delta
    }
}
```

기존 rule path 함수 (`applyAgentLearning` 의 리팩터 이전 버전) 는 위 코드로 완전 교체. `personaChanged` 를 두 번 체크하지 말 것 — 한 곳에서 합산.

**Import 추가**: `context`, `time`.

### 7. `internal/config/config.go` 확장

기존 Config struct 에 `LLMExtraction` 섹션 추가 (yaml tag 예시):

```go
type LLMExtractionConfig struct {
    Enabled     bool   `yaml:"enabled"`
    Model       string `yaml:"model"`        // Phase 2 에서 사용
    MinMessages int    `yaml:"min_messages"`
}
```

Config struct 필드:
```go
LLMExtraction LLMExtractionConfig `yaml:"llm_extraction"`
```

**H5 anchor — defaults 주입 위치 명시**:

1. 먼저 `internal/config/config.go` 전체를 read. `Normalize()` / `applyDefaults()` / `Defaults()` 등 기본값 채우는 함수 찾을 것. 기존 sub-config (Daemon/Telegram/Research) 가 어디서 default 받는지 pattern 확인.
2. 그 함수 안에 아래 3줄 추가 (해당 함수 없으면 신규 `normalizeLLMExtraction()` 만들고 YAML parse 직후에 호출):

```go
if cfg.LLMExtraction.MinMessages == 0 {
    cfg.LLMExtraction.MinMessages = 5
}
if cfg.LLMExtraction.Model == "" {
    cfg.LLMExtraction.Model = "claude-haiku-4-5-20251213"
}
// Enabled 는 zero value (false) 가 곧 desired default — 변환 불필요.
```

3. 기존 config test (`internal/config/config_test.go`) 에서 `llm_extraction:` 섹션 전혀 없는 YAML 로 load 했을 때 MinMessages=5, Model="claude-haiku-4-5-20251213" 되는지 case 추가.
4. 기존 YAML 파서가 unknown key 에 error 내는지 확인 — 만약 strict 모드면 기존 설치 config.yaml (llm_extraction 없음) 는 regress 없음 (누락 OK). unknown key 는 어차피 발생 안 하지만 예방적 확인.

### 8. `cmd/elnath/runtime.go` wiring

기존 `learningStore` 생성 블록 (runtime.go:217 근처) 에 CursorStore 와 LLMExtractor 추가:

```go
learningPath := filepath.Join(cfg.DataDir, "lessons.jsonl")
learningStore := learning.NewStore(learningPath, learning.WithRedactor(secretDetector.RedactString))

cursorStore := learning.NewCursorStore(filepath.Join(cfg.DataDir, "lesson_cursors.jsonl"))

var llmExtractor learning.LLMExtractor
var failCounter *learning.FailCounter
if cfg.LLMExtraction.Enabled {
    // Phase 1: mock 만. Phase 2 에서 Anthropic 실제 주입.
    llmExtractor = &learning.MockLLMExtractor{}
    failCounter = learning.NewFailCounter(3) // H6: process-local guard
}
```

기존 `LearningDeps` (또는 그에 상응하는 주입 구조) 에 전달:
```go
deps := &orchestrator.LearningDeps{
    Store:          learningStore,
    SelfState:      selfState,     // C1: *self.SelfState 타입
    Logger:         logger,
    LLMExtractor:   llmExtractor,
    CursorStore:    cursorStore,
    ComplexityGate: learning.ComplexityGate{
        MinMessages:     cfg.LLMExtraction.MinMessages,
        RequireToolCall: true,
    },
    FailCounter:    failCounter,
    // SessionID / MessageCount / ToolCallCount / CompactSummary 는
    // 각 workflow 가 Run 시점에 shallow-copy 후 채워서 전달 (H1).
    // 여기서는 zero value.
}
```

**중요**: SessionID / MessageCount / ToolCallCount / CompactSummary 는 **run-scoped** 이라 여기서 정적으로 주입 불가. 각 workflow (`single.go` / `team.go` / `ralph.go` / `autopilot.go`) 가 `applyAgentLearning` 호출 직전에 `deps` 복사본 만들어 채움. Phase 1 에서는 **single.go 만** 채워서 호출하도록 수정 (나머지는 후속 PR). 단, 기존 F-3.2 가 이미 workflow 별 applyAgentLearning 주입 로직 있으므로 거기 run-scoped fields 만 추가.

### 9. `cmd/elnath/cmd_daemon.go` wiring

runtime.go 와 동일 패턴. daemon 의 `learningStore` 생성부 (cmd_daemon.go:116 근처) 에 CursorStore + LLMExtractor 추가 후 orchestrator 주입 경로에 전달.

### 10. Workflow run-scoped injection

Phase 1 범위를 좁히기 위해 `single.go` 만 수정:
- `SingleWorkflow.Run` 안에서 `applyAgentLearning` 호출 직전, `deps.SessionID = ...`, `deps.MessageCount = ...`, `deps.ToolCallCount = totalCalls(info.ToolStats)`, `deps.CompactSummary = func() string { return "" }` 채움 (Phase 1 에서 실제 compact summary 는 빈 문자열 — Phase 2 에서 채움).
- Team/Ralph/Autopilot 은 기존 F-3.2 동작 유지. 신규 필드 미주입 시 `MessageCount = 0` → complexity gate 에서 자연스럽게 skip 되므로 호환.

## Tests

### `internal/learning/lesson_test.go` (신규 또는 기존 확장)

```go
func TestLesson_JSONRoundtrip_BackwardsCompat(t *testing.T) {
    // 기존 형태 (Rationale 등 없음) JSON 이 unmarshal 되고 신규 필드는 zero value.
    raw := `{"id":"abc","text":"x","source":"agent","confidence":"medium","created":"2025-01-01T00:00:00Z"}`
    var l Lesson
    if err := json.Unmarshal([]byte(raw), &l); err != nil { t.Fatal(err) }
    if l.Rationale != "" || l.PersonaDirection != "" { t.Errorf("new fields should be zero") }
}

func TestLesson_JSONRoundtrip_AllFields(t *testing.T) {
    // 모든 필드 있는 lesson roundtrip.
}

func TestLesson_JSONOmitsZeroFields(t *testing.T) {
    // Rationale=""일 때 marshal 출력에 "rationale" key 없음.
}
```

### `internal/learning/persona_mapping_test.go` (신규)

Table-driven: 9 조합 (3 directions × 3 magnitudes) + unknown direction + unknown magnitude + empty string + case insensitivity ("Large", "DECREASE").

### `internal/learning/complexity_test.go` (신규)

```go
func TestComplexityGate(t *testing.T) {
    tests := []struct {
        name       string
        gate       ComplexityGate
        msgs, calls int
        want       bool
    }{
        {"default_blocks_short", DefaultComplexityGate, 3, 5, false},
        {"default_blocks_no_tools", DefaultComplexityGate, 10, 0, false},
        {"default_passes", DefaultComplexityGate, 5, 1, true},
        {"no_tool_require", ComplexityGate{MinMessages: 5}, 5, 0, true},
        {"at_boundary", DefaultComplexityGate, 5, 1, true},
        {"below_boundary", DefaultComplexityGate, 4, 1, false},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := tt.gate.ShouldExtract(tt.msgs, tt.calls); got != tt.want {
                t.Errorf("got %v want %v", got, tt.want)
            }
        })
    }
}
```

### `internal/learning/cursor_test.go` (신규)

- Get on nonexistent file → 0, nil.
- Update then Get → returns last_line.
- Multiple updates same session → returns latest (largest line).
- Updates for different sessions interleaved → per-session correct latest.
- Malformed line mid-file → scan continues, valid records still read.
- Concurrent Update with `-race` (10 goroutines, 100 updates each).
- Empty sessionID → noop (Get returns 0, Update returns nil without writing).

### `internal/learning/extractor_llm_test.go` (신규)

- MockLLMExtractor{Lessons: [{...}]} Extract → returns copy, mutation doesn't affect mock.
- MockLLMExtractor{Err: someErr} Extract → returns error, lessons nil.
- Nil MockLLMExtractor 포인터 call → returns nil, nil (panic-safe).

### `internal/orchestrator/learning_test.go` (또는 기존 test 파일) 확장

```go
func TestApplyAgentLearning_LLMPath_ComplexityGateBlocks(t *testing.T) {
    // LLMExtractor 는 세트되어 있으나 MessageCount=3 → Extract 호출 안 됨.
    // 호출 여부 확인 위해 MockLLMExtractor 에 호출 카운터 필드 추가하거나
    // spy 패턴. 호출 0회 확인.
}

func TestApplyAgentLearning_LLMPath_MockLessonsAppended(t *testing.T) {
    // Mock 이 2 lessons 반환 → Store 에 저장, Source="agent:llm:single".
}

func TestApplyAgentLearning_LLMPath_FailClosed(t *testing.T) {
    // Rule fire + LLM fail → Rule lesson 저장, LLM lesson 0.
}

func TestApplyAgentLearning_RuleAndLLMParallel(t *testing.T) {
    // Rule 1 lesson + LLM 2 lessons → Store 에 3개 (SHA256 중복 없도록 텍스트 다르게).
}

func TestApplyAgentLearning_LLMPath_PersonaHintApplied(t *testing.T) {
    // Mock 반환 lesson 에 PersonaDirection="increase", PersonaMagnitude="medium",
    // PersonaDelta=[{Param:"caution", Delta:0}] → apply 후 Delta=0.03 로 덮어쓰기.
}

func TestApplyPersonaHint_SynthesizesPersonaDelta(t *testing.T) {
    // C2 verify: PersonaParam="caution", PersonaDirection="increase",
    // PersonaMagnitude="medium", PersonaDelta=nil → after apply,
    // PersonaDelta=[{Param:"caution", Delta:0.03}].
}

func TestApplyPersonaHint_MissingParamAndEmptyDelta_NoOp(t *testing.T) {
    // PersonaParam="" + PersonaDelta empty → no synthesis (방향/크기만 있어도 적용 대상 불명).
}

func TestApplyAgentLearning_ConcurrentSingleRuns_NoCrossTalk(t *testing.T) {
    // H1 verify: 동일 shared *LearningDeps 포인터를 두 goroutine 에서
    // 각자 shallow-copy + 다른 SessionID 주입 후 applyAgentLearning 호출.
    // -race 통과 + 두 run 이 독립적으로 mock.Extract 호출 (SessionID 가 섞이지 않음).
}

func TestStoreAppend_RedactsRationaleAndEvidence(t *testing.T) {
    // M4 verify: redactor 가 "AKIA..." 등을 마스킹. Rationale 에 fake key 넣고 Append
    // → readAllLocked 결과의 Rationale 이 redacted, Evidence[i] 도 redacted.
}
```

MockLLMExtractor 에 호출 카운터 추가가 필요하면 test 파일 로컬에서 wrapper 구조체 정의 (e.g., `countingExtractor`).

## Constraints

- **스키마 하위호환**: 기존 lessons.jsonl (현재 0 bytes) 과 앞으로 쌓이는 rule-only lesson 양쪽 모두 unmarshal 가능해야 함. 신규 필드는 전부 `omitempty`.
- **Store.Append 변경 금지**: SHA256 dedupe / redactor / Append 시그니처 그대로 사용. LLM lesson 도 동일 경로.
- **Rule path 동작 불변**: 기존 Rule A-E 호출 순서와 결과는 F-3.2 그대로. 신규 코드가 rule path 에 영향 주지 말 것.
- **LLM path fail-closed**: Extract 에러 시 rule lesson 은 반드시 저장되어야 한다. 에러로 전체 함수 조기 return 금지.
- **Cursor update ordering**: Extract 성공 후에만 CursorStore.Update 호출. 실패 시 cursor 전진 금지 (다음 run 이 같은 내용 다시 봐야 함).
- **Mock default**: `cfg.LLMExtraction.Enabled == false` (기본값) 일 때 `LLMExtractor = nil`. orchestrator 의 nil 체크가 확실히 동작해야 함 (테스트 필수).
- **단일 workflow 주입**: Phase 1 에서 run-scoped fields (SessionID / MessageCount / ToolCallCount / CompactSummary) 는 `single.go` 에만 채움. Team/Ralph/Autopilot 은 나중 PR — 이번 PR 에서 nil 주입 → complexity gate 차단 → 동작 0 regression.
- **Context timeout**: LLM Extract 는 30s context timeout. 상수 또는 config 값으로 뽑을 것 (`llmExtractTimeout = 30 * time.Second`).
- **Logger nil safety**: `deps.Logger == nil` 이면 `slog.Default()` 사용. 기존 코드와 동일 패턴.
- **Tests must use `t.TempDir()`** — ~/.elnath 건드리지 말 것.

## Verification gates

```bash
cd /Users/stello/elnath
go vet ./internal/learning/... ./internal/orchestrator/... ./internal/config/... ./cmd/elnath/...
go test -race ./internal/learning/... ./internal/orchestrator/... ./internal/config/... ./cmd/elnath/...
make build
./elnath lessons stats          # 기존 출력 (F-4) 호환 확인. LLM extraction: disabled 라인 없음 (Phase 1 은 stats 노출 안 함 — Phase 2 에 포함)
```

전부 exit 0 + 기존 테스트 regression 0 이어야 완료.

## Scope limits

- **Anthropic provider 호출 금지** — Phase 2.
- **Breaker 구현 금지** — Phase 2.
- **CompactLessonSummary 실 구현 금지** — Phase 2. Phase 1 은 `func() string { return "" }` 기본값.
- **lessons stats 에 LLM 섹션 추가 금지** — Phase 2.
- **Research path (`extractor.go` R1-R3) 확장 금지** — F-5.3.
- **Prompt engineering 불필요** — Phase 1 은 mock 만.
- **Wiki/persona state 스키마 변경 금지**.
- **기존 rule A-E 로직 건드리지 말 것**.

## 완료 보고 형식

작업 종료 시:
1. 수정/추가 파일 목록 (신규 8 + 수정 5+)
2. `go test -race ./internal/learning/... ./internal/orchestrator/... ./cmd/elnath/...` PASS 요약 (신규 테스트 개수)
3. `go vet` + `make build` 결과
4. `./elnath lessons stats` 실행 결과 (기존 F-4 출력 호환 확인)
5. 예상 commit message (spec §8 Phase 1 템플릿 사용)

커밋은 하지 마라. stello 가 직접 commit 후 Phase 2 OpenCode 프롬프트 작성.

## Open items (구현 중 질문 생기면 보고)

- `SingleWorkflow.Run` 내부에서 `MessageCount` / `ToolCallCount` 를 이미 집계하는 변수 있는지. 없으면 신규 카운터 추가.
- `CompactSummary` callback signature 를 `func() string` 으로 할지 `func() (string, int /*lastLine*/)` 로 할지. Phase 1 은 단순 `func() string` 으로 가되, Phase 2 에서 (string, int) 로 확장 시 wrapper 로 호환.
- Config 파일 파서가 nested struct 의 partial YAML 을 어떻게 병합하는지 — 테스트 돌려보고 default 채우는 방식 확정.
