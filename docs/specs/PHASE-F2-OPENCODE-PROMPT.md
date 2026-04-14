# OpenCode Delegation Prompt: Phase F-2 Agent-task Lesson Extraction

대상 spec: `docs/specs/PHASE-F2-AGENT-LESSONS.md`

2 phase 구성. 각 phase 완료 후 `go test -race ./... && go vet ./... && make build` 게이트. 실패하면 같은 phase 에서 fix, 다음 phase 로 넘어가지 말 것.

**원칙 (이전 phase 와 동일):**

- 기존 public API 시그니처 파괴 금지. `RunResult` 는 필드 추가만.
- 테스트는 반드시 `t.Run` table-driven + real file I/O.
- stub/hardcode 금지. 실제 작동 코드만.
- Benchmark/eval 경로 lesson 생성 OFF 보장 (Learning=nil).
- 커밋은 phase 당 squash 1개. push 금지.
- spec 의 설계 의도를 벗어나는 선택 전엔 보고.

---

## Phase 1: learning.ExtractAgent + `agent.RunResult` 확장

```
elnath 프로젝트 (`/Users/stello/elnath/`, feat/telegram-redesign 브랜치) 에서 Phase F-2 Phase 1 을 시작한다.

목표: (A) agent loop 에서 tool stats / iteration / finish reason 을 수집하여 RunResult 에 노출. (B) learning 패키지에 ExtractAgent (agent-task rule set) 추가. 둘 다 pure logic 단계까지만. orchestrator/cmd 통합은 Phase 2.

### 사전 확인

먼저 아래 파일을 읽어 현재 구조 파악:
- internal/agent/agent.go (Run 루프)
- internal/agent/executor.go (tool 실행 entrypoint)
- internal/agent/agent_test.go (기존 RunResult 사용 패턴)
- internal/learning/extractor.go (research rule 패턴 — 참조)
- internal/learning/lesson.go (Lesson struct, deriveID)

### 작업 1: internal/agent/agent.go — RunResult 확장

1.1 타입 추가 (RunResult 근처):

```go
type RunResult struct {
    Messages     []llm.Message
    Usage        llm.UsageStats
    ToolStats    []ToolStat
    Iterations   int
    FinishReason FinishReason
}

type ToolStat struct {
    Name      string
    Calls     int
    Errors    int
    TotalTime time.Duration
}

type FinishReason string

const (
    FinishReasonStop           FinishReason = "stop"
    FinishReasonBudgetExceeded FinishReason = "budget_exceeded"
    FinishReasonAckLoop        FinishReason = "ack_loop"
    FinishReasonError          FinishReason = "error"
)
```

1.2 Run 메서드 수정:

- loop 시작 전 `toolStats := map[string]*toolStatAcc{}` + `var toolStatsMu sync.Mutex`.
- iterations 카운터 증가.
- tool 실행 후 — 성공/실패 / duration 을 stats map 에 merge. 병렬 tool 실행 경로 (`agent.go` / `executor.go` 의 fan-out) 에서도 mutex 로 보호.
- 루프 종료 분기별 FinishReason:
  - `len(toolCalls) == 0` → `FinishReasonStop`
  - ack-retry 한도 초과 break → `FinishReasonAckLoop`
  - for loop 자연 종료 (iter == maxIterations) → `FinishReasonBudgetExceeded`
- 최종 `RunResult` 에 ToolStats (map → slice, name asc 정렬), Iterations, FinishReason 채움.
- error 로 일찍 return 하는 경로는 RunResult 자체가 nil 이므로 수정 불필요.

1.3 tool 실행 지점에서 duration / error 캡처. executor 구조상 이미 `toolExecResult` 같은 타입이 있으면 거기에 `Duration time.Duration` 필드 추가. 없으면 호출 측에서 `start := time.Now(); ...; dur := time.Since(start)`.

1.4 private 헬퍼:

```go
type toolStatAcc struct {
    calls  int
    errors int
    total  time.Duration
}

func mergeToolStat(m map[string]*toolStatAcc, mu *sync.Mutex, name string, dur time.Duration, hadErr bool) {
    mu.Lock()
    defer mu.Unlock()
    acc := m[name]
    if acc == nil {
        acc = &toolStatAcc{}
        m[name] = acc
    }
    acc.calls++
    if hadErr {
        acc.errors++
    }
    acc.total += dur
}

