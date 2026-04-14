# Phase F-2: Agent-task Lesson Extraction

**Status:** SPEC READY
**Predecessor:** Phase F-1 (Lessons Operational Tooling) DONE
**Successor:** Phase F-3 (team/ralph/autopilot 로 확장) 또는 LLM-based extraction
**Branch:** `feat/telegram-redesign`
**Ref:** Phase E-3 §7 Future Work — "Agent task (non-research) lesson 추출"

---

## 1. Goal

지금까지 lesson 추출은 `elnath research` 경로에서만 발생했다. F-2 는 **SingleWorkflow agent loop** 끝단에 extractor 를 붙여 일반 task 에서도 lesson 을 만든다. 데이터 축적 속도를 대폭 늘려 F-1 에서 만든 운영 도구가 실제 가치를 낸다.

Signal 관점에서 agent task 는 research 보다 빈약하다 (Topic/Confidence 없음). 대신 **tool-level stats + iteration budget + finish reason** 을 새 signal 로 도입한다.

**핵심 플라이휠 확장:**

```
(기존) research.Loop → lesson
(신규) SingleWorkflow.Run → AgentResultInfo → lesson
                                │
                                ├─ tool 반복 실패      → caution
                                ├─ maxIter 고갈        → caution
                                ├─ 효율적 완료         → persistence
                                └─ 과도한 token 사용   → verbosity↓
```

**Out of scope:**

- TeamWorkflow / RalphWorkflow / AutopilotWorkflow 통합 (Phase F-3)
- LLM-based extraction
- Topic 자동 분류 (LLM/embedding). 이번엔 `input.Message` 앞 80자 truncate 를 topic proxy 로 사용
- User feedback 기반 lesson (👍/👎)
- Lesson dedup (현재 Append 는 중복 허용)
- Per-user / per-session partitioning

## 2. Architecture Overview

```
┌──────────────────────────────────────┐
│ agent.Agent.Run (loop)               │
│   - accumulates ToolStats            │  (NEW)
│   - tracks FinishReason              │  (NEW)
│   - returns extended RunResult       │
└────────┬─────────────────────────────┘
         │
         ▼
┌──────────────────────────────────────┐
│ orchestrator.SingleWorkflow.Run      │
│   - maps RunResult → AgentResultInfo │  (NEW)
│   - calls applyAgentLearning if deps │  (NEW)
└────────┬─────────────────────────────┘
         │
         ▼
┌──────────────────────────────────────┐        ┌──────────────────────┐
│ learning.ExtractAgent(info)          │        │ learning.Store       │
│   - 4 rule-based extractor           │───────▶│ .Append              │
└────────┬─────────────────────────────┘        └──────────────────────┘
         │
         ▼
┌──────────────────────────────────────┐
│ self.SelfState.ApplyLessons + Save   │
└──────────────────────────────────────┘
```

**설계 결정:**

1. **Agent 레벨에서 tool stats 수집** — workflow 레벨에서는 늦다. `agent.Run` 이 이미 tool call 루프를 돌고 있으므로 거기서 카운트.
2. **`RunResult` 확장** — 기존 `{Messages, Usage}` 에 `ToolStats`, `Iterations`, `FinishReason` 추가. backwards-compatible (필드 추가만).
3. **Workflow 레벨 통합** — `SingleWorkflow.Run` 이 `WorkflowInput.Learning` (optional) 을 받아 extractor 호출. 기존 호출자는 nil 이면 no-op.
4. **Extractor 분리** — `learning.ExtractAgent` 는 `Extract` (research 용) 와 다른 rule set. 같은 store/Lesson 타입 공유.
5. **Topic proxy** — 명시적 topic 없음. `firstMessageSnippet(input.Message, 80)` 헬퍼로 생성. 빈 문자열이면 `"agent-task"` fallback.
6. **Benchmark off** — `WorkflowInput.Learning == nil` 이면 skip. eval harness 는 nil 주입.
7. **TeamWorkflow 는 이번에 건드리지 않음** — Team 은 여러 sub-agent 가 돌고 tool stats 집계가 복잡. 별도 phase.

