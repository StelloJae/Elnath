# OpenCode Delegation Prompt: Phase E-3 B6 Self-Improvement

2 phase. 각 phase 완료 후 `go test -race` + `go vet` 검증.

---

## Phase 1: internal/learning/ 패키지 (pure logic)

```
elnath 프로젝트 (`/Users/stello/elnath/`, feat/telegram-redesign 브랜치)에서 Phase E-3 작업을 시작한다.

목표: `internal/learning/` 패키지를 신설한다. Research 결과에서 rule-based로 lesson을 추출하고, JSONL 파일에 저장한다.

### 참고할 기존 코드

internal/self/persona.go 의 Lesson 타입 (이미 존재):
```go
type Lesson struct {
    Param string  // "curiosity" | "verbosity" | "caution" | "creativity" | "persistence"
    Delta float64 // positive=increase, negative=decrease
}
```

internal/self/state.go 의 SelfState.ApplyLessons + Save (이미 존재).

### 작업 1: internal/learning/lesson.go

```go
package learning

import (
    "crypto/sha256"
    "encoding/hex"
    "time"

    "github.com/stello/elnath/internal/self"
)

type Lesson struct {
    ID           string        `json:"id"`
    Text         string        `json:"text"`
    Topic        string        `json:"topic,omitempty"`
    Source       string        `json:"source"`
    Confidence   string        `json:"confidence"`
    PersonaDelta []self.Lesson `json:"persona_delta,omitempty"`
    Created      time.Time     `json:"created"`
}

// deriveID returns an 8-char hex hash of text.
func deriveID(text string) string {
    sum := sha256.Sum256([]byte(text))
    return hex.EncodeToString(sum[:])[:8]
}
```

### 작업 2: internal/learning/store.go

```go
package learning

import (
    "bufio"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "sync"
    "time"
)

type Store struct {
    mu   sync.Mutex
    path string
}

func NewStore(path string) *Store {
    return &Store{path: path}
}
```

메서드:

1. `(s *Store) Append(lesson Lesson) error`
   - `s.mu.Lock()` / defer Unlock
   - `s == nil || s.path == ""` → nil (no-op)
   - parent dir 생성: `os.MkdirAll(filepath.Dir(s.path), 0o755)`
   - `lesson.Created`가 zero이면 `time.Now().UTC()` 설정
   - `lesson.ID`가 빈 문자열이면 `deriveID(lesson.Text)` 설정
   - 파일 열기: `os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)`
   - `json.NewEncoder(file).Encode(lesson)` — JSONL 한 줄
   - file.Close()
   - 에러 래핑

2. `(s *Store) List() ([]Lesson, error)`
   - `s.mu.Lock()` / defer Unlock
   - `s == nil || s.path == ""` → nil, nil
   - 파일 열기 (읽기 전용). `os.IsNotExist(err)` → nil, nil
   - `bufio.Scanner` 로 줄 단위 읽기
   - scanner buffer 확장: `scanner.Buffer(make([]byte, 64*1024), 1024*1024)` (큰 lesson 대비)
   - 각 줄 `json.Unmarshal` → Lesson, 결과 slice에 append
   - scanner.Err() 확인
   - 결과 반환

3. `(s *Store) Recent(n int) ([]Lesson, error)`
   - `lessons, err := s.List()` → 에러 처리
   - `sort.Slice(lessons, func(i, j int) bool { return lessons[i].Created.After(lessons[j].Created) })` — newest first
   - `n > 0 && n < len(lessons)` 이면 slice = slice[:n]
   - 결과 반환

### 작업 3: internal/learning/store_test.go

- Append: 새 파일 → JSON 1줄 확인, Created 자동 설정, ID 자동 생성 (Text 같으면 같은 ID)
- Append 3번 → List 결과 3개, order 유지
- Recent(2) → 최근 2개, newest first
- List 빈 파일 → nil
- 파일 없음 → nil, nil (에러 아님)
- 동시성 (`t.Run("concurrent", ...)`):
  - 10 goroutine × 5 append = 50 lessons
  - sync.WaitGroup 사용
  - List() 결과 50개 확인
  - `go test -race` 통과

### 작업 4: internal/learning/extractor.go

```go
package learning