func finalizeToolStats(m map[string]*toolStatAcc) []ToolStat {
    out := make([]ToolStat, 0, len(m))
    for name, acc := range m {
        out = append(out, ToolStat{
            Name:      name,
            Calls:     acc.calls,
            Errors:    acc.errors,
            TotalTime: acc.total,
        })
    }
    sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
    return out
}
```

필요한 import 추가 (`sort`, `sync`, `time`).

### 작업 2: internal/agent/agent_test.go (+ executor_test.go 필요 시)

새 서브테스트 추가:

1. `TestRunResult_ToolStats` — mock tool 2개 (bash, file). bash 2회 호출 중 1회 error. file 1회 호출.
   예상: ToolStats=[{Name:"bash", Calls:2, Errors:1}, {Name:"file", Calls:1, Errors:0}]. 정렬 확인.

2. `TestRunResult_FinishReason_Stop` — tool call 없이 assistant 답변만 → FinishReasonStop, Iterations=1.

3. `TestRunResult_FinishReason_BudgetExceeded` — maxIterations=2 로 제약, tool call 을 매번 요청하는 mock → 2 iteration 소진, FinishReasonBudgetExceeded.

4. `TestRunResult_FinishReason_AckLoop` — ack-only 응답을 3번 반환하는 mock → FinishReasonAckLoop.

5. race 보강: 병렬 tool 실행 mock (다수 tool call 을 한 assistant message 에) → ToolStats 합계가 올바른지, `-race` 통과.

기존 테스트 중 RunResult 를 `*RunResult{...}` literal 로 비교하는 곳이 있으면 새 필드 포함하거나 `cmpopts.IgnoreFields(RunResult{}, "ToolStats", "Iterations", "FinishReason")` 로 우회.

### 작업 3: internal/learning/agent_extractor.go 신규

spec §3.3 그대로 구현. 이미 동일 패키지에 `Extract` 가 있으므로 공통 헬퍼 (`truncate`, `maxLessonTextLen`) 재사용. `self.Lesson` 도 동일 import.

### 작업 4: internal/learning/agent_extractor_test.go

spec §3.4 의 11 케이스 전부. `t.Run` 중첩 table-driven. 공통 fixture:

```go
stubStats := func(pairs ...any) []AgentToolStat {
    // pairs: name, calls, errors, name, calls, errors, ...
    out := []AgentToolStat{}
    for i := 0; i+2 < len(pairs); i += 3 {
        out = append(out, AgentToolStat{
            Name:   pairs[i].(string),
            Calls:  pairs[i+1].(int),
            Errors: pairs[i+2].(int),
        })
    }
    return out
}
```

출력 Text 문자열은 `strings.Contains` 로 검증 (exact match 피함).

### 검증

```bash
cd /Users/stello/elnath
go test -race ./internal/agent/... ./internal/learning/...
go vet ./internal/agent/... ./internal/learning/...
```

통과 후 phase 종료. 회귀로 실패하는 기존 테스트가 있다면 새 필드 영향인지 확인 후 수정.

### 보고

- RunResult 에 추가된 필드와 채워지는 경로
- tool stats race 보호 방식 (mutex / channel / 기타)
- ExtractAgent rule 4개 trigger 확인된 테스트 수
- spec 이탈이 있다면 사유
```

---

## Phase 2: Orchestrator 통합 + cmd wiring + e2e