## 3. Deliverables

### 3.1 Modified: `internal/agent/agent.go` — RunResult 확장

기존 `RunResult` 확장:

```go
// RunResult is returned after the agent loop completes.
type RunResult struct {
    Messages     []llm.Message
    Usage        llm.UsageStats
    ToolStats    []ToolStat   // NEW — per-tool execution stats
    Iterations   int          // NEW — loop iterations used
    FinishReason FinishReason // NEW — why the loop ended
}

// ToolStat aggregates execution outcomes for a single tool across one Run.
type ToolStat struct {
    Name      string
    Calls     int           // total invocations
    Errors    int           // invocations that returned an error
    TotalTime time.Duration // cumulative wall-clock
}

type FinishReason string

const (
    FinishReasonStop           FinishReason = "stop"            // model stopped requesting tools
    FinishReasonBudgetExceeded FinishReason = "budget_exceeded" // hit maxIterations
    FinishReasonAckLoop        FinishReason = "ack_loop"        // ack-only retries exhausted
    FinishReasonError          FinishReason = "error"           // stream/tool fatal error
)
```

`Run` 내부 루프 수정:

1. `toolStats := map[string]*ToolStat{}` 초기화, tool 실행 전후로 카운트.
2. `iterations` 증가, 종료 시 `result.Iterations = iterations`.
3. break 분기에서 `result.FinishReason` 세팅:
   - 정상 break (no tool calls) → `FinishReasonStop`
   - ack loop exhausted → `FinishReasonAckLoop`
   - for loop 탈출 (maxIterations 도달) → `FinishReasonBudgetExceeded`
4. error return 직전 `FinishReasonError` 세팅은 `RunResult` 가 nil 반환되므로 불필요. 에러는 caller 가 따로 처리.

Tool 실행 부분 (기존 `toolExecResult` 주변 코드) 수정 시 주의: 병렬 tool 실행 경로가 있다면 `sync.Mutex` 로 stats 보호. 현재 executor 구조 확인 후 단일 lock 또는 채널 집계.

**파일당 수정 범위:** `agent.go` 약 30-50 LOC 추가. 기존 테스트가 `RunResult` 를 literal 로 비교하지 않으면 호환. 필요 시 테스트에서 새 필드 무시.

### 3.2 Modified: `internal/agent/executor.go` — tool stats 수집

tool 실행 함수가 별도 파일에 있다면 그쪽에 `(name, err, duration)` 를 돌려주는 hook 또는 리턴 필드 추가. Agent 는 이를 받아 map 에 merge.

### 3.3 New: `internal/learning/agent_extractor.go`

