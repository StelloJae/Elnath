# Phase E-3: B6 Self-Improvement Substrate

**Status:** SPEC READY  
**Predecessor:** Phase E-2 (Scheduler) DONE  
**Successor:** Phase F (LB2 Magic Docs or polish)  
**Branch:** `feat/telegram-redesign`  
**Ref:** Superiority Design v2.2 §Phase 5.3 — B6 Self-Improvement

---

## 1. Goal

Research task가 완료되면 결과에서 **lesson**을 추출하여 저장하고, 다음 prompt에 주입하여 future task 성능을 개선한다. Persona 파라미터(curiosity/caution 등)도 미세 조정한다.

**핵심 플라이휠:**
```
research.Loop → ResearchResult → Extract lessons
                                     │
              ┌──────────────────────┼──────────────────────┐
              ▼                      ▼                      ▼
      JSONL store append    Persona delta apply    Next prompt injection
      (lessons.jsonl)       (SelfState.Save)       (LessonsNode)
```

**Out of scope:**
- LLM-based lesson extraction (현재는 rule-based). 향후 Phase E-4에서 확장 가능
- Agent task에서 lesson 추출 (research task만). 향후 확장
- Routing preference self-learning
- Lesson deduplication (단순 append)
- Lesson pruning (저장소 무한 성장 — Phase F에서 rotation 고려)

## 2. Architecture Overview

```
┌────────────────────────┐
│ research.TaskRunner    │
│ .Run(ctx, payload)     │
└───────┬────────────────┘
        │ result := loop.Run(...)
        ▼
┌────────────────────────┐
│ learning.Extract       │
│ (rule-based, ~3 rules) │
└───────┬────────────────┘
        │ []Lesson
        ▼
┌────────────────────────┐         ┌────────────────────────┐
│ learning.Store.Append  │         │ SelfState.ApplyLessons │
│ → lessons.jsonl        │         │ → Save()               │
└────────────────────────┘         └────────────────────────┘
        │                                    │
        │ (later, next prompt build)         │
        ▼                                    ▼
┌────────────────────────┐         ┌────────────────────────┐
│ LessonsNode.Render     │         │ IdentityNode.Render    │
│ → inject top-N lessons │         │ → updated persona      │
└────────────────────────┘         └────────────────────────┘
```

**설계 결정:**

1. **JSONL store** — `{dataDir}/lessons.jsonl`. append-only, 복잡한 DB 불필요. 동시 쓰기는 mutex로 보호.
2. **Rule-based extractor** — 현재 research 결과에서 meta data (Supported, Confidence)를 보고 3-5개 간단한 규칙 적용. LLM 호출 없음 (비용 0).
3. **Lesson = text + optional persona delta** — text는 prompt에 inject, delta는 persona에 apply. 두 효과 모두 선택적.
4. **LessonsNode priority = 87** — Persona(90)과 SelfState(85) 사이. Persona 선언 직후에 적용.
5. **Integration은 research.TaskRunner 내부** — runner가 store와 selfState를 옵션으로 받음. 없으면 no-op.

## 3. Deliverables

### 3.1 New Package: `internal/learning/`

#### `internal/learning/lesson.go`

```go
package learning

import (
    "time"

    "github.com/stello/elnath/internal/self"
)

// Lesson is a piece of actionable guidance extracted from a task outcome.
// Lessons may inject text into future prompts and/or adjust persona parameters.
type Lesson struct {
    ID           string        `json:"id"`              // deterministic hash of Text
    Text         string        `json:"text"`            // human-readable guidance
    Topic        string        `json:"topic,omitempty"` // context tag (e.g., research topic)
    Source       string        `json:"source"`          // task ID or topic that generated it
    Confidence   string        `json:"confidence"`      // "high" | "medium" | "low"
    PersonaDelta []self.Lesson `json:"persona_delta,omitempty"`
    Created      time.Time     `json:"created"`
}
```

`ID`는 `Text`의 SHA256 short hash (8자리). 중복 제거용.

#### `internal/learning/store.go`

JSONL 기반 append-only store.

```go
type Store struct {
    mu   sync.Mutex
    path string
}

// NewStore creates a Store backed by the given JSONL file.
// The file is created on first Append if it does not exist.
func NewStore(path string) *Store

// Append writes a lesson to the file. Creates parent dir if needed.
func (s *Store) Append(lesson Lesson) error

// List returns all lessons in the store, oldest first.
func (s *Store) List() ([]Lesson, error)

// Recent returns the most recent n lessons (newest first).
// If n <= 0 or >= total, returns all in reverse order.
func (s *Store) Recent(n int) ([]Lesson, error)
```

