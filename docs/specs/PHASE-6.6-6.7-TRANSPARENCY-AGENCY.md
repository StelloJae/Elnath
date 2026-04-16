# Phase 6.6+6.7: Decision Transparency + User Agency

**Status**: SPEC READY
**Date**: 2026-04-16
**Scope**: 1.5 sessions
**Predecessor**: Phase 5.3 Self-Improvement Substrate
**Branch**: `feat/telegram-redesign`

---

## 1. Problem Statement

Phase 5.3에서 닫힌 자기개선 루프(outcome → routing advisor → wiki preference)를 구현했지만, 사용자가 이 루프에 참여할 수 없다:

1. **투명성 부재**: 어떤 intent가 분류되었고, 왜 이 워크플로우가 선택되었는지 볼 수 없음
2. **피드백 불가**: "이건 ralph가 더 나아"라고 명시적으로 알릴 수 없음
3. **교훈 관리 불가**: Telegram에서 lesson을 추가/삭제할 수 없음
4. **실수 취소 불가**: 잘못 보낸 태스크를 취소할 수 없음

인간이 루프 안에 있어야 자기개선이 신뢰할 수 있는 루프가 된다.

---

## 2. Design Decisions

| 결정 | 선택 | 근거 |
|------|------|------|
| `explain last` 데이터소스 | OutcomeRecord 확장 + wiki pref | 항상 기록되는 outcome store 활용. eval-only audit log 의존 제거 |
| OutcomeRecord 확장 필드 | Input(snippet), EstimatedFiles, ExistingCode, PreferenceUsed | 라우팅 결정 재구성에 필요한 최소 필드만 |
| `/remember` 대상 | learning.Store (lesson) | ChatSessionBinder는 세션 바인딩용. 사용자 교훈은 lesson store에 저장 |
| `/forget` 인터페이스 | ID prefix 기반 | 기존 `learning.Store.Delete(idPrefixes...)` 재활용 |
| `/override` 방식 | wiki preference 직접 작성 (source: "user") | 자동 advisor가 덮어쓰지 않음. 영구적. `/override clear`로 해제 |
| `/undo` 범위 | pending/running task 취소 | 세션이 append-only이므로 메시지 롤백 불가. daemon queue 취소로 정의 |
| Input snippet 길이 | 100 runes | 프라이버시 + 파일 크기. 라우팅 맥락 파악에 충분 |

---

## 3. Deliverables

### 3.1 Modified: `internal/learning/outcome.go`

OutcomeRecord에 라우팅 컨텍스트 필드 추가:

```go
type OutcomeRecord struct {
    // ... existing fields ...
    InputSnippet   string `json:"input_snippet,omitempty"`
    EstimatedFiles int    `json:"estimated_files,omitempty"`
    ExistingCode   bool   `json:"existing_code,omitempty"`
    PreferenceUsed bool   `json:"preference_used,omitempty"`
}
```

### 3.2 Modified: `cmd/elnath/runtime.go`

Outcome 기록 시 새 필드 채우기:

```go
record := learning.OutcomeRecord{
    // ... existing fields ...
    InputSnippet:   runeSnippet(userInput, 100),
    EstimatedFiles: routeCtx.EstimatedFiles,
    ExistingCode:   routeCtx.ExistingCode,
    PreferenceUsed: pref != nil,
}
```

`runeSnippet` 헬퍼: rune 단위로 100자 자르기 (이미 `firstMessageSnippet` 패턴이 orchestrator/learning.go에 존재).

### 3.3 New: `cmd/elnath/cmd_explain.go`

`elnath explain last` 명령.

```go
func cmdExplain(ctx context.Context, args []string) error
```

`last` 서브커맨드:
1. `outcomeStore.Recent(1)` → 마지막 OutcomeRecord
2. `wiki.LoadWorkflowPreference(wikiStore, record.ProjectID)` → 현재 preference
3. `routingAdvisor.Advise(record.ProjectID)` → advisor가 현재 추천하는 것
4. 출력 포맷:

```
Last routing decision (2026-04-16 15:30:00 UTC)

  Input:     "fix the authentication bug in..."
  Intent:    complex_task
  Workflow:  ralph
  Result:    ✓ success (42 iterations, 38.2s)

  Why this workflow?
    • Preference: complex_task → ralph (source: self-improvement)
    • Context: existing_code=true, estimated_files=3

  Project "elnath" routing stats (last 30):
    complex_task: ralph 87% (7/8), team 40% (2/5)
    simple_task:  single 92% (11/12)
```

`explain history [n]` (선택적 확장): 최근 n건의 outcome 요약 테이블.

### 3.4 Modified: `internal/telegram/shell.go`

4개 슬래시 명령 추가:

**`/remember <text>`**
```go
case "/remember":
    text := strings.Join(fields[1:], " ")
    if text == "" {
        return "Usage: /remember <lesson text>", nil
    }
    lesson := learning.Lesson{
        Text:       text,
        Source:     "user:telegram",
        Confidence: "high",
        Topic:      s.currentProjectID(), // 현재 프로젝트 context
    }
    if err := s.learningStore.Append(lesson); err != nil {
        return fmt.Sprintf("Failed: %v", err), nil
    }
    return fmt.Sprintf("Remembered (ID: %s)", lesson.ID), nil
```

**`/forget <id-prefix>`**
```go
case "/forget":
    if len(fields) < 2 {
        return "Usage: /forget <lesson-id-prefix>", nil
    }
    n, err := s.learningStore.Delete(fields[1])
    if err != nil {
        return fmt.Sprintf("Failed: %v", err), nil
    }
    return fmt.Sprintf("Forgot %d lesson(s)", n), nil
```

