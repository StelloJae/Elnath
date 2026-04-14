# Phase F-3 Multi-workflow Learning Integration

**Predecessor:** Phase F-2.5 (Lesson Redaction) DONE
**Status:** SPEC (locked, ready for execution)
**Decision owner:** Claude (delegated by stello)
**Estimated scope:** ~600–800 LOC across 2 sub-phases
**Branch:** `feat/telegram-redesign`

---

## 0. Goal

F-2 에서 `SingleWorkflow` 한정으로 걸었던 learning hook 을 `TeamWorkflow`, `RalphWorkflow`, `AutopilotWorkflow` 까지 확장한다. 각 workflow 가 자기 특성에 맞는 1 개 lesson 을 생성하도록 설계.

> **Why this matters**: Elnath 의 자율 AI 비서 핵심은 실행 → 반성 → persona 조정의 학습 loop. F-2 는 single-call 에서만 돌고, 실제 운영 트래픽의 상당 비중을 차지하는 team/ralph/autopilot 경로는 학습 데이터를 전혀 남기지 않는 구조적 공백이었다.

---

## 1. Decisions (locked)

| ID | Question | Answer | Core rationale |
|----|----------|--------|----------------|
| Q1 | Team lesson 단위 | **B: team-level 집계 (1 lesson)** | Team 은 `runOne` 에서 `agent.New` 를 직접 호출해 Single 을 우회 (`team.go:319-348`). sub-agent ToolStats 를 merge 해 1 회 extract. 증가율 예측 가능. |
| Q2 | Ralph 반복 전략 | **C: 집계 + Rule E (retry instability)** | Ralph 의 본질인 "재시도" 자체가 signal. Single hook 이 매 iter 마다 걸리므로 **반드시 suppress 후 wrapper level 1 회 extract**. |
| Q3 | Autopilot 단계 전략 | **B: pipeline-final 1 lesson** | Team 과 design parity. 각 stage 에서 Single hook suppress, verify stage 종료 후 1 회 extract. defer 로 중간 실패 path 도 커버. |
| Q4 | Source 네이밍 | **A: `"agent:<workflow>"` flat string** | Lesson 스키마 불변 → JSONL 하위호환. 기존 `"agent"` 값은 legacy fallback 으로 필터에서 양쪽 수용. |

### 추가 결정 (spec 작성 중 확정)
- **중복 가드 방식**: wrapper 가 inner `WorkflowInput.Learning = nil` 로 override. context 메타 기반 skip 은 명시성 떨어져 기각.
- **기존 lesson migration**: 하지 않는다. 파일은 append-only, filter 레벨 하위호환.
- **LOC 초과 시 분할**: Phase F-3.1 (learning extractor) 와 Phase F-3.2 (orchestrator wiring) 두 커밋으로 나눈다. F-3.1 verified 후 F-3.2 진입.

---

## 2. Phase F-3.1 — Learning Extractor Changes

**Files touched**: `internal/learning/agent_extractor.go`, `internal/learning/agent_extractor_test.go` (신규 또는 확장).

### 2.1 `AgentResultInfo` 확장

```go
type AgentResultInfo struct {
    // 기존 필드 보존
    Topic         string
    FinishReason  string
    Iterations    int
    MaxIterations int
    OutputTokens  int
    InputTokens   int
    TotalCost     float64
    ToolStats     []AgentToolStat
    // NEW
    RetryCount int    // ralph attempt-1. single/team/autopilot 은 항상 0
    Workflow   string // "single"|"team"|"ralph"|"autopilot"|"" (legacy)
}
```

### 2.2 Rule E — Ralph Retry Instability

```go
const agentRalphRetryThreshold = 3

if info.RetryCount >= agentRalphRetryThreshold {
    lessons = append(lessons, Lesson{
        Text:       truncate(fmt.Sprintf("Task retried %d times on %s; review decomposability.", info.RetryCount, topic), maxLessonTextLen),
        Topic:      topic,
        Source:     sourceFor(info.Workflow),
        Confidence: "medium",
        PersonaDelta: []self.Lesson{{Param: "caution", Delta: 0.02}},
        Created:    now,
    })
}
```