**Append 로직:**
1. `s.mu.Lock()` / defer Unlock
2. parent dir 생성 (`os.MkdirAll`)
3. 파일 열기 (`os.O_APPEND|os.O_CREATE|os.O_WRONLY`, 0o600)
4. `lesson.Created`가 zero면 `time.Now().UTC()` 설정
5. `lesson.ID`가 빈 문자열이면 Text의 SHA256 short (8자) 설정
6. `json.NewEncoder(file).Encode(lesson)` — JSONL 한 줄
7. file.Close()

**List 로직:**
1. 파일 없으면 `return nil, nil`
2. `bufio.Scanner`로 각 줄 파싱
3. 각 줄 `json.Unmarshal` → Lesson
4. 결과 slice 반환

**Recent 로직:**
1. `List()` → 전체
2. 역순 정렬 (newest first, by Created)
3. n개만 slice

#### `internal/learning/store_test.go`

- Append: 새 파일 생성, JSON 1줄 확인
- Append 3번 → List 결과 3개, Created 자동 설정
- Append: ID 비어있으면 자동 생성, 같은 Text는 같은 ID
- Recent(2) → 최근 2개 (역순)
- List 빈 파일 → nil
- 동시성: 10 goroutine에서 각 5개 append → race test 통과, 총 50개

#### `internal/learning/extractor.go`

Research 결과에서 rule-based로 lesson 추출.

```go
// Research results expose just enough for extraction without importing the full research package
// to avoid circular dependencies. We use a minimal interface.

type ResultInfo struct {
    Topic     string
    Summary   string
    TotalCost float64
    Rounds    []RoundInfo
}

type RoundInfo struct {
    HypothesisID string
    Statement    string
    Findings     string
    Confidence   string // "high" | "medium" | "low"
    Supported    bool
}

// Extract derives lessons from a research result using fixed rules.
// Returns an empty slice if no lesson applies.
func Extract(result ResultInfo) []Lesson
```

**규칙 3개:**

Rule 1 — High-confidence supported finding → positive lesson (persistence↑):
```
For each round where Supported=true and Confidence="high":
  Lesson{
    Text: "On {topic}: {findings}",
    Topic: result.Topic,
    Confidence: "high",
    PersonaDelta: [{Param: "persistence", Delta: +0.02}],
  }
```

Rule 2 — Majority low-confidence → caution lesson (caution↑, curiosity↓):
```
If >= half of rounds have Confidence="low":
  Lesson{
    Text: "Topic {topic} requires more evidence before conclusions.",
    Topic: result.Topic,
    Confidence: "medium",
    PersonaDelta: [
      {Param: "caution", Delta: +0.03},
      {Param: "curiosity", Delta: -0.01},
    ],
  }
```

Rule 3 — High cost exceeded target → efficiency lesson (verbosity↓):
```
If result.TotalCost > 2.0 (USD, threshold):
  Lesson{
    Text: "Research on {topic} exceeded budget ({cost:.2f}); prefer focused experiments.",
    Topic: result.Topic,
    Confidence: "high",
    PersonaDelta: [{Param: "verbosity", Delta: -0.02}],
  }
```

**Text 최대 길이:** 200자. Findings가 길면 truncate + "...".

Persona 파라미터 이름은 반드시 5개 중 하나: curiosity, verbosity, caution, creativity, persistence.

#### `internal/learning/extractor_test.go`

- 빈 Rounds → 빈 lesson slice
- 1 high-confidence supported round → 1 lesson (Rule 1)
- 3 low-confidence rounds → 1 lesson (Rule 2, caution 증가)
- TotalCost=3.0 → 1 lesson (Rule 3)
- 복합 (high round 2개 + cost 초과) → 3 lessons (rule 1 x2 + rule 3)
- Findings 길이 > 200자 → truncate 확인

### 3.2 New: `internal/prompt/lessons_node.go`

```go
type LessonsNode struct {
    priority   int
    store      learning.LessonLister // narrow interface
    maxEntries int
    maxChars   int
}

// LessonLister is the narrow interface the node needs.
type LessonLister interface {
    Recent(n int) ([]learning.Lesson, error)
}

func NewLessonsNode(priority int, store LessonLister, maxEntries, maxChars int) *LessonsNode
```

