# Phase C-2 Worker 4: Layer 3 — Consolidator + Promotion

## 역할
Draft skill의 prevalence를 분석하여 자동으로 active로 승격하고, 만료된 draft를 정리하는 consolidator와 승격 알림 포매터를 구현한다.

**신규 파일만 생성한다.** 기존 파일 수정 금지 (W1이 담당).

## 선행 조건
W1의 Tasks 1-5 완료 필요 (Skill struct 확장, Creator, Tracker, interfaces.go).

## 선행 지식

### Creator API (W1이 생성)
```go
func NewCreator(store *wiki.Store, tracker *Tracker, registry *Registry) *Creator
func (c *Creator) Promote(name string) error  // status→active, registry.Add() hot-reload
func (c *Creator) Delete(name string) error
```

### Tracker API (W1이 생성)
```go
func NewTracker(dataDir string) *Tracker
func (t *Tracker) LoadPatterns() ([]PatternRecord, error)
func (t *Tracker) RecordPattern(PatternRecord) error

type PatternRecord struct {
    ID           string    `json:"id"`
    Description  string    `json:"description"`
    SessionIDs   []string  `json:"session_ids"`
    ToolSequence []string  `json:"tool_sequence"`
    FirstSeen    time.Time `json:"first_seen"`
    LastSeen     time.Time `json:"last_seen"`
    DraftSkill   string    `json:"draft_skill,omitempty"` // 이 draft와 연결된 skill 이름
}
```

### Skill + FromPage (W1이 확장)
```go
type Skill struct {
    Name, Description, Trigger string
    RequiredTools []string
    Model, Prompt string
    Status string // "active" | "draft"
    Source string // "user" | "hint" | "analyst" | "promoted"
}
func FromPage(page *wiki.Page) *Skill
```

### Wiki Store API
```go
func (s *Store) List() ([]*Page, error)  // 모든 .md 파일 순회
func (s *Store) Read(path string) (*Page, error)
```

### ConsolidationResult (W1 interfaces.go)
```go
type ConsolidationResult struct {
    Promoted []string
    Merged   []string
    Rejected []string
    Cleaned  []string
}
```

## 작업

### Task 1: Consolidator 구현
**파일:** `internal/skill/consolidator.go`, `internal/skill/consolidator_test.go` (신규)

```go
type ConsolidatorConfig struct {
    MinSessions   int           // default 5
    MinPrevalence int           // default 2
    MaxDraftAge   time.Duration // default 90 days
}

func DefaultConsolidatorConfig() ConsolidatorConfig

type Consolidator struct {
    creator  *Creator
    tracker  *Tracker
    registry *Registry
    store    *wiki.Store
    config   ConsolidatorConfig
}

func NewConsolidator(creator, tracker, registry, store, config) *Consolidator
func (c *Consolidator) Run(ctx context.Context) (*ConsolidationResult, error)
```

**Run() 로직:**
1. `store.List()` → FromPage() → status=="draft"인 것만 수집
2. `tracker.LoadPatterns()` → 모든 PatternRecord 로드
3. 각 draft에 대해:
   - PatternRecord의 `DraftSkill` 필드가 이 draft 이름과 일치하는 레코드 찾기
   - 해당 레코드들의 SessionIDs를 모두 set에 합침 → prevalence = set 크기
   - `prevalence >= MinPrevalence AND prevalence >= MinSessions` → `creator.Promote(name)`
   - 위 조건 미충족이고 `time.Since(page.Created) > MaxDraftAge` → `creator.Delete(name)` (expired)
4. ConsolidationResult 반환

**테스트 3개:**
1. `TestConsolidatorPromotesMeetingThreshold`: draft + 충분한 patterns → promoted
2. `TestConsolidatorSkipsBelowThreshold`: draft + 부족한 patterns → promoted 안 됨
3. `TestConsolidatorCleansOldDrafts`: MaxDraftAge=0으로 즉시 만료 → cleaned

### Task 2: Promotion 알림 포매터
**파일:** `internal/skill/promotion.go`, `internal/skill/promotion_test.go` (신규)

```go
func FormatPromotionMessage(sk *Skill, prevalence int, totalSessions int) string
```

출력 예: `"New skill /deploy-check activated (7 sessions, 3 independent patterns). Use /skill-list to review."`

**테스트:** 반환값에 skill 이름, prevalence 수, session 수가 포함되는지 확인. `strings.Contains` 사용.

## 완료 기준
- [ ] `go test -race ./internal/skill/ -run TestConsolidator` PASS
- [ ] `go test -race ./internal/skill/ -run TestFormatPromotionMessage` PASS
- [ ] `go build ./internal/skill/...` 성공
- [ ] `go vet ./internal/skill/` 경고 없음

## Scheduler 연동 (W1이 처리)
이 worker는 consolidator와 promotion 로직만 구현한다. Scheduler 등록과 daemon의 `runTask()` 분기는 W1이 `runtime.go`와 `scheduler/task.go`에서 처리한다.

```yaml
# W1이 scheduled_tasks.yaml에 추가하는 예시
- name: skill-promote
  type: skill-promote
  interval: 24h
  run_on_start: false
  enabled: true
```

Daemon이 이 task를 트리거하면 `consolidator.Run()` 호출 → 승격된 skill마다 `FormatPromotionMessage()` → Telegram 알림.

## 참고 문서
- Spec: `docs/specs/PHASE-C2-SKILL-EMERGENCE.md` §11 (W4 상세)
- Impl plan: `docs/specs/PHASE-C2-IMPL-PLAN.md` (Tasks 10-11)
- 기존 learning.ComplexityGate 패턴 참고: `internal/learning/complexity.go`