근거: 5-attempt cap 의 60% 지점. ralph 가 3회 이상 재시도한다는 건 task 가 단일 agent 로 풀리는 범주가 아니란 뜻 → decomposability 재검토 signal.

### 2.3 Source 헬퍼 (모든 4 rule + Rule E 공통)

```go
func sourceFor(workflow string) string {
    if workflow == "" {
        return "agent" // backward compat
    }
    return "agent:" + workflow
}
```

기존 Rule A/B/C/D 의 `Source: "agent"` hardcode 를 전부 `Source: sourceFor(info.Workflow)` 로 치환.

### 2.4 `MergeAgentToolStats` 헬퍼

```go
// MergeAgentToolStats sums Calls/Errors/TotalTime per tool Name across input slices.
// Entries with Calls == 0 are dropped from the merged result.
func MergeAgentToolStats(slices ...[]AgentToolStat) []AgentToolStat
```

Team/Ralph/Autopilot wrapper 가 내부 sub-run ToolStats 를 합칠 때 사용.

### 2.5 Tests (F-3.1)

| Test | 케이스 |
|------|-------|
| `TestExtractAgent_RalphRetry` | RetryCount ∈ {0, 2, 3, 5} → Rule E 생성 여부 및 text 내용 |
| `TestExtractAgent_SourceSuffix` | Workflow ∈ {"", "single", "team", "ralph", "autopilot"} 에 대해 Rule A/B/C/D/E 각각 Source 값 검증 |
| `TestMergeAgentToolStats` | 빈 입력, 단일 슬라이스, 겹치는 Name (sum), Calls=0 필터 |

### 2.6 Verification

```bash
cd /Users/stello/elnath
go vet ./internal/learning/...
go test -race ./internal/learning/...
```

모두 pass 시 phase F-3.1 완료.

---

## 3. Phase F-3.2 — Orchestrator Wiring

**Files touched**:
- `internal/orchestrator/learning.go` (신규)
- `internal/orchestrator/types.go` (`WorkflowResult.ToolStats` 필드 추가)
- `internal/orchestrator/single.go` (ToolStats 전파)
- `internal/orchestrator/team.go` (wrapper-level learning)
- `internal/orchestrator/ralph.go` (Single hook suppress + wrapper learning)
- `internal/orchestrator/autopilot.go` (Single hook suppress + defer wrapper learning)
- `cmd/elnath/runtime.go` (line ~387 gate 확장)
- Tests 전반

### 3.1 공통 헬퍼 `internal/orchestrator/learning.go`

`SingleWorkflow.applyLearning`, `firstMessageSnippet`, `toAgentToolStats` 를 패키지 레벨 헬퍼로 이동.

```go
func applyAgentLearning(deps *LearningDeps, info learning.AgentResultInfo)
func firstMessageSnippet(msg string, n int) string
func toAgentToolStats(src []agent.ToolStat) []learning.AgentToolStat
func aggregateFinishReason(reasons []string) string
```

`aggregateFinishReason` 우선순위: `budget_exceeded > error > ack_loop > stop`. Team 집계용.

### 3.2 `WorkflowResult.ToolStats`

```go
type WorkflowResult struct {
    Messages  []llm.Message
    Summary   string
    Usage     llm.UsageStats
    ToolStats []agent.ToolStat // NEW
    Workflow  string
}
```

Ralph/Autopilot 이 내부 Single 호출의 ToolStats 를 이 필드로 회수.

### 3.3 SingleWorkflow (변경 최소화)

- `applyLearning` 제거 → `applyAgentLearning(deps, info)` 직접 호출.
- `info.Workflow = "single"` 세팅.
- 결과에 `ToolStats: result.ToolStats` 전파.

### 3.4 TeamWorkflow

Team 은 Single 을 우회하므로 duplicate guard 불필요.