**Render 로직:**
1. `n == nil || n.store == nil` → ""
2. `state != nil && state.BenchmarkMode` → ""
3. `lessons, err := n.store.Recent(n.maxEntries)` — 에러 시 빈 문자열 (로그만)
4. len == 0 → ""
5. 출력 형식:
```
Recent lessons:

- [2026-04-13] On go patterns: prefer errors.Is over type assertions
- [2026-04-12] Topic ml-strategies requires more evidence before conclusions.
- ...
```
6. 전체 maxChars(기본 1000) 초과 시 끝부터 drop

#### `internal/prompt/lessons_node_test.go`

- nil store → ""
- 빈 store → ""
- 3 lessons → 렌더링에 모두 포함
- BenchmarkMode → ""
- maxChars 초과 → truncate

### 3.3 Modified: `internal/research/runner.go`

Research TaskRunner에 learning integration 추가.

```go
type TaskRunner struct {
    // ... existing fields ...
    learningStore *learning.Store // optional, nil이면 no-op
    selfState     *self.SelfState // optional, nil이면 persona delta skip
}

// WithRunnerLearning attaches a learning store for automatic lesson extraction.
func WithRunnerLearning(store *learning.Store) TaskRunnerOption

// WithRunnerSelfState attaches self state for persona delta application.
func WithRunnerSelfState(s *self.SelfState) TaskRunnerOption
```

`Run` 메서드 끝부분에 추가:

```go
func (r *TaskRunner) Run(ctx context.Context, payload daemon.TaskPayload, onText func(string)) (daemon.TaskRunnerResult, error) {
    // ... existing research loop execution ...
    result, err := loop.Run(ctx, topic)
    if err != nil {
        return daemon.TaskRunnerResult{}, err
    }

    // NEW: Lesson extraction and application
    r.applyLearning(result)

    // ... existing JSON encoding and return ...
}

func (r *TaskRunner) applyLearning(result *research.ResearchResult) {
    if r.learningStore == nil {
        return
    }
    info := toResultInfo(result)
    lessons := learning.Extract(info)
    for _, l := range lessons {
        if err := r.learningStore.Append(l); err != nil {
            r.logger.Warn("learning: append failed", "error", err)
            continue
        }
        if len(l.PersonaDelta) > 0 && r.selfState != nil {
            r.selfState.ApplyLessons(l.PersonaDelta)
        }
    }
    if r.selfState != nil {
        if err := r.selfState.Save(); err != nil {
            r.logger.Warn("learning: selfState save failed", "error", err)
        }
    }
}

// toResultInfo converts research.ResearchResult to learning.ResultInfo.
// Avoids circular imports between research and learning packages.
func toResultInfo(r *research.ResearchResult) learning.ResultInfo {
    rounds := make([]learning.RoundInfo, 0, len(r.Rounds))
    for _, round := range r.Rounds {
        rounds = append(rounds, learning.RoundInfo{
            HypothesisID: round.Hypothesis.ID,
            Statement:    round.Hypothesis.Statement,
            Findings:     round.Result.Findings,
            Confidence:   round.Result.Confidence,
            Supported:    round.Result.Supported,
        })
    }
    return learning.ResultInfo{
        Topic:     r.Topic,
        Summary:   r.Summary,
        TotalCost: r.TotalCost,
        Rounds:    rounds,
    }
}
```

### 3.4 Modified: `cmd/elnath/cmd_daemon.go`

Learning store 생성 + research runner에 주입.

```go
// After selfState is loaded...
learningPath := filepath.Join(cfg.DataDir, "lessons.jsonl")
learningStore := learning.NewStore(learningPath)

// Inject into research runner
researchRunner := research.NewTaskRunner(
    provider, model,
    wikiIdx, wikiStore,
    usageTracker,
    logger,
    research.WithRunnerLearning(learningStore),
    research.WithRunnerSelfState(selfState),
)
```

### 3.5 Modified: `cmd/elnath/runtime.go`

LessonsNode를 prompt builder에 등록.

```go
// Add learning store to runtime (same instance as daemon uses)
learningPath := filepath.Join(cfg.DataDir, "lessons.jsonl")
learningStore := learning.NewStore(learningPath)

// ... existing node registrations ...
b.Register(prompt.NewIdentityNode(100))
b.Register(prompt.NewContextFilesNode(95))
b.Register(prompt.NewPersonaNode(90))
b.Register(prompt.NewLessonsNode(87, learningStore, 10, 1000))  // NEW
b.Register(prompt.NewSelfStateNode(85))
// ... rest unchanged ...
```