import (
    "fmt"
    "time"

    "github.com/stello/elnath/internal/self"
)

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
    Confidence   string
    Supported    bool
}

const maxLessonTextLen = 200
const costThresholdUSD = 2.0

func truncate(s string, n int) string {
    if len(s) <= n {
        return s
    }
    return s[:n-3] + "..."
}

// Extract applies fixed rules to generate lessons from a research result.
func Extract(result ResultInfo) []Lesson {
    var lessons []Lesson
    now := time.Now().UTC()

    // Rule 1: high-confidence supported finding → persistence boost
    for _, round := range result.Rounds {
        if round.Supported && round.Confidence == "high" {
            text := truncate(fmt.Sprintf("On %s: %s", result.Topic, round.Findings), maxLessonTextLen)
            lessons = append(lessons, Lesson{
                Text:       text,
                Topic:      result.Topic,
                Source:     result.Topic,
                Confidence: "high",
                PersonaDelta: []self.Lesson{
                    {Param: "persistence", Delta: 0.02},
                },
                Created: now,
            })
        }
    }

    // Rule 2: majority low-confidence → caution boost
    lowCount := 0
    for _, round := range result.Rounds {
        if round.Confidence == "low" {
            lowCount++
        }
    }
    if len(result.Rounds) > 0 && lowCount*2 >= len(result.Rounds) {
        text := truncate(fmt.Sprintf("Topic %s requires more evidence before conclusions.", result.Topic), maxLessonTextLen)
        lessons = append(lessons, Lesson{
            Text:       text,
            Topic:      result.Topic,
            Source:     result.Topic,
            Confidence: "medium",
            PersonaDelta: []self.Lesson{
                {Param: "caution", Delta: 0.03},
                {Param: "curiosity", Delta: -0.01},
            },
            Created: now,
        })
    }

    // Rule 3: high cost → verbosity reduction
    if result.TotalCost > costThresholdUSD {
        text := truncate(fmt.Sprintf("Research on %s exceeded budget ($%.2f); prefer focused experiments.", result.Topic, result.TotalCost), maxLessonTextLen)
        lessons = append(lessons, Lesson{
            Text:       text,
            Topic:      result.Topic,
            Source:     result.Topic,
            Confidence: "high",
            PersonaDelta: []self.Lesson{
                {Param: "verbosity", Delta: -0.02},
            },
            Created: now,
        })
    }

    return lessons
}
```

### 작업 5: internal/learning/extractor_test.go

테이블 기반 테스트:

1. 빈 Rounds + TotalCost=0 → 빈 slice
2. 1 round (Supported=true, Confidence="high") → 1 lesson, "persistence" Delta +0.02
3. 3 rounds 전부 Confidence="low" → 1 lesson (Rule 2), caution +0.03, curiosity -0.01
4. 2 rounds (1 high supported, 1 low) → Rule 1 lesson 1개만
5. TotalCost=3.5 → Rule 3 lesson 1개, verbosity -0.02
6. 복합: 2 high supported + TotalCost=5.0 → 3 lessons (Rule 1 x2 + Rule 3)
7. Findings 300자 → Text가 200자로 truncate, "..." 끝에 붙음

### 검증

```bash
go test -race ./internal/learning/...
go vet ./internal/learning/...
```

통과 확인.
```

---

## Phase 2: LessonsNode + research.TaskRunner 통합 + runtime 등록

```
Phase E-3 Phase 2. Phase 1에서 internal/learning/ 패키지가 완성됐다.

### 작업 1: internal/prompt/lessons_node.go

```go
package prompt