**`/override <intent> <workflow>` / `/override clear`**
```go
case "/override":
    if len(fields) < 2 {
        return "Usage: /override <intent> <workflow> | /override clear", nil
    }
    projectID := s.currentProjectID()
    if projectID == "" {
        return "No active project context", nil
    }
    if fields[1] == "clear" {
        // source를 "self-improvement"로 설정하여 advisor가 다시 관리하게 함
        return s.clearOverride(projectID)
    }
    if len(fields) < 3 {
        return "Usage: /override <intent> <workflow>", nil
    }
    intent, workflow := fields[1], fields[2]
    pref := &routing.WorkflowPreference{
        PreferredWorkflows: map[string]string{intent: workflow},
    }
    // source: "user" → advisor가 덮어쓰지 않음
    if err := wiki.SaveUserWorkflowPreference(s.wikiStore, projectID, pref); err != nil {
        return fmt.Sprintf("Failed: %v", err), nil
    }
    return fmt.Sprintf("Override set: %s → %s for project %s", intent, workflow, projectID), nil
```

**`/undo`**
```go
case "/undo":
    // 마지막 pending/running task 취소
    cancelled, err := s.cancelLastTask(ctx)
    if err != nil {
        return fmt.Sprintf("Failed: %v", err), nil
    }
    if !cancelled {
        return "No pending or running task to cancel", nil
    }
    return "Last task cancelled", nil
```

### 3.5 Modified: `internal/telegram/shell.go` — Dependencies

`Shell` struct에 추가 필드:

```go
type Shell struct {
    // ... existing fields ...
    learningStore  *learning.Store    // for /remember, /forget
    wikiStore      *wiki.Store        // for /override
    outcomeStore   *learning.OutcomeStore // for context
}
```

이 의존성들은 `Shell` 생성 시 주입. 기존 `cmd_daemon.go`에서 Shell 생성하는 곳에서 전달.

### 3.6 New: `internal/wiki/routing_write.go` 확장

`SaveUserWorkflowPreference` — `/override`에서 사용. 기존 `SaveWorkflowPreference`와 달리 `source: "user"` 설정.

```go
func SaveUserWorkflowPreference(store *Store, projectID string, pref *routing.WorkflowPreference) error
```

기존 페이지가 있으면 preference를 **merge** (기존 + 새 것). 기존 페이지가 없으면 생성.
`Extra["source"] = "user"` → 자동 advisor가 덮어쓰지 않음.

### 3.7 `/help` 업데이트

Telegram `/help` 텍스트에 4개 명령 추가.

---

## 4. File Summary

### New Files (2)

| File | LOC (est.) | 역할 |
|------|-----------|------|
| `cmd/elnath/cmd_explain.go` | ~120 | `elnath explain last/history` |
| `cmd/elnath/cmd_explain_test.go` | ~80 | explain 테스트 |

### Modified Files (6)

| File | 변경 내용 |
|------|----------|
| `internal/learning/outcome.go` | OutcomeRecord에 4 필드 추가 |
| `cmd/elnath/runtime.go` | outcome 기록 시 새 필드 채우기 + runeSnippet 헬퍼 |
| `cmd/elnath/commands.go` | `"explain": cmdExplain` 등록 |
| `internal/telegram/shell.go` | 4개 슬래시 명령 + learningStore/wikiStore/outcomeStore 의존성 |
| `internal/wiki/routing_write.go` | SaveUserWorkflowPreference 추가 |
| `cmd/elnath/cmd_daemon.go` | Shell 생성 시 새 의존성 주입 |

---

## 5. Acceptance Criteria

- [ ] `elnath explain last` — 마지막 라우팅 결정 + 통계 출력
- [ ] `/remember "text"` → lessons.jsonl에 추가, ID 반환
- [ ] `/forget <id>` → lesson 삭제, 삭제 수 반환
- [ ] `/override complex_task ralph` → wiki preference에 user override 설정
- [ ] `/override clear` → user override 제거, advisor가 다시 관리
- [ ] `/undo` → 마지막 pending task 취소 (없으면 메시지)
- [ ] `/help` — 새 명령 4개 표시
- [ ] `go test -race ./...` 전체 통과
- [ ] `go build ./cmd/elnath/` 성공

---

## 6. Risk

| Risk | Mitigation |
|------|-----------|
| /override가 잘못된 워크플로우 이름 허용 | 유효한 워크플로우 목록(single/team/autopilot/ralph/research) 검증 |
| /remember로 무한 lesson 축적 | 기존 Store.AutoRotateIfNeeded 활용 |
| InputSnippet에 민감 정보 | 100 rune 제한 + OutcomeStore redactor가 이미 적용 |
| /undo race condition | daemon queue의 기존 동시성 보호 재활용 |
| explain last에 outcome 없음 (첫 실행) | "No routing decisions recorded yet" 메시지 |

---

## 7. Implementation Order

두 컴포넌트는 병렬 가능:

```
A: CLI explain (독립)
   ├── outcome.go 필드 추가
   ├── runtime.go 필드 채우기
   ├── cmd_explain.go 신규
   └── commands.go 등록

B: Telegram slash commands (독립)
   ├── shell.go 4개 명령
   ├── routing_write.go 확장
   ├── cmd_daemon.go 의존성 주입
   └── /help 업데이트
```

A와 B 병렬 → 통합 테스트 → QA