```
Phase F-2 Phase 2 시작. Phase 1 에서 RunResult 확장 + ExtractAgent 가 완성됐다는 가정.

목표: SingleWorkflow 에 learning hook 연결. runtime 과 daemon 이 기존 learning store/selfState 를 Learning 으로 주입. 실제 agent 실행 시 lesson 이 쌓이는지 e2e 검증.

### 작업 1: internal/orchestrator/types.go — WorkflowInput.Learning 추가

spec §3.5 그대로. import 에 `log/slog`, `github.com/stello/elnath/internal/learning`, `github.com/stello/elnath/internal/self` 추가.

```go
type LearningDeps struct {
    Store     *learning.Store
    SelfState *self.SelfState
    Logger    *slog.Logger
}
```

### 작업 2: internal/orchestrator/single.go

spec §3.6 그대로.

- `applyLearning(input, result, maxIter)` 헬퍼 추가. result 가 nil 이면 no-op.
- `firstMessageSnippet(msg, n)` UTF-8 safe 구현 ([]rune 사용).
- `toAgentToolStats(src []agent.ToolStat)` 변환.
- Run 의 에러 return 경로에서는 호출 안 함 (result 없음).

### 작업 3: internal/orchestrator/single_test.go 확장

테스트 케이스:

1. `TestSingleWorkflow_Learning_Nil` — Learning=nil 이면 기존 동작. store 파일 생성 안 됨.
2. `TestSingleWorkflow_Learning_RuleATrigger` — mock agent 가 "bash" tool 을 5회 실행 중 3회 error. Learning.Store 주입. Run 후 store.List() 에 1개 lesson (Rule A).
3. `TestSingleWorkflow_Learning_PersonaApplied` — SelfState mock 주입. Rule B trigger 시 SelfState.ApplyLessons 호출됨 + Save 호출됨.
4. `TestSingleWorkflow_Learning_StoreAppendError` — Store 가 에러 반환 → Logger 에 warn, 다음 lesson 계속 시도, Run 에러 없이 반환.

테스트용 mock:
- mock provider (agent.Run 에 주입) 가 원하는 ToolStats/FinishReason 을 담은 RunResult 를 리턴하도록 설계. 단, SingleWorkflow 는 agent.Run 을 실제로 호출하므로 provider 수준에서 tool call 시퀀스를 컨트롤해야 함. 이미 `internal/agent/agent_test.go` 나 `executor_test.go` 에 stub provider 패턴이 있을 것 — 그걸 재사용.
- SelfState 는 `t.TempDir()` 에 실제 파일로 로드해서 ApplyLessons 전/후 값 비교.

### 작업 4: cmd/elnath/runtime.go — Learning 주입

현재 runtime 은 learning store 와 selfState 를 이미 생성한다 (F-1 에서). SingleWorkflow 를 호출하는 지점 (또는 workflow dispatcher / router 를 거치는 지점) 을 찾아 `WorkflowInput.Learning = &LearningDeps{...}` 설정.

호출 지점이 여러 곳이면 공통 헬퍼:

```go
func (rt *Runtime) learningDeps() *orchestrator.LearningDeps {
    if rt.learningStore == nil {
        return nil
    }
    return &orchestrator.LearningDeps{
        Store:     rt.learningStore,
        SelfState: rt.selfState,
        Logger:    rt.logger,
    }
}
```

Benchmark 경로에서는 `rt.learningStore == nil` 또는 explicit `benchmarkMode` 조건으로 Learning nil 반환.

### 작업 5: cmd/elnath/cmd_daemon.go

이미 F-1 에서 learningStore / selfState 가 있음. daemon 내 task runner 가 single workflow 를 호출하는 지점에서 동일 Learning 주입.

주의: daemon 의 research task path 는 이미 E-3 에서 별도로 learning 연결됨. 중복 주입하지 말 것 (research TaskRunner 는 SingleWorkflow 를 우회함 — 확인).

### 작업 6: internal/telegram/shell.go 및 internal/daemon/runner.go 에 Learning 주입 (선택)

Phase F-2 핵심 목표가 "일상 task 에서 lesson 이 쌓이게" 이므로 telegram chat path / daemon task path 가 SingleWorkflow 를 거치는지 확인.

- SingleWorkflow 를 직접 호출하면 Learning 주입
- WorkflowRouter 같은 중간 계층을 거치면 라우터 레벨에서 주입
- Team/Ralph/Autopilot 은 이번 phase 에서 주입하지 않음 (Phase F-3)

WorkflowInput 생성 지점을 grep 으로 모두 찾아서 누락 없이 처리:

```bash
grep -rn "WorkflowInput{" --include="*.go" .
grep -rn "WorkflowInput {" --include="*.go" .
```

### 작업 7: 종합 e2e — 수동 smoke

빌드 후 실제 lesson 쌓이는지:

```bash
rm -f ~/.elnath/data/lessons.jsonl  # 깨끗한 상태에서 시작
./elnath run  # 또는 daemon 재기동
# → "list files in current directory" 같은 간단 task
# → 종료 후
./elnath lessons list
./elnath lessons stats
```

Rule C (efficient stop) 이 가장 쉽게 trigger 되므로 단순 task 1-2 회로 확인 가능.

### 전체 검증

```bash
go test -race ./... 2>&1 | tail -50
go vet ./...
make build
./elnath lessons stats
```

### 커밋

`feat: phase F-2 agent-task lesson extraction` 단일 커밋. push 하지 않음.

### 보고

- WorkflowInput 생성 지점 grep 결과 + Learning 주입 여부
- 수동 smoke 결과 (lessons.jsonl 내용 1-2 줄 복붙)
- 의도한 대로 research 경로와 중복 주입 없는지 확인한 근거
- 남은 TODO (Team/Ralph 등) 리스트

### spec 이탈 시 반드시 사전 보고

- RunResult 확장이 기존 테스트 30개 이상을 고친다면 → 보고 후 진행 여부 결정
- WorkflowInput 에 필드 추가 대신 별도 구조체가 더 나을 것 같다면 → 보고
- Topic proxy 로 input.Message 가 아닌 다른 signal 이 더 맞다면 → 보고
```

---

## 작업 중 실패 대응

- Phase 1 에서 RunResult 확장이 기존 테스트를 대량으로 깨뜨리면 임시로 별도 struct (`AgentRunMetrics`) 를 반환하도록 분리하는 선택지 검토. 보고 후 결정.
- Topic 이 너무 짧거나 비어있는 케이스에서 `agent-task` fallback 이 Rule C 에 반복 적용되면 의미 없는 positive lesson 이 쌓일 수 있음. firstMessageSnippet 이 빈 문자열을 반환하면 Rule C skip 하는 가드도 허용.
- Team/Ralph/Autopilot 이 내부적으로 SingleWorkflow 를 재호출하는지 확인. 재호출 시 중복 추출 방지를 위해 workflow 이름을 AgentResultInfo 에 포함시키고 Source="agent:team" 등으로 구분하는 것도 가능하지만, 이번 phase 에서는 SingleWorkflow 직접 호출 경로만 타깃. 내부 재호출이 있으면 그 상위 workflow 에서 Learning=nil 로 덮어써서 중복 방지.

## 완료 기준

- Phase 1 + Phase 2 모두 `go test -race ./... && go vet ./... && make build` 성공
- spec §5 Acceptance Criteria 의 체크박스 전부 green
- `elnath run` 한 번으로 `lessons.jsonl` 에 agent source 의 entry 가 하나라도 생김
- research 경로 회귀 없음 (E-3 테스트 유지)
- 단일 커밋 per phase, push 안 함