import (
    "context"
    "fmt"
    "log/slog"
    "strings"

    "github.com/stello/elnath/internal/learning"
)

type LessonLister interface {
    Recent(n int) ([]learning.Lesson, error)
}

type LessonsNode struct {
    priority   int
    store      LessonLister
    maxEntries int
    maxChars   int
}

func NewLessonsNode(priority int, store LessonLister, maxEntries, maxChars int) *LessonsNode {
    if maxEntries <= 0 {
        maxEntries = 10
    }
    if maxChars <= 0 {
        maxChars = 1000
    }
    return &LessonsNode{priority: priority, store: store, maxEntries: maxEntries, maxChars: maxChars}
}

func (n *LessonsNode) Name() string { return "lessons" }

func (n *LessonsNode) Priority() int {
    if n == nil {
        return 0
    }
    return n.priority
}

func (n *LessonsNode) Render(_ context.Context, state *RenderState) (string, error) {
    if n == nil || n.store == nil {
        return "", nil
    }
    if state != nil && state.BenchmarkMode {
        return "", nil
    }
    lessons, err := n.store.Recent(n.maxEntries)
    if err != nil {
        slog.Warn("lessons node: store read failed", "error", err)
        return "", nil
    }
    if len(lessons) == 0 {
        return "", nil
    }
    var b strings.Builder
    b.WriteString("Recent lessons:\n")
    for _, l := range lessons {
        date := l.Created.Format("2006-01-02")
        line := fmt.Sprintf("\n- [%s] %s", date, l.Text)
        if b.Len()+len(line) > n.maxChars {
            break
        }
        b.WriteString(line)
    }
    return b.String(), nil
}
```

### 작업 2: internal/prompt/lessons_node_test.go

테스트:
- nil store → ""
- 빈 store (mock Recent가 nil 반환) → ""
- 3 lessons → 출력에 모두 포함, "Recent lessons:" 헤더, 각 lesson에 날짜 prefix
- BenchmarkMode=true → ""
- 긴 lesson들 (maxChars 초과) → 일부만 포함

Mock:
```go
type mockLessonLister struct {
    lessons []learning.Lesson
    err     error
}

func (m *mockLessonLister) Recent(n int) ([]learning.Lesson, error) {
    if m.err != nil {
        return nil, m.err
    }
    if n > 0 && n < len(m.lessons) {
        return m.lessons[:n], nil
    }
    return m.lessons, nil
}
```

### 작업 3: internal/research/runner.go — learning integration

기존 TaskRunner struct에 필드 추가:

```go
type TaskRunner struct {
    // ... existing fields ...
    learningStore *learning.Store
    selfState     *self.SelfState
}
```

import 추가:
```go
"github.com/stello/elnath/internal/learning"
"github.com/stello/elnath/internal/self"
```

Option 함수 2개 추가:

```go
func WithRunnerLearning(store *learning.Store) TaskRunnerOption {
    return func(r *TaskRunner) {
        r.learningStore = store
    }
}

func WithRunnerSelfState(s *self.SelfState) TaskRunnerOption {
    return func(r *TaskRunner) {
        r.selfState = s
    }
}
```

Run 메서드 수정 — result 성공 후 applyLearning 호출:

```go
func (r *TaskRunner) Run(ctx context.Context, payload daemon.TaskPayload, onText func(string)) (daemon.TaskRunnerResult, error) {
    // ... existing execution logic ...
    result, err := loop.Run(ctx, topic)
    if err != nil {
        return daemon.TaskRunnerResult{}, err
    }

    r.applyLearning(result)

    // ... existing JSON encoding and return ...
}
```

applyLearning 메서드 추가:

```go
func (r *TaskRunner) applyLearning(result *research.ResearchResult) {
    if r == nil || r.learningStore == nil || result == nil {
        return
    }

    info := toResultInfo(result)
    lessons := learning.Extract(info)
    if len(lessons) == 0 {
        return
    }

    personaChanged := false
    for _, l := range lessons {
        if err := r.learningStore.Append(l); err != nil {
            r.logger.Warn("learning: append failed", "error", err)
            continue
        }
        if len(l.PersonaDelta) > 0 && r.selfState != nil {
            r.selfState.ApplyLessons(l.PersonaDelta)
            personaChanged = true
        }
    }

    if personaChanged {
        if err := r.selfState.Save(); err != nil {
            r.logger.Warn("learning: selfState save failed", "error", err)
        }
    }
}

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