```go
package learning

import (
    "fmt"
    "strings"
    "time"

    "github.com/stello/elnath/internal/self"
)

// AgentResultInfo is the minimal view of an agent run consumed by ExtractAgent.
type AgentResultInfo struct {
    Topic            string        // short proxy (e.g., first 80 chars of user message)
    FinishReason     string        // "stop" | "budget_exceeded" | "ack_loop" | "error"
    Iterations       int
    MaxIterations    int
    OutputTokens     int
    InputTokens      int
    TotalCost        float64
    ToolStats        []AgentToolStat
}

type AgentToolStat struct {
    Name      string
    Calls     int
    Errors    int
    TotalTime time.Duration
}

const (
    agentToolFailureThreshold  = 3            // errors on same tool within one run
    agentVerboseOutputTokens   = 50_000       // per run
    agentEfficientIterationPct = 0.3          // iterations / max <= this counts as efficient
    agentStalledReason         = "budget_exceeded"
)

// ExtractAgent derives lessons from a completed agent run using fixed rules.
// Rule set is intentionally separate from Extract (research) — agent tasks have
// different signals (no confidence scores; tool-level outcomes instead).
func ExtractAgent(info AgentResultInfo) []Lesson {
    var lessons []Lesson
    now := time.Now().UTC()
    topic := strings.TrimSpace(info.Topic)
    if topic == "" {
        topic = "agent-task"
    }

    // Rule A: tool failure loop
    for _, ts := range info.ToolStats {
        if ts.Errors >= agentToolFailureThreshold {
            lessons = append(lessons, Lesson{
                Text:       truncate(fmt.Sprintf("Tool %q failed %dx on %s; reconsider before retrying the same approach.", ts.Name, ts.Errors, topic), maxLessonTextLen),
                Topic:      topic,
                Source:     "agent",
                Confidence: "medium",
                PersonaDelta: []self.Lesson{
                    {Param: "caution", Delta: 0.02},
                },
                Created: now,
            })
        }
    }

    // Rule B: task stalled (budget exhausted without natural stop)
    if info.FinishReason == agentStalledReason {
        lessons = append(lessons, Lesson{
            Text:       truncate(fmt.Sprintf("Task stalled at iteration %d/%d on %s; scope or decompose earlier.", info.Iterations, info.MaxIterations, topic), maxLessonTextLen),
            Topic:      topic,
            Source:     "agent",
            Confidence: "medium",
            PersonaDelta: []self.Lesson{
                {Param: "caution", Delta: 0.03},
                {Param: "verbosity", Delta: -0.01},
            },
            Created: now,
        })
    }

    // Rule C: efficient successful completion
    if info.FinishReason == "stop" && info.MaxIterations > 0 {
        pct := float64(info.Iterations) / float64(info.MaxIterations)
        if pct > 0 && pct <= agentEfficientIterationPct && totalCalls(info.ToolStats) > 0 {
            lessons = append(lessons, Lesson{
                Text:       truncate(fmt.Sprintf("Efficient completion on %s: %d/%d iterations; pattern worth repeating.", topic, info.Iterations, info.MaxIterations), maxLessonTextLen),
                Topic:      topic,
                Source:     "agent",
                Confidence: "high",
                PersonaDelta: []self.Lesson{
                    {Param: "persistence", Delta: 0.01},
                },
                Created: now,
            })
        }
    }

    // Rule D: verbose output
    if info.OutputTokens >= agentVerboseOutputTokens {
        lessons = append(lessons, Lesson{
            Text:       truncate(fmt.Sprintf("Verbose output on %s: %d tokens; tighten summaries.", topic, info.OutputTokens), maxLessonTextLen),
            Topic:      topic,
            Source:     "agent",
            Confidence: "medium",
            PersonaDelta: []self.Lesson{
                {Param: "verbosity", Delta: -0.02},
            },
            Created: now,
        })
    }

    return lessons
}

func totalCalls(stats []AgentToolStat) int {
    n := 0
    for _, s := range stats {
        n += s.Calls
    }
    return n
}
```

### 3.4 New: `internal/learning/agent_extractor_test.go`

Table-driven, 각 rule trigger 조건 + 복합 케이스:

1. 빈 info → 빈 slice
2. Rule A: `ToolStats=[{Name:"bash", Calls:5, Errors:3}]` → 1 lesson, caution +0.02, Text 에 "bash" 포함
3. Rule A: errors=2 → lesson 없음 (threshold 미달)
4. Rule B: `FinishReason="budget_exceeded", Iterations=50, MaxIterations=50` → 1 lesson
5. Rule C: `FinishReason="stop", Iterations=3, MaxIterations=50, ToolStats=[...1 call...]` → 1 lesson
6. Rule C: tool 호출 0건 → lesson 없음 (trivial success 제외)
7. Rule C: Iterations=40, MaxIterations=50 → lesson 없음 (pct > 0.3)
8. Rule D: OutputTokens=60000 → 1 lesson
9. 복합: budget_exceeded + 2 tool 각각 errors 5/4 + OutputTokens 70000 → 4 lessons (A×2, B, D)
10. Topic 빈 문자열 → Text 에 "agent-task" fallback
11. Topic 200자 이상 → truncate