```go
// 기존 Run 끝부분 (synthesise 이후)
if input.Learning != nil {
    var toolStatSlices [][]learning.AgentToolStat
    var finishReasons []string
    totalIter := 0
    for _, r := range results {
        toolStatSlices = append(toolStatSlices, toAgentToolStats(r.result.ToolStats))
        finishReasons = append(finishReasons, string(r.result.FinishReason))
        totalIter += r.result.Iterations
    }
    info := learning.AgentResultInfo{
        Topic:         firstMessageSnippet(input.Message, 80),
        FinishReason:  aggregateFinishReason(finishReasons),
        Iterations:    totalIter,
        MaxIterations: input.Config.MaxIterations * len(results),
        OutputTokens:  totalUsage.OutputTokens,
        InputTokens:   totalUsage.InputTokens,
        ToolStats:     learning.MergeAgentToolStats(toolStatSlices...),
        Workflow:      "team",
    }
    applyAgentLearning(input.Learning, info)
}
```

### 3.5 RalphWorkflow (중복 가드 필수)

핵심 변경:
1. 루프 진입 전 `current.Learning = nil` 로 override.
2. 각 iter 의 `result.ToolStats` 를 accumulator 에 쌓는다. `result.FinishReason` 및 `Iterations` 도 보존.
3. 루프 종료 후 (verified OR cap 초과) 1 회 `applyAgentLearning` 호출.

```go
current := input
current.Learning = nil // suppress inner Single hook

var accToolStats [][]learning.AgentToolStat
var lastFinishReason string
totalIter := 0
attemptsRun := 0 // 실제로 돌린 iter 수. for-loop post-increment 문제 회피.
verified := false

for a := 1; a <= maxAttempts; a++ {
    attemptsRun = a
    result, err := single.Run(ctx, current)
    // ... 기존 로직
    accToolStats = append(accToolStats, toAgentToolStats(result.ToolStats))
    totalIter += result.Iterations
    lastFinishReason = result.FinishReason
    // ... verify → ok 이면 verified=true; break
}

// 루프 종료 직후
if input.Learning != nil {
    finishReason := lastFinishReason
    if !verified {
        finishReason = "ralph_cap_exceeded"
    }
    info := learning.AgentResultInfo{
        Topic:         firstMessageSnippet(input.Message, 80),
        FinishReason:  finishReason,
        Iterations:    totalIter,
        MaxIterations: input.Config.MaxIterations * attemptsRun,
        OutputTokens:  totalUsage.OutputTokens,
        InputTokens:   totalUsage.InputTokens,
        ToolStats:     learning.MergeAgentToolStats(accToolStats...),
        RetryCount:    attemptsRun - 1,
        Workflow:      "ralph",
    }
    applyAgentLearning(input.Learning, info)
}
```

**RetryCount 의미**:
- attempt 1 verified → attemptsRun=1, RetryCount=0
- attempt 4 verified → attemptsRun=4, RetryCount=3 (Rule E 발화 threshold)
- cap 초과 (5 전부 실패) → attemptsRun=5, RetryCount=4 (Rule E 발화)

### 3.6 AutopilotWorkflow (중복 가드 + defer)

각 stage 에서 `stageInput.Learning = nil`. 중간 실패 path 에서도 extract 되도록 defer 배치.

```go
var accToolStats [][]learning.AgentToolStat
var lastFinishReason string
totalIter := 0
lessonScheduled := false

extract := func() {
    if lessonScheduled || input.Learning == nil {
        return
    }
    lessonScheduled = true
    info := learning.AgentResultInfo{
        Topic:         firstMessageSnippet(input.Message, 80),
        FinishReason:  lastFinishReason,
        Iterations:    totalIter,
        MaxIterations: input.Config.MaxIterations * len(autopilotStages),
        OutputTokens:  totalUsage.OutputTokens,
        InputTokens:   totalUsage.InputTokens,
        ToolStats:     learning.MergeAgentToolStats(accToolStats...),
        Workflow:      "autopilot",
    }
    applyAgentLearning(input.Learning, info)
}
defer extract()

for _, s := range autopilotStages {
    stageInput := WorkflowInput{..., Learning: nil}
    result, err := single.Run(ctx, stageInput)
    if err != nil { return ..., err } // defer 가 발동
    accToolStats = append(accToolStats, toAgentToolStats(result.ToolStats))
    totalIter += result.Iterations
    lastFinishReason = string(result.FinishReason)
}
```