**중요:** `research.ResearchResult`, `RoundResult`, `ExperimentResult`, `Hypothesis` 등의 실제 필드 이름은 `internal/research/` 의 기존 타입 정의에서 확인하고 맞춘다. 위 코드는 가정이다.

### 작업 4: internal/research/runner_test.go — 기존 테스트 확장

- WithRunnerLearning: runner에 store 주입 → Run 후 store.List()에 lesson 있음 (high-confidence mock result인 경우)
- WithRunnerSelfState: runner에 selfState 주입 → Run 후 persona 값 변경
- learningStore=nil → applyLearning 호출돼도 에러 없음
- selfState=nil → store만 append, persona Apply skip

### 작업 5: cmd/elnath/cmd_daemon.go — learning store 생성 + 주입

research runner 생성 부분 수정:

```go
import (
    "github.com/stello/elnath/internal/learning"
)

// ... selfState 로드 후 ...

learningPath := filepath.Join(cfg.DataDir, "lessons.jsonl")
learningStore := learning.NewStore(learningPath)

researchRunner := research.NewTaskRunner(
    provider,
    model,
    wikiIdx,
    wikiStore,
    usageTracker,
    app.Logger,
    research.WithRunnerLearning(learningStore),
    research.WithRunnerSelfState(selfState),
)
d.SetResearchRunner(researchRunner)
```

### 작업 6: cmd/elnath/runtime.go — LessonsNode 등록

runtime.go의 node 등록 부분 수정:

```go
import (
    "github.com/stello/elnath/internal/learning"
)

// ... 기존 코드 ...

// buildExecutionRuntime 안에서
learningPath := filepath.Join(cfg.DataDir, "lessons.jsonl")
learningStore := learning.NewStore(learningPath)

// 노드 등록 순서 변경
b := prompt.NewBuilder()
b.Register(prompt.NewIdentityNode(100))
b.Register(prompt.NewContextFilesNode(95))
b.Register(prompt.NewPersonaNode(90))
b.Register(prompt.NewLessonsNode(87, learningStore, 10, 1000)) // NEW
b.Register(prompt.NewSelfStateNode(85))
b.Register(prompt.NewToolCatalogNode(80))
// ... 나머지 기존 등록 유지 ...
```

**주의:** runtime.go의 현재 노드 등록 코드를 먼저 read하고, 정확한 위치에 LessonsNode를 삽입한다 (Persona 뒤, SelfState 앞).

### 전체 검증

```bash
go test -race ./internal/learning/... ./internal/prompt/... ./internal/research/... ./cmd/elnath/...
go vet ./...
make build
```

전부 통과 확인.

### 수동 검증 (선택)

1. 기존 lessons.jsonl이 있으면 백업: `mv ~/.elnath/lessons.jsonl ~/.elnath/lessons.jsonl.bak` (또는 삭제)
2. daemon 재시작
3. `./elnath research start "test topic for lessons"` → 실행
4. 완료 후 `cat ~/.elnath/lessons.jsonl` → JSONL 확인 (rule이 매치됐다면 1-3개 entry)
5. `./elnath run` → system prompt dump 확인 (debug mode가 있으면). "Recent lessons:" 섹션 포함 확인
6. self_state.json 확인 → persona 값이 0.5에서 약간 변동됨
```