### 3.5 Modified: `internal/orchestrator/types.go` — WorkflowInput 확장

```go
type WorkflowInput struct {
    Message  string
    Messages []llm.Message
    Session  *agent.Session
    Tools    *tools.Registry
    Provider llm.Provider
    Config   WorkflowConfig
    OnText   func(string)
    Extra    interface{}

    Learning *LearningDeps // NEW — optional. nil disables extraction.
}

// LearningDeps bundles the store and self-state mutators used for per-run
// lesson extraction. Both fields are optional; when Learning is non-nil but a
// sub-field is nil, only the available side runs (e.g. store-only without
// persona updates).
type LearningDeps struct {
    Store     *learning.Store
    SelfState *self.SelfState
    Logger    *slog.Logger
}
```

### 3.6 Modified: `internal/orchestrator/single.go`

```go
func (w *SingleWorkflow) Run(ctx context.Context, input WorkflowInput) (*WorkflowResult, error) {
    messages := append(input.Messages, llm.NewUserMessage(input.Message))

    opts := agentOptions(input.Config)
    a := agent.New(input.Provider, input.Tools, opts...)

    result, err := a.Run(ctx, messages, input.OnText)
    if err != nil {
        return nil, fmt.Errorf("single workflow: %w", err)
    }

    w.applyLearning(input, result, input.Config.MaxIterations)

    summary := extractSummary(result.Messages)

    return &WorkflowResult{
        Messages: result.Messages,
        Summary:  summary,
        Usage:    result.Usage,
        Workflow: w.Name(),
    }, nil
}

func (w *SingleWorkflow) applyLearning(input WorkflowInput, result *agent.RunResult, maxIter int) {
    deps := input.Learning
    if deps == nil || deps.Store == nil {
        return
    }

    info := learning.AgentResultInfo{
        Topic:         firstMessageSnippet(input.Message, 80),
        FinishReason:  string(result.FinishReason),
        Iterations:    result.Iterations,
        MaxIterations: maxIter,
        OutputTokens:  result.Usage.OutputTokens,
        InputTokens:   result.Usage.InputTokens,
        ToolStats:     toAgentToolStats(result.ToolStats),
    }
    lessons := learning.ExtractAgent(info)
    if len(lessons) == 0 {
        return
    }

    log := deps.Logger
    if log == nil {
        log = slog.Default()
    }

    personaChanged := false
    for _, l := range lessons {
        if err := deps.Store.Append(l); err != nil {
            log.Warn("agent learning: append failed", "error", err)
            continue
        }
        if deps.SelfState != nil && len(l.PersonaDelta) > 0 {
            deps.SelfState.ApplyLessons(l.PersonaDelta)
            personaChanged = true
        }
    }
    if personaChanged {
        if err := deps.SelfState.Save(); err != nil {
            log.Warn("agent learning: selfState save failed", "error", err)
        }
    }
}

func firstMessageSnippet(msg string, n int) string {
    msg = strings.TrimSpace(msg)
    if msg == "" {
        return ""
    }
    runes := []rune(msg)
    if len(runes) <= n {
        return msg
    }
    return strings.TrimSpace(string(runes[:n]))
}

func toAgentToolStats(src []agent.ToolStat) []learning.AgentToolStat {
    out := make([]learning.AgentToolStat, 0, len(src))
    for _, s := range src {
        out = append(out, learning.AgentToolStat{
            Name:      s.Name,
            Calls:     s.Calls,
            Errors:    s.Errors,
            TotalTime: s.TotalTime,
        })
    }
    return out
}
```

### 3.7 Modified: `cmd/elnath/runtime.go` + `cmd/elnath/cmd_daemon.go`

`SingleWorkflow` 를 생성하는 모든 호출 지점에서 `WorkflowInput.Learning` 을 주입. runtime 은 이미 F-1 에서 `learningStore` 와 `selfState` 를 생성하므로 그대로 재사용.

