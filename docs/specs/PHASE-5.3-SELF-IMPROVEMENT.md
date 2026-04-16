# Phase 5.3: Self-Improvement Substrate — Closed Loop

**Status**: SPEC READY
**Date**: 2026-04-16
**Scope**: 1.5 sessions
**Predecessor**: Phase 5.2 Ambient Autonomy (PR #9, merged)
**Successors**: Phase 6.1 Operational Polish
**Branch**: `feat/telegram-redesign`
**Ref**: Superiority Design v2.2 §Phase 5.3 — B6 Self-Improvement

---

## 1. Problem Statement

Elnath의 learning 파이프라인은 현재 **열린 루프**다. Lesson 추출(rule-based + LLM), JSONL 저장, prompt injection, persona delta 적용이 모두 동작하지만, 이 학습이 **시스템의 의사결정을 변경하지 않는다**. 같은 프로젝트에서 같은 유형의 태스크를 10번 실패해도, 11번째에 동일한 워크플로우를 선택한다.

### 현재 상태 (F-1 ~ F-6 완료)

| 컴포넌트 | 상태 | 한계 |
|----------|------|------|
| Rule-based lesson extraction | DONE | 성공 패턴 미감지 |
| LLM-based lesson extraction | DONE | outcome에서 routing 변경 없음 |
| JSONL Store (dedup, rotation, filter) | DONE | |
| Persona delta (5 params) | DONE | ±0.02 변화 체감 불가 |
| LessonsNode prompt injection | DONE | Recent(10) — 토픽 무관 |
| CLI (list/clear/rotate/stats) | DONE | |
| Complexity gate + breaker | DONE | |

### 핵심 갭

1. **Outcome 미추적**: 워크플로우 성공/실패 이력이 기록되지 않음
2. **Routing 불변**: Router가 wiki preference를 읽지만, preference가 수동 작성에만 의존
3. **Lesson 무차별 주입**: 10개 최근 lesson이 토픽 무관하게 모든 세션에 주입
4. **Lesson 고립**: lessons.jsonl에만 존재. Wiki FTS5 검색 불가, Obsidian에서 불가시

### 목표

**feedback → lesson → consumer update** 플라이휠의 닫힌 루프를 완성한다:

```
Task 실행 → Outcome 관측 → Lesson 추출 (기존)
                                 ↓
                    Consumer가 실제 동작을 변경:
                    1. Routing preference 자동 갱신
                    2. Context-aware lesson injection
                    3. Wiki knowledge export
                                 ↓
                    다음 Task가 실제로 더 잘 수행됨
```

### 경쟁 분석

| 기능 | Hermes | Claude Code | Elnath (현재) | Elnath (5.3 후) |
|------|--------|-------------|--------------|-----------------|
| Outcome 추적 | 없음 | 없음 | 없음 | **JSONL store** |
| Routing self-learning | 없음 (라우터 없음) | 없음 (라우터 없음) | 없음 | **자동 wiki update** |
| Context-aware recall | MemoryProvider trajectory 분리 | Sonnet semantic recall | Recent(10) 무차별 | **topic filter** |
| Knowledge export | FTS5 transcript | flat file memory | lessons.jsonl only | **wiki FTS5** |

---

## 2. Design Decisions

| 결정 | 선택 | 근거 |
|------|------|------|
| Outcome 저장 | 별도 JSONL (`outcomes.jsonl`) | lessons.jsonl과 용도 분리. learning.Store 패턴 재활용. 메모리 분리 원칙 (Hermes 참조) |
| Outcome 기록 시점 | `runtime.go` — workflow 완료 직후 | orchestrator 내부가 아닌 runtime 레벨. WorkflowResult + RoutingContext 모두 접근 가능 |
| Routing advisor 트리거 | Outcome 기록 시 inline 계산 | 별도 goroutine/scheduler 불필요. outcome 수가 적어 계산 비용 무시 가능 |
| Preference 갱신 방식 | wiki 페이지 직접 rewrite | 기존 `LoadWorkflowPreference` 경로 그대로 활용. 수동 override와 자동 갱신 공존 |
| 수동 override 보호 | `Extra["source"]` 필드 | `source: "self-improvement"` 인 경우만 자동 갱신. 수동 작성(`source` 없음)은 절대 덮어쓰지 않음 |
| Lesson 필터링 | `RenderState.ProjectID` 추가 → topic filter | LLM 호출 없이 기존 `ListFiltered(Filter{Topic})` 활용. Hermes semantic recall의 비용 효율적 대안 |
| Wiki export | boot task (ambient) | 별도 goroutine/daemon 코드 불필요. `wiki/boot/export-lessons.md` 선언으로 주기 실행 |
| Success 판정 | FinishReason 기반 | `stop` = success, `budget_exceeded`/`error`/`ack_loop` = failure. 빈 문자열은 기록 제외 (정보 없음) |
| 최소 샘플 | 5건 | 5건 미만이면 routing advice 생성 안 함. 통계적 의미 없는 조기 판단 방지 |
| Window 크기 | 최근 30건 | 오래된 outcome은 현재 시스템 상태와 무관. 30건 sliding window |

---

## 3. Architecture

### 3.1 Component A: Outcome Tracking

```
runtime.go (workflow 완료 후)
  │
  ├─ WorkflowResult
  │   { Workflow, FinishReason, Iterations, Usage, ToolStats }
  ├─ RoutingContext
  │   { ProjectID, EstimatedFiles, ExistingCode, VerificationHint }
  ├─ Intent (conversation.Intent)
  │
  ▼
learning.OutcomeStore.Append(OutcomeRecord{
    ProjectID, Intent, Workflow, FinishReason,
    Success, Duration, Cost, Iterations,
    Timestamp,
})
  │
  ▼
learning.RoutingAdvisor.Advise(projectID) → *WorkflowPreference
  │
  ▼
wiki.SaveWorkflowPreference(store, projectID, pref)
  │ (source: "self-improvement" — 수동 작성 보호)
```

### 3.2 Component B: Context-aware Lesson Injection

```
prompt.RenderState
  │ ProjectID 필드 추가 (기존 routeCtx에서 전달)
  ▼
LessonsNode.Render(ctx, state)
  │ state.ProjectID가 비어있지 않으면:
  │   store.ListFiltered(Filter{Topic: projectID}) 시도
  │   결과 < 3개이면 Recent(maxEntries)로 fallback
  │ BenchmarkMode이면 빈 문자열
  ▼
"Relevant lessons for this project:\n\n- ..."
```

### 3.3 Component C: Lesson Wiki Export

```
wiki/boot/export-lessons.md (PageType: boot-task)
  │ Schedule: daily 03:00
  │ Prompt: "lessons.jsonl에서 high-confidence lesson을
  │          wiki/self/lessons.md로 export"
  ▼
ambient.Scheduler → daemon runner
  │ runner가 elnath lessons list --confidence high 실행
  │ 결과를 wiki 페이지로 작성
  ▼
wiki/self/lessons.md (FTS5 검색 가능)
```

---

## 4. Deliverables

### 4.1 New: `internal/learning/outcome.go`

```go
package learning

import "time"

type OutcomeRecord struct {
    ID           string    `json:"id"`
    ProjectID    string    `json:"project_id"`
    Intent       string    `json:"intent"`
    Workflow     string    `json:"workflow"`
    FinishReason string    `json:"finish_reason"`
    Success      bool      `json:"success"`
    Duration     float64   `json:"duration_s"`
    Cost         float64   `json:"cost"`
    Iterations   int       `json:"iterations"`
    Timestamp    time.Time `json:"timestamp"`
}

func IsSuccessful(finishReason string) bool {
    return finishReason == "stop"
}

func ShouldRecord(finishReason string) bool {
    return finishReason != ""
}
```

### 4.2 New: `internal/learning/outcome_store.go`

JSONL append-only store. `learning.Store`와 동일한 패턴: mutex, atomic write, `os.O_APPEND`.

```go
type OutcomeStore struct {
    mu   sync.Mutex
    path string
}

func NewOutcomeStore(path string) *OutcomeStore

func (s *OutcomeStore) Append(record OutcomeRecord) error
func (s *OutcomeStore) Recent(n int) ([]OutcomeRecord, error)
func (s *OutcomeStore) ForProject(projectID string, n int) ([]OutcomeRecord, error)
func (s *OutcomeStore) Rotate(keepLast int) error
func (s *OutcomeStore) AutoRotateIfNeeded(keepLast int) error
```

**Append 로직:**
1. Lock
2. `os.MkdirAll` parent dir
3. `record.Timestamp`이 zero면 `time.Now().UTC()`
4. `record.ID`가 빈 문자열이면 `SHA256(projectID+intent+workflow+timestamp)[:8]`
5. `json.NewEncoder(file).Encode(record)` — JSONL 한 줄
6. Close + Unlock

**ForProject 로직:**
1. 전체 읽기
2. projectID filter
3. 역순 정렬 (newest first)
4. n개 slice

### 4.3 New: `internal/learning/routing_advisor.go`

Outcome 통계를 기반으로 WorkflowPreference를 생성한다.

```go
type RoutingAdvisor struct {
    store      *OutcomeStore
    windowSize int // default 30
    minSamples int // default 5
}

func NewRoutingAdvisor(store *OutcomeStore) *RoutingAdvisor

func (a *RoutingAdvisor) Advise(projectID string) (*routing.WorkflowPreference, error)
```

**Advise 알고리즘:**

1. `store.ForProject(projectID, a.windowSize)` — 최근 30건
2. Intent별로 그룹핑
3. 각 intent 내에서 workflow별 성공률 계산:
   ```
   success_rate = count(Success=true) / total
   ```
4. 판정 규칙:
   - intent에 대해 sample >= minSamples(5)인 워크플로우만 평가
   - 성공률 100% 워크플로우가 있으면 → `PreferredWorkflows[intent] = workflow`
   - 성공률 < 30% 워크플로우가 있으면 → `AvoidWorkflows` 에 추가
   - 성공률이 비슷하면 (차이 < 20%) → preference 생성 안 함 (기존 heuristic 유지)
5. 결과가 비어있으면 nil 반환 (자동 갱신 스킵)

**예시:**
```
projectID=elnath, 최근 30건:
  complex_task + team: 10건 중 3건 성공 (30%)
  complex_task + ralph: 8건 중 7건 성공 (87.5%)
  simple_task + single: 12건 중 11건 성공 (91.7%)

→ PreferredWorkflows: {"complex_task": "ralph"}
→ AvoidWorkflows: [] (team은 30%이므로 경계선 — avoid 안 함)
```

### 4.4 New: `internal/wiki/routing_write.go`

```go
func SaveWorkflowPreference(store *Store, projectID string, pref *routing.WorkflowPreference) error
```

**로직:**
1. 기존 페이지 읽기 시도
2. 기존 페이지가 있고 `Extra["source"] != "self-improvement"` → **return nil** (수동 작성 보호)
3. 기존 페이지가 없거나 `source == "self-improvement"`:
   - YAML frontmatter + markdown body 생성
   - `Extra["source"] = "self-improvement"`, `Extra["updated_at"] = now`
   - `store.Create` 또는 `store.Update`

### 4.5 Modified: `internal/prompt/node.go`

```go
type RenderState struct {
    // ... existing fields ...
    ProjectID string // NEW: routing context에서 전달
}
```

### 4.6 Modified: `internal/prompt/lessons_node.go`

```go
// LessonLister 인터페이스는 기존 그대로 유지 (backward compat):
type LessonLister interface {
    Recent(n int) ([]learning.Lesson, error)
}

// LessonFilteredLister는 topic 필터링을 지원하는 확장 인터페이스.
// learning.Store가 이미 양쪽 모두 구현.
type LessonFilteredLister interface {
    LessonLister
    ListFiltered(f learning.Filter) ([]learning.Lesson, error)
}

// LessonsNode는 LessonLister로 생성 (기존 호환 유지).
// store가 LessonFilteredLister도 구현하면 topic 필터링 활용.
type LessonsNode struct {
    priority   int
    store      LessonLister // 기존 인터페이스 유지
    maxEntries int
    maxChars   int
}

func (n *LessonsNode) Render(_ context.Context, state *RenderState) (string, error) {
    // 기존 nil/benchmark 체크 동일

    var lessons []learning.Lesson
    var err error

    if state != nil && state.ProjectID != "" {
        if fl, ok := n.store.(LessonFilteredLister); ok {
            lessons, err = fl.ListFiltered(learning.Filter{
                Topic:   state.ProjectID,
                Limit:   n.maxEntries,
                Reverse: true,
            })
            if err != nil || len(lessons) < 3 {
                lessons, err = n.store.Recent(n.maxEntries)
            }
        } else {
            lessons, err = n.store.Recent(n.maxEntries)
        }
    } else {
        lessons, err = n.store.Recent(n.maxEntries)
    }

    // 기존 렌더링 로직 동일
}
```

**Backward compatibility**: `LessonsNode`는 `LessonLister`로 생성하므로 기존 mock에 변경 없음. `learning.Store`가 `LessonFilteredLister`를 자동 충족하므로 production에서는 topic 필터링 활성화. wiki.Store.Upsert 활용 (Create/Update 분기 불필요).

### 4.7 Modified: `cmd/elnath/runtime.go`

**Outcome 기록 + routing advisor 호출:**

```go
// runTask 또는 runMessage의 workflow 완료 직후:

if rt.outcomeStore != nil && routeCtx.ProjectID != "" && learning.ShouldRecord(result.FinishReason) {
    record := learning.OutcomeRecord{
        ProjectID:    routeCtx.ProjectID,
        Intent:       string(intent),
        Workflow:     result.Workflow,
        FinishReason: result.FinishReason,
        Success:      learning.IsSuccessful(result.FinishReason),
        Duration:     elapsed.Seconds(),
        Cost:         result.Usage.TotalCost,
        Iterations:   result.Iterations,
    }
    if err := rt.outcomeStore.Append(record); err != nil {
        rt.app.Logger.Warn("outcome store: append failed", "error", err)
    }
    _ = rt.outcomeStore.AutoRotateIfNeeded(300)

    if pref, err := rt.routingAdvisor.Advise(routeCtx.ProjectID); err == nil && pref != nil {
        if err := wiki.SaveWorkflowPreference(rt.wikiStore, routeCtx.ProjectID, pref); err != nil {
            rt.app.Logger.Warn("routing advisor: wiki save failed", "error", err)
        }
    }
}

// executionRuntime struct에 추가할 필드:
//   outcomeStore   *learning.OutcomeStore
//   routingAdvisor *learning.RoutingAdvisor
```

**RenderState에 ProjectID 전달:**

```go
renderState := &prompt.RenderState{
    // ... existing fields ...
    ProjectID: routeCtx.ProjectID, // NEW
}
```

**Runtime initialization (learningDeps 근처):**

```go
outcomePath := filepath.Join(cfg.DataDir, "outcomes.jsonl")
outcomeStore := learning.NewOutcomeStore(outcomePath)
routingAdvisor := learning.NewRoutingAdvisor(outcomeStore)
```

### 4.8 New: `wiki/boot/export-lessons.md`

```markdown
---
title: "Export Lessons to Wiki"
type: boot-task
schedule: "daily 03:00"
silent: true
---

Check lessons.jsonl for high-confidence lessons that haven't been exported to wiki yet.
For each new lesson, append it to wiki/self/lessons.md with date, topic, and text.
Keep the page under 200 lines — rotate oldest entries when exceeded.
```

이 boot task는 ambient scheduler가 자동 실행한다. daemon이 agent를 실행하고, agent가 `elnath lessons list` + wiki write를 수행한다. 별도 Go 코드 불필요 — 기존 ambient + tool 인프라 재활용.

---

## 5. File Summary

### New Files (5 + tests)

| File | LOC (est.) | 역할 |
|------|-----------|------|
| `internal/learning/outcome.go` | ~30 | OutcomeRecord + IsSuccessful |
| `internal/learning/outcome_store.go` | ~120 | JSONL Append/Recent/ForProject |
| `internal/learning/outcome_store_test.go` | ~150 | Store 테스트 (race 포함) |
| `internal/learning/routing_advisor.go` | ~100 | Outcome 통계 → WorkflowPreference |
| `internal/learning/routing_advisor_test.go` | ~180 | 판정 규칙 테스트 |
| `internal/wiki/routing_write.go` | ~60 | SaveWorkflowPreference |
| `internal/wiki/routing_write_test.go` | ~80 | 수동 보호 + 자동 갱신 테스트 |
| `wiki/boot/export-lessons.md` | ~10 | Boot task 선언 |

### Modified Files (4)

| File | 변경 내용 |
|------|----------|
| `internal/prompt/node.go` | RenderState에 ProjectID 필드 추가 |
| `internal/prompt/lessons_node.go` | LessonFilteredLister 인터페이스, topic 필터링 |
| `internal/prompt/lessons_node_test.go` | 필터링 + fallback 테스트 추가 |
| `cmd/elnath/runtime.go` | outcomeStore/routingAdvisor 초기화, outcome 기록, RenderState.ProjectID |

---

## 6. Acceptance Criteria

- [ ] `go test -race ./internal/learning/... ./internal/wiki/... ./internal/prompt/... ./cmd/elnath/...` 통과
- [ ] `go vet ./...` 경고 없음
- [ ] `make build` 성공
- [ ] Workflow 완료 후 `outcomes.jsonl`에 record 추가됨
- [ ] 같은 프로젝트에서 5건 이상 outcome 쌓이면 `routing-preferences.md` 자동 갱신
- [ ] 수동 작성된 routing-preferences.md는 자동 갱신으로 덮어쓰지 않음
- [ ] LessonsNode가 ProjectID 기반으로 관련 lesson 우선 표시
- [ ] ProjectID가 없거나 관련 lesson < 3개이면 Recent(10) fallback
- [ ] BenchmarkMode에서 LessonsNode → 빈 문자열 (기존 동작 유지)
- [ ] `wiki/boot/export-lessons.md` 페이지가 파싱 가능 (ambient scanner)
- [ ] 기존 테스트 전부 통과 (regression 없음)

---

## 7. Risk

| Risk | Mitigation |
|------|-----------|
| Routing advice 조기 수렴 (5건으로 판단 너무 이름) | minSamples=5는 최소치. 실 운영에서 너무 이르면 10으로 상향. window=30으로 최근 데이터만 반영 |
| 자동 routing이 수동 설정 덮어씀 | `Extra["source"]` 체크로 수동 보호. 자동 생성 페이지만 갱신 |
| outcomes.jsonl 무한 성장 | ForProject가 이미 n개만 반환. 추후 rotation 필요 시 Store.Rotate 패턴 재활용 |
| LessonsNode fallback이 너무 자주 발생 | ProjectID 외에 topic 필드가 project명과 불일치 가능. LLM extractor에서 topic=projectID 설정 확인 필요 |
| Boot task export가 wiki 페이지 비대화 | export-lessons.md prompt에 200줄 제한 명시. 오래된 건 rotate |
| Routing advisor inline 실행 지연 | outcomes 30건 읽기 + 통계 계산은 < 1ms. 병목 없음 |

---

## 8. Future Work (Phase 6+)

- **F-5.2 Consolidation**: autoDream 등가. lesson 크로스 세션 통합, 중복 제거, 상충 해소
- **Model preference learning**: task type별 모델 성공률 추적 → 자동 모델 선택
- **Lesson decay**: 30일 반감기 기반 가중치 감소
- **MagicDocs ↔ Learning cross-feed**: wiki 지식 추출과 lesson 추출의 통합 파이프라인
- **Memory API 형식화**: wiki/session/lesson/outcome을 통합 인터페이스로 추상화 (Phase 7)

---

## 9. Implementation Order

Phase 5.3은 3개 독립 컴포넌트로 분해된다. A가 핵심, B/C는 A와 병렬 가능:

```
A: Outcome Store + Routing Advisor + Wiki Write + Runtime Integration
   ├── outcome.go + outcome_store.go (+ test)
   ├── routing_advisor.go (+ test)
   ├── wiki/routing_write.go (+ test)
   └── runtime.go 수정

B: Context-aware Lesson Injection (A와 병렬)
   ├── prompt/node.go (ProjectID 추가)
   ├── prompt/lessons_node.go (필터링)
   └── prompt/lessons_node_test.go

C: Lesson Wiki Export (A/B와 병렬)
   └── wiki/boot/export-lessons.md
```

A → 검증 → B/C 병렬 → 통합 테스트

총 예상 변경: ~550 production LOC + ~410 test LOC = ~960 total LOC