### 3.7 Single.Run 이 agent.RunResult 의 Iterations 를 WorkflowResult 로 전파

Ralph/Autopilot 이 iter 합산을 위해 필요. `WorkflowResult` 에 `Iterations int` 추가할지 vs `ToolStats` 만으로 추론할지.

**결정**: `WorkflowResult` 에 `Iterations int`, `FinishReason string` 필드 추가. ToolStats 와 같은 수준의 학습 관련 메타라 일관성 있음. Ralph/Autopilot 이 이 필드를 읽어 누적.

```go
type WorkflowResult struct {
    Messages     []llm.Message
    Summary      string
    Usage        llm.UsageStats
    ToolStats    []agent.ToolStat  // NEW
    Iterations   int               // NEW
    FinishReason string            // NEW
    Workflow     string
}
```

### 3.8 `cmd/elnath/runtime.go` gate 확장

**변경 전** (`runtime.go:387-389`):
```go
if wf.Name() == "single" {
    input.Learning = rt.learningDeps()
}
```

**변경 후**:
```go
switch wf.Name() {
case "single", "team", "ralph", "autopilot":
    input.Learning = rt.learningDeps()
}
```

Research path 는 `ResearchDeps.LearningStore` 로 이미 학습 경로 보유 (E-3). 건드리지 않음.

### 3.9 Tests (F-3.2)

| File | Test | 검증 |
|------|------|------|
| `team_test.go` | `TestTeamWorkflow_Learning` | 3 subtask 중 2 개가 tool error ≥ 3 → merged ToolStats 로 Rule A lesson 생성. Source = `"agent:team"`. |
| `ralph_test.go` | `TestRalphWorkflow_LearningVerifiedFirstAttempt` | 1-attempt verified → Rule E 없음, Source = `"agent:ralph"`, RetryCount=0. |
| `ralph_test.go` | `TestRalphWorkflow_LearningRetryTriggersRuleE` | 3-attempt verified → Rule E 존재. |
| `ralph_test.go` | `TestRalphWorkflow_LearningCapExceededFinishReason` | 5-cap 초과 → Rule E + FinishReason = "ralph_cap_exceeded". |
| `ralph_test.go` | `TestRalphWorkflow_NoPerIterLearning` | mock store 의 `Append` 호출 ≤ 1. inner Single hook suppress 검증. |
| `autopilot_test.go` | `TestAutopilotWorkflow_LearningAllStagesPass` | 4 stage 전부 성공 → Source = `"agent:autopilot"`, Append 1 회. |
| `autopilot_test.go` | `TestAutopilotWorkflow_LearningMidStageFailTriggersLesson` | code stage 실패 → defer 로 lesson 생성됨. |
| `single_test.go` | 기존 assertion 업데이트 | Source 기대값 `"agent"` → `"agent:single"` |
| `runtime_test.go` | 기존 assertion 검토 | Learning 이 모든 workflow 에 주입되는지 확인 (필요 시 테스트 추가) |

### 3.10 Verification gates

```bash
cd /Users/stello/elnath
go vet ./...
go test -race ./internal/learning/... ./internal/orchestrator/... ./cmd/elnath/...
make build
```

전부 pass 시 phase F-3.2 완료.

---

## 4. Scope boundaries

**In scope (F-3)**:
- AgentResultInfo.{RetryCount, Workflow} 필드
- Rule E (ralph retry instability)
- sourceFor 헬퍼 + 4 rule 통합
- MergeAgentToolStats
- Team/Ralph/Autopilot wrapper learning
- Single hook suppression guards (ralph/autopilot)
- WorkflowResult 확장 (ToolStats/Iterations/FinishReason)
- runtime.go workflow gate 확장

**Out of scope (F-3 이후)**:
- LLM 기반 lesson extraction (rule 외 신호)
- `lessons stats --by-source` CLI flag (F-4 후보)
- Research path lesson 재설계 (E-3 에서 담당)
- Coordination/conflict rule (Rule F, 투기적)
- lessons.jsonl schema migration (필요 없음, filter 로 하위호환)
- Persona param 신규 (caution/verbosity/persistence 3 개 유지)

---