우선순위 87 (Persona 90과 SelfState 85 사이). 모든 workflow가 공유.

**주의:** daemon과 run CLI 모두에서 같은 `lessons.jsonl` 파일을 읽고 쓴다. JSONL append는 원자적이지만 (small write + O_APPEND), 둘이 동시에 append할 수 있다. `Store.mu`가 같은 프로세스 내에서만 보호하므로, daemon + run이 동시에 다른 프로세스면 race 가능하다. 현실적으로 daemon과 run은 동시에 안 돌지만, 만약 동시 가능성이 문제면 `O_APPEND` + 8KB 이하 write는 POSIX에서 atomic (대부분의 Linux/macOS 파일시스템) — OK.

## 4. File Summary

### New Files (5)

| File | LOC (est.) | 역할 |
|------|-----------|------|
| `internal/learning/lesson.go` | ~30 | Lesson struct |
| `internal/learning/store.go` | ~120 | JSONL Append/List/Recent |
| `internal/learning/store_test.go` | ~180 | Store 테스트 (동시성 포함) |
| `internal/learning/extractor.go` | ~100 | 3 rule-based extractor |
| `internal/learning/extractor_test.go` | ~150 | 각 규칙 테스트 |
| `internal/prompt/lessons_node.go` | ~70 | LessonsNode (Recent 기반) |
| `internal/prompt/lessons_node_test.go` | ~100 | Node 테스트 |

### Modified Files (3)

| File | 변경 내용 |
|------|----------|
| `internal/research/runner.go` | learning/selfState 필드, WithRunnerLearning/WithRunnerSelfState, applyLearning 메서드 |
| `cmd/elnath/cmd_daemon.go` | learning store 생성, research runner에 주입 |
| `cmd/elnath/runtime.go` | learning store 생성 (동일 파일 공유), LessonsNode 등록 |

## 5. Acceptance Criteria

- [ ] `go test -race ./internal/learning/... ./internal/prompt/... ./internal/research/... ./cmd/elnath/...` 통과
- [ ] `go vet ./...` 경고 없음
- [ ] `make build` 성공
- [ ] research task 완료 후 `~/.elnath/lessons.jsonl`에 JSON 줄 추가됨 (rule이 매치될 경우)
- [ ] self_state.json의 persona 값이 Delta 만큼 변경됨
- [ ] 이후 `elnath run` 실행 시 system prompt에 "Recent lessons:" 섹션 포함
- [ ] BenchmarkMode에서 LessonsNode → 빈 문자열
- [ ] Rule 3개 각각의 trigger 조건에서 lesson 생성 확인

## 6. Risk

| Risk | Mitigation |
|------|-----------|
| Persona drift (반복 호출로 극단값 도달) | `Persona.Adjust`가 이미 [0.0, 1.0] clamp. Delta가 매번 0.02~0.03으로 작음. 수백 번 누적해야 1.0 도달 |
| lessons.jsonl 무한 성장 | 현재 rotation 없음. Phase F에서 `RotateIfLargerThan(1MB)` 추가 예정 |
| JSONL 동시 write race | 같은 프로세스 내 mutex. 다른 프로세스 간은 O_APPEND + 작은 write로 대부분 OS에서 원자적 |
| Rule이 너무 단순해서 잘못된 lesson 생성 | confidence 필드가 high인 경우만 persona delta 적용. medium/low는 text만 저장 |
| research가 매번 실패 → caution만 무한 증가 | 각 round의 Supported 여부 기준. 전부 실패하면 lesson 생성. 사용자가 lessons.jsonl 수동 정리 가능 |
| LessonsNode가 prompt 비대화 | maxEntries=10 + maxChars=1000 제한. token budget 초과 시 priority 87 → 드롭 대상 |

## 7. Future Work (Phase E-4+)

- LLM-based lesson extraction (현재 rule-based는 제한적)
- Agent task (non-research) lesson 추출
- Topic-specific lesson filtering (`RecentForTopic(topic, n)`)
- Lesson decay (오래된 lesson은 점진적으로 낮은 가중치)
- CLI: `elnath lessons list`, `elnath lessons clear <id>`
- Routing preference self-learning (B6 확장)
- Wiki integration — lessons를 `wiki/self/lessons.md`로도 export