수정할 후보 지점 (실제 파일 구조 확인 후 결정):

- `cmd/elnath/cmd_run.go` 의 interactive chat 루프에서 workflow dispatch 하는 지점
- `internal/daemon/runner.go` 의 task 실행 지점
- `internal/telegram/shell.go` 의 chat path (StreamConsumer 호출하는 곳)

단, benchmark / eval 경로는 `Learning=nil` 유지 (이미 `internal/eval/` 은 별도 workflow 경로라 건드리지 않아도 됨 — 확인 필요).

### 3.8 Modified: `internal/orchestrator/single_test.go`

- learning nil → panic 없음, 기존 동작 유지
- learning store 주입 + agent 가 tool 5회 에러 mock → store 에 lesson 1개 append 확인
- persona delta apply 확인 (selfState mock)

### 3.9 Modified: `internal/agent/agent_test.go` / `agent_executor_test.go`

- ToolStats 수집 검증 (tool 2번 호출 중 1번 에러 → `{Name, Calls:2, Errors:1}`)
- FinishReason 검증: maxIterations=2 로 루프 → `budget_exceeded`
- 정상 종료 → `stop`
- Iterations 필드가 루프 카운트와 일치

기존 테스트 중 `RunResult` 를 literal 비교 / len 검증 하는 게 있으면 수정 (필드 추가된 것 감안).

## 4. File Summary

### New (2)

| File | LOC (est.) | 역할 |
|------|-----------|------|
| `internal/learning/agent_extractor.go` | ~120 | 4-rule agent extractor |
| `internal/learning/agent_extractor_test.go` | ~180 | 11 case table-driven |

### Modified (6-7)

| File | 변경 |
|------|------|
| `internal/agent/agent.go` | `RunResult` 에 ToolStats/Iterations/FinishReason, 루프 집계 로직 |
| `internal/agent/executor.go` | tool 실행 시 duration/error 캡처 |
| `internal/agent/agent_test.go` / `executor_test.go` | 신규 필드 검증, 회귀 |
| `internal/orchestrator/types.go` | `WorkflowInput.Learning` + `LearningDeps` |
| `internal/orchestrator/single.go` | applyLearning hook |
| `internal/orchestrator/single_test.go` | learning mock 테스트 |
| `cmd/elnath/runtime.go` | single workflow 호출자에 Learning 주입 |
| `cmd/elnath/cmd_daemon.go` | 동일 (이미 learning store 생성 중) |
| `internal/telegram/shell.go` (선택) | chat path 에서 Learning 주입 |
| `internal/daemon/runner.go` (선택) | task path 에서 Learning 주입 |

Total new LOC 추정: ~300 (neat 로직) + ~500 (통합) + ~250 (테스트) ≈ **~1050 LOC**.

## 5. Acceptance Criteria

- [ ] `go test -race ./internal/learning/... ./internal/agent/... ./internal/orchestrator/... ./cmd/elnath/...` 통과
- [ ] `go vet ./...` 경고 없음
- [ ] `make build` 성공
- [ ] `agent.RunResult` 가 ToolStats/Iterations/FinishReason 세 필드를 항상 채움 (zero value 허용)
- [ ] 기존 research → lesson 경로 회귀 없음 (E-3 테스트 green 유지)
- [ ] SingleWorkflow 에 Learning=nil 주입 시 기존 동작과 동일
- [ ] Learning 주입 + 의도된 scenario 에서 lessons.jsonl 에 entry 추가 (각 rule 별로 1건씩 smoke)
- [ ] BenchmarkMode / eval 경로에서 lessons.jsonl 수정되지 않음
- [ ] `elnath lessons stats` 출력에 `Source: agent` 가 카운트됨 (indirect — F-1 stats 는 Source 필드 미노출. 필요 시 별도 flag 추가는 추후)

## 6. Risk

