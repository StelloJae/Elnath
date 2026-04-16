# Phase C-2 Worker 1: Foundation (Skill CRUD + Creator + Tracker + Integration)

## 역할
Skill Emergence MVP의 기반을 구축한다. 기존 파일을 수정하여 Status/Source 필드 추가, draft 필터링, JSONL tracker, Creator CRUD, CLI 명령, runtime/scheduler/telegram 통합을 완성한다.

**이 worker만 기존 파일을 수정한다.** W2, W4는 신규 파일만 생성.

## 선행 지식

### 프로젝트
- 순수 Go CLI daemon (`/Users/stello/elnath/`)
- Wiki: 마크다운 + SQLite FTS5 (`internal/wiki/store.go` — Create/Read/Update/Delete/Upsert/List)
- Skill 기존 코드: `internal/skill/skill.go` (Skill struct, FromPage), `internal/skill/registry.go` (Registry, Load, Execute, FilterRegistry)
- 테스트: `go test -race ./...`, 테이블 드리븐

### 핵심 API
```go
// wiki.Store
func (s *Store) Create(page *Page) error
func (s *Store) Read(path string) (*Page, error)  
func (s *Store) Update(page *Page) error
func (s *Store) Delete(path string) error
func (s *Store) List() ([]*Page, error)
func (s *Store) Upsert(page *Page) error

// wiki.Page
type Page struct {
    Path    string
    Title   string
    Tags    []string
    Extra   map[string]any
    Content string
    Created time.Time
    Updated time.Time
}

// skill.Registry (기존)
func (r *Registry) Add(s *Skill)           // 이미 존재
func (r *Registry) Load(store) error       // draft 필터 추가 필요
func (r *Registry) Get(name) (*Skill, bool)
func (r *Registry) List() []*Skill
```

### 명령 등록 패턴
```go
// cmd/elnath/commands.go — commands map에 추가
"skill": cmdSkill,

// cmd/elnath/runtime.go — buildExecutionRuntime() 내부
// skillReg는 line 272-277에서 이미 생성됨
// 그 아래에 Creator, Tracker 생성 추가
```

## 작업 순서 (TDD)

### 1. Skill struct에 Status, Source 추가
**파일:** `internal/skill/skill.go`, `internal/skill/skill_test.go`

Skill struct에 `Status string` ("active"|"draft", 빈 문자열은 "active"로 기본), `Source string` ("user"|"hint"|"analyst"|"promoted") 필드 추가.

`FromPage()`에서 `Extra["status"]`와 `Extra["source"]` 파싱. status가 비어있으면 "active" 기본값.

테스트: 기존 TestFromPage 테이블에 status/source 파싱 케이스 추가.

### 2. Registry.Load()에 draft 필터 추가
**파일:** `internal/skill/registry.go`, `internal/skill/registry_test.go`

Load() 루프에서 `if skill.Status == "draft" { continue }` 추가.

테스트: wiki에 active + draft skill 2개 생성 → Load 후 active만 존재 확인.

### 3. interfaces.go 생성
**파일:** `internal/skill/interfaces.go` (신규)

W2, W4가 구현할 인터페이스와 공유 타입 정의:

```go
type Analyst interface {
    Analyze(ctx context.Context, sessions []SessionTrajectory) ([]SkillPatch, error)
}

type SessionTrajectory struct {
    SessionID string
    Messages  []llm.Message
    Success   bool
    Intent    string
}

type SkillPatch struct {
    Action         string // "create" | "deepen"
    Params         CreateParams
    Evidence       []string
    Confidence     float64
    PatchRationale string
}

type ConsolidationResult struct {
    Promoted []string
    Merged   []string
    Rejected []string
    Cleaned  []string
}

type NotifyFunc func(ctx context.Context, message string) error
```

### 4. Tracker 생성 (JSONL)
**파일:** `internal/skill/tracker.go`, `internal/skill/tracker_test.go` (신규)

두 개의 JSONL 파일: `{dataDir}/skill-usage.jsonl`, `{dataDir}/skill-patterns.jsonl`

```go
type Tracker struct { usagePath, patternPath string }
func NewTracker(dataDir string) *Tracker
func (t *Tracker) RecordUsage(UsageRecord) error
func (t *Tracker) RecordPattern(PatternRecord) error
func (t *Tracker) LoadPatterns() ([]PatternRecord, error)
func (t *Tracker) UsageStats() (map[string]int, error)
```

제네릭 `appendJSONL[T]`와 `readJSONL[T]` 헬퍼 사용. 파일 없으면 빈 결과 반환.

### 5. Creator 생성 (CRUD + Promote + hot-reload)
**파일:** `internal/skill/creator.go`, `internal/skill/creator_test.go` (신규)

```go
type Creator struct {
    store    *wiki.Store
    tracker  *Tracker
    registry *Registry // optional, Promote() 시 registry.Add() 호출
}
func NewCreator(store *wiki.Store, tracker *Tracker, registry *Registry) *Creator
func (c *Creator) Create(CreateParams) (*Skill, error)   // wiki/skills/{name}.md 생성
func (c *Creator) Delete(name string) error
func (c *Creator) Promote(name string) error             // status→active, source→promoted, registry.Add()
```

Create는 `wiki.Store.Create()`로 page 생성. Extra에 name, description, trigger, status, source, required_tools 저장.
Promote는 `wiki.Store.Read()` → Extra["status"]="active" → `store.Update()` → `registry.Add()`.

테스트: Create→Read 확인, 중복 생성 에러, Delete 확인, Promote 후 registry에 존재 확인.

### 6. CLI 명령 (cmd_skill.go)
**파일:** `cmd/elnath/cmd_skill.go` (신규), `cmd/elnath/commands.go` (수정)

서브커맨드: list [--all], show <name>, create <name>, edit <name>, delete <name>, stats

commands.go의 commands map에 `"skill": cmdSkill` 추가.

### 7. Runtime/Scheduler/Telegram 통합
**파일:** `cmd/elnath/runtime.go`, `internal/scheduler/task.go`, `internal/telegram/shell.go`

**runtime.go:**
- `buildExecutionRuntime()` 내부, skillReg 생성(line ~272) 이후:
  ```go
  skillTracker := skill.NewTracker(cfg.DataDir)
  skillCreator := skill.NewCreator(wikiStore, skillTracker, skillReg)
  ```
- executionRuntime struct에 `skillCreator`, `skillTracker` 필드 추가
- Tool 등록: `reg.Register(tools.NewSkillTool(skillCreator, skillReg))`
- Prompt: `b.Register(prompt.NewSkillGuidanceNode(64))`
- skill-promote task 핸들러 추가 (runTask에서 type 분기)

**scheduler/task.go:36:**
- validation에 `"skill-promote"` 타입 추가

**telegram/shell.go:**
- `/skill-list`, `/skill-create` 명령 처리
- 승격 알림 포맷

## 완료 기준
- [ ] `go test -race ./internal/skill/... ./cmd/elnath/... ./internal/scheduler/...` 전체 PASS
- [ ] `make build` 성공
- [ ] `go vet ./...` 경고 없음
- [ ] `elnath skill list` 정상 동작
- [ ] Registry.Load()가 draft skill 필터링

## 참고 문서
- Spec: `docs/specs/PHASE-C2-SKILL-EMERGENCE.md`
- Impl plan: `docs/specs/PHASE-C2-IMPL-PLAN.md` (Tasks 1-7)
- 기존 C-1 spec: `docs/specs/PHASE-C1-SKILL-SYSTEM.md`