## 5. Risks & Mitigations

| ID | Risk | Mitigation |
|----|------|------------|
| R1 | Ralph/Autopilot 에서 Single hook suppress 실패 → 매 iter/stage 마다 lesson 저장 | `TestRalphWorkflow_NoPerIterLearning` + autopilot 대응 테스트로 Append 횟수 단언 |
| R2 | Team FinishReason 집계가 signal 손실 | `aggregateFinishReason` 우선순위 godoc 에 명시 + 테스트 |
| R3 | Single Source 변경(`"agent"` → `"agent:single"`)으로 기존 테스트 파괴 | F-3.1 작업 범위에 Single 테스트 assertion 업데이트 포함 |
| R4 | 미커밋 잔여분과 `runtime.go` merge 충돌 | F-3.2 prompt 에 "preserve uncommitted changes" 명시, OpenCode 가 3-way merge |
| R5 | WorkflowResult 필드 확장이 기존 caller 깨뜨림 | Go zero-value 기본값 안전. 모든 caller 가 field 별 접근이라 회귀 위험 낮음. `go vet + race test` 로 검증 |

---

## 6. Sequencing & Checkpoints

1. **미커밋 정리** (선택적; OpenCode 지시로 우회 가능) — 상세는 `§7`.
2. **F-3.1 execute** (OpenCode prompt 1) → verification (`go test ./internal/learning/...`) → human spot check → commit.
3. **F-3.2 execute** (OpenCode prompt 2) → verification (full test suite + build) → code-reviewer agent → commit.
4. **code-reviewer pass** (opus) on both commits together.
5. Memory update: `project_elnath_next_action.md` F-3 DONE 기록.

---

## 7. 미커밋 잔여분 처리 판단

`git diff --stat` 결과 22 modified + 다수 untracked, 총 1958 insertions. F-3 과의 충돌 지점은 `cmd/elnath/runtime.go` (§3.8) 와 `cmd/elnath/cmd_daemon.go` (Learning 주입 지점) 두 파일.

**결정**: 미커밋 정리를 **별도 세션으로 미룸**. 대신 OpenCode prompt 에 "working tree 의 기존 미커밋 변경을 preserve 하고 F-3 변경을 적층" 명시 → merge conflict 자동 해결.

근거:
- 1958 line diff 를 이 세션 맥락에서 다 읽어 범주별 커밋을 구성하려면 토큰 과다 소모. F-3 자체(~700 LOC) 가 우선순위 더 높음.
- 미커밋 변경은 Telegram 재설계/Gate retry/신규 패키지(skill/scheduler/audit/prompt nodes) 계열로, 전부 F-3 와 다른 lineage. 공존 가능.
- F-3 완료 후 별도 세션에서 "미커밋 정리" 단일 업무로 다룰 것.

---

## 8. Commit message templates

### F-3.1 commit

```
feat: phase F-3.1 learning extractor multi-workflow prep

Extend AgentResultInfo with RetryCount/Workflow fields. Add Rule E for
ralph retry instability. Introduce sourceFor helper so all rules can
emit workflow-scoped Source values ("agent:single"/"agent:team"/etc.)
while preserving legacy "agent" for backward compat. Add
MergeAgentToolStats helper for Team/Ralph/Autopilot aggregation.
```

### F-3.2 commit

```
feat: phase F-3.2 team/ralph/autopilot learning integration

Wire Team/Ralph/Autopilot workflows to the learning extractor. Team
aggregates sub-agent ToolStats via MergeAgentToolStats. Ralph and
Autopilot suppress the inner SingleWorkflow learning hook and extract
once at wrapper level — ralph after its retry loop, autopilot via
defer so mid-stage failures still produce a lesson. Expand runtime
gate to inject Learning for all four workflows.
```

---

## 9. OpenCode prompts

See companion files:
- `docs/specs/PHASE-F3.1-OPENCODE-PROMPT.md` — learning extractor (F-3.1)
- `docs/specs/PHASE-F3.2-OPENCODE-PROMPT.md` — orchestrator wiring (F-3.2)

Each prompt is self-contained: context, task breakdown, constraints, tests, verification gates, scope limits.