| Risk | Mitigation |
|------|-----------|
| 모든 task 마다 lesson 생성 → lessons.jsonl 폭증 | F-1 auto-rotate (KeepLast:5000, MaxBytes:1MB) 가 흡수. Rule 이 trivial case 제외 (Rule C 는 tool call 있는 경우만) |
| 중복 lesson 다량 생성 (같은 에러 반복) | 같은 Text → 같은 ID (SHA256 8자). 사용자가 `elnath lessons clear --id X` 로 정리 가능. 자동 dedup 은 다음 phase |
| Tool stats 수집이 agent hot path 에 오버헤드 | map + mutex 연산은 ms 이하. tool 실행 자체가 수 초~분 단위라 무시 가능 |
| Persona drift 가속 | Delta 값 agent rule 기준 0.01~0.03. clamp 가 이미 [0, 1]. 수백 회 누적해야 극단값 |
| 병렬 tool 실행 경로에서 ToolStats race | mutex 로 보호. 테스트에 race 포함 |
| Team/Ralph workflow 가 SingleWorkflow 를 내부 호출할 가능성 | 확인 필요. 내부 호출 시 중복 추출 위험. Workflow 이름이 "single" 이 아닌 경우 skip 하는 가드 추가 검토 |
| RunResult 추가 필드가 기존 테스트 깨뜨림 | 새 필드는 값을 채우기만. 기존 literal 비교가 있다면 수정. |
| Topic proxy 가 민감정보 유출 | lessons.jsonl 은 local 파일 (0o600). 외부 전송 없음. 사용자가 `clear --topic` 로 정리 가능 |

## 7. Future Work

- **Phase F-3 (multi-workflow):** TeamWorkflow / RalphWorkflow / AutopilotWorkflow 에도 extractor 연결. Team 은 sub-agent 별 stats aggregation 설계 필요.
- **LLM-based extraction:** 현재 rule 은 4개. task outcome summarization 을 LLM 에 맡기면 훨씬 풍부한 lesson. 비용 약 $0.01/run.
- **Lesson dedup:** 같은 ID prefix (같은 Text) 는 Append 시 skip 옵션.
- **User feedback signal:** 사용자가 ":thumbsdown:" 같은 signal 을 남기면 부정 lesson 생성.
- **Topic classification:** `firstMessageSnippet` 대신 LLM / embedding 으로 cluster. 현재 80자 snippet 은 중복이 많음.
- **Source-aware stats:** `elnath lessons stats --by-source` → research vs agent 분포.

---

## Appendix A. Rule 매핑표

| Rule | Trigger | Persona Delta | Confidence |
|------|---------|---------------|-----------|
| A: Tool failure loop | `ts.Errors >= 3` | caution +0.02 | medium |
| B: Budget exceeded | `FinishReason == "budget_exceeded"` | caution +0.03, verbosity -0.01 | medium |
| C: Efficient stop | `FinishReason == "stop" && iter/max <= 0.3 && toolCalls > 0` | persistence +0.01 | high |
| D: Verbose output | `OutputTokens >= 50,000` | verbosity -0.02 | medium |

모든 rule 은 서로 독립 (AND 아님). 한 run 에서 여러 lesson 생성 가능.

## Appendix B. 예시 시나리오

**시나리오 1:** 사용자가 `"refactor auth middleware"` 요청. agent 가 10 iteration 만에 완료 (maxIter=50). bash tool 3번 호출 (에러 0).
→ Rule C trigger. Lesson: `"Efficient completion on refactor auth middleware: 10/50 iterations; pattern worth repeating."`

**시나리오 2:** `"fix flaky test"` 요청. agent 가 go_test tool 을 5번 실행했는데 4번 실패. maxIter 도달.
→ Rule A (go_test errors=4) + Rule B (budget_exceeded) = 2 lessons.

**시나리오 3:** `"write a long report"` 요청. 정상 완료지만 output 80k tokens.
→ Rule D trigger. Lesson: `"Verbose output on write a long report: 80000 tokens; tighten summaries."`
