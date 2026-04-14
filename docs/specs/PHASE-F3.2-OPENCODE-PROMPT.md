# Phase F-3.2 — OpenCode Prompt (Orchestrator Wiring)

## Context

Elnath 는 Go 로 만든 자율 AI 비서 daemon (`/Users/stello/elnath/`, 브랜치 `feat/telegram-redesign`). Phase F-3.1 에서 `internal/learning/agent_extractor.go` 에 다음이 추가됐다:
- `AgentResultInfo.RetryCount`, `AgentResultInfo.Workflow` 필드
- Rule E (ralph retry instability, threshold=3)
- `sourceFor(workflow)` 헬퍼 → 4 기존 rule 전부 이걸 경유하여 `"agent:<workflow>"` 또는 legacy `"agent"` Source 생성
- `MergeAgentToolStats(slices...)` 헬퍼

이 prompt 는 Team/Ralph/Autopilot workflow 가 실제로 저 extractor 를 호출하게 만든다. **F-3 spec 의 core 구현**.

상세 spec: `docs/specs/PHASE-F3-MULTI-WORKFLOW-LEARNING.md` §3.

## 중대 설계 주의 (틀리면 silent bug)

1. **Ralph 와 Autopilot 은 내부에서 SingleWorkflow 를 호출한다** (`ralph.go:54`, `autopilot.go:120`). 아무 가드 없이 모든 workflow 에 Learning 을 주입하면 ralph 5-attempt 마다 5 lesson, autopilot 4-stage 마다 4 lesson 이 저장된다. **Wrapper 에서 inner `WorkflowInput.Learning = nil` 로 override 필수**.
2. **Team 은 SingleWorkflow 를 우회한다** (`team.go:runOne` 이 `agent.New` 직접 호출). 이 가드 불필요. Team 은 sub-agent ToolStats 만 merge 하면 됨.
3. **Autopilot 중간 stage 실패 path 에서도 lesson 이 나와야 한다**. 단순 post-loop extract 로는 놓침. **defer 로 배치**.

## Scope (이 phase 에서 할 일)

### 신규 파일

- `internal/orchestrator/learning.go` — 공통 헬퍼
- `internal/orchestrator/team_test.go` 의 경우 신규 test 만 추가 (파일이 이미 있으면 확장)
- `internal/orchestrator/ralph_test.go` 확장
- `internal/orchestrator/autopilot_test.go` 확장 (파일 없으면 신규)

### 수정 파일

- `internal/orchestrator/types.go` — WorkflowResult 확장
- `internal/orchestrator/single.go` — ToolStats/Iterations/FinishReason 전파, applyLearning 헬퍼 호출로 전환
- `internal/orchestrator/team.go` — wrapper-level learning 추가
- `internal/orchestrator/ralph.go` — Single hook suppression + wrapper learning
- `internal/orchestrator/autopilot.go` — Single hook suppression + defer wrapper learning
- `cmd/elnath/runtime.go:387-389` — workflow gate 확장
- 기존 single_test.go / runtime_test.go 의 Source 관련 assertion 업데이트

## Task

### 1. `internal/orchestrator/learning.go` 신규

`SingleWorkflow.applyLearning` 와 helper `firstMessageSnippet`, `toAgentToolStats` 를 여기로 옮긴다.

```go
package orchestrator

import (
    "log/slog"
    "strings"

    "github.com/stello/elnath/internal/agent"
    "github.com/stello/elnath/internal/learning"
)

func applyAgentLearning(deps *LearningDeps, info learning.AgentResultInfo) {
    if deps == nil || deps.Store == nil {
        return
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
    if personaChanged && deps.SelfState != nil {
        if err := deps.SelfState.Save(); err != nil {
            log.Warn("agent learning: selfState save failed", "error", err)
        }
    }
}

func firstMessageSnippet(msg string, n int) string { /* single.go 에서 이동 */ }

func toAgentToolStats(src []agent.ToolStat) []learning.AgentToolStat { /* single.go 에서 이동 */ }

// aggregateFinishReason picks the most informative reason across sub-runs.
// Precedence: budget_exceeded > error > ack_loop > stop.
func aggregateFinishReason(reasons []string) string {
    priority := map[string]int{
        "budget_exceeded": 4,
        "error":           3,
        "ack_loop":        2,
        "stop":            1,
        "":                0,
    }
    best := ""
    for _, r := range reasons {
        if priority[r] > priority[best] {
            best = r
        }
    }
    return best
}
```

`single.go` 에서는 해당 정의들 삭제하고, `applyLearning` 호출부를 `applyAgentLearning(deps, info)` 로 바꾼다.

### 2. `WorkflowResult` 확장 (`types.go`)

```go
type WorkflowResult struct {
    Messages     []llm.Message
    Summary      string
    Usage        llm.UsageStats
    ToolStats    []agent.ToolStat // NEW
    Iterations   int              // NEW
    FinishReason string           // NEW
    Workflow     string
}
```

### 3. `SingleWorkflow` 수정

```go
result, err := a.Run(ctx, messages, input.OnText)
if err != nil { return nil, fmt.Errorf("single workflow: %w", err) }

if input.Learning != nil {
    info := learning.AgentResultInfo{
        Topic:         firstMessageSnippet(input.Message, 80),
        FinishReason:  string(result.FinishReason),
        Iterations:    result.Iterations,
        MaxIterations: input.Config.MaxIterations,
        OutputTokens:  result.Usage.OutputTokens,
        InputTokens:   result.Usage.InputTokens,
        ToolStats:     toAgentToolStats(result.ToolStats),
        Workflow:      "single",
    }
    applyAgentLearning(input.Learning, info)
}

return &WorkflowResult{
    Messages:     result.Messages,
    Summary:      extractSummary(result.Messages),
    Usage:        result.Usage,
    ToolStats:    result.ToolStats,
    Iterations:   result.Iterations,
    FinishReason: string(result.FinishReason),
    Workflow:     w.Name(),
}, nil
```

기존 `applyLearning` 메서드 삭제.

### 4. `TeamWorkflow` 수정

`runSubtasks` 의 `subtaskResult` 는 이미 `*agent.RunResult` 를 보유. 기존 `Run` 메서드의 synthesise 이후에 learning 블록 추가:

```go
// 기존 synth 호출 이후
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

`WorkflowResult` 반환 시 ToolStats/Iterations/FinishReason 은 집계 값 채우기 (extractor 에 줬던 것과 동일 값).

### 5. `RalphWorkflow` 수정

```go
func (w *RalphWorkflow) Run(ctx context.Context, input WorkflowInput) (*WorkflowResult, error) {
    single := NewSingleWorkflow()
    totalUsage := llm.UsageStats{}
    maxAttempts := w.MaxAttempts
    if maxAttempts <= 0 { maxAttempts = defaultMaxAttempts }

    current := input
    current.Learning = nil // suppress inner Single hook

    var accToolStatSlices [][]learning.AgentToolStat
    var lastFinishReason string
    totalIter := 0
    attemptsRun := 0 // 실제 돌린 iter 수. for-loop post-increment 회피.
    verified := false
    var finalResult *WorkflowResult

    for a := 1; a <= maxAttempts; a++ {
        attemptsRun = a
        result, err := single.Run(ctx, current)
        if err != nil { return nil, fmt.Errorf("ralph workflow attempt %d: %w", a, err) }

        totalUsage.InputTokens  += result.Usage.InputTokens
        totalUsage.OutputTokens += result.Usage.OutputTokens
        totalUsage.CacheRead    += result.Usage.CacheRead
        totalUsage.CacheWrite   += result.Usage.CacheWrite
        accToolStatSlices = append(accToolStatSlices, toAgentToolStats(result.ToolStats))
        totalIter += result.Iterations
        lastFinishReason = result.FinishReason
        finalResult = result

        ok, feedback, verifyUsage, err := w.verify(ctx, input, result)
        if err != nil { return nil, fmt.Errorf("ralph verify attempt %d: %w", a, err) }
        totalUsage.InputTokens  += verifyUsage.InputTokens
        totalUsage.OutputTokens += verifyUsage.OutputTokens
        totalUsage.CacheRead    += verifyUsage.CacheRead
        totalUsage.CacheWrite   += verifyUsage.CacheWrite

        if ok {
            verified = true
            finalResult.Usage = totalUsage
            finalResult.Workflow = w.Name()
            break
        }

        // build retry input
        feedbackMsg := buildRecoveryPrompt(input.Message, feedback)
        current = WorkflowInput{
            Message:  feedbackMsg,
            Messages: sanitizeRetryMessages(result.Messages),
            Session:  input.Session,
            Tools:    input.Tools,
            Provider: input.Provider,
            Config:   input.Config,
            Learning: nil, // still suppress
        }
    }

    // wrapper-level learning (verified 또는 cap 초과 모두 발화)
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
            ToolStats:     learning.MergeAgentToolStats(accToolStatSlices...),
            RetryCount:    attemptsRun - 1,
            Workflow:      "ralph",
        }
        applyAgentLearning(input.Learning, info)
    }

    if !verified {
        return nil, fmt.Errorf("ralph workflow: task not verified after %d attempts", maxAttempts)
    }
    return finalResult, nil
}
```

**주의**:
- 루프 변수는 내부 `a` 로 쓰고 외부 참조는 `attemptsRun` 사용. Go for-loop post-increment (`a = maxAttempts+1` on normal termination) 회피 목적.
- `break` 로 나오면 `attemptsRun == 성공한 attempt 번호`. cap 초과로 루프 자연 종료 시 `attemptsRun == maxAttempts`. 둘 다 `attemptsRun - 1` 이 정확한 RetryCount.
- verified=false && maxAttempts 도달 시 `finalResult` 는 마지막 iter 결과지만 반환 경로는 error. learning 호출은 그 전에 이미 완료.

### 6. `AutopilotWorkflow` 수정 (defer 패턴)

```go
func (w *AutopilotWorkflow) Run(ctx context.Context, input WorkflowInput) (*WorkflowResult, error) {
    single := NewSingleWorkflow()
    totalUsage := llm.UsageStats{}
    messages := append(input.Messages, llm.NewUserMessage(input.Message))

    var accToolStatSlices [][]learning.AgentToolStat
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
            ToolStats:     learning.MergeAgentToolStats(accToolStatSlices...),
            Workflow:      "autopilot",
        }
        applyAgentLearning(input.Learning, info)
    }
    defer extract()

    for _, s := range autopilotStages {
        stageInput := WorkflowInput{
            Message:  s.instruction(input.Message),
            Messages: messages,
            Session:  input.Session,
            Tools:    input.Tools,
            Provider: input.Provider,
            Config:   input.Config,
            OnText:   input.OnText,
            Learning: nil, // suppress inner Single hook
        }
        result, err := single.Run(ctx, stageInput)
        if err != nil {
            errSummary := fmt.Sprintf("Autopilot stage %q failed: %v", s.name, err)
            messages = append(messages, llm.NewAssistantMessage(errSummary))
            // defer 가 lesson 저장
            return &WorkflowResult{
                Messages: messages,
                Summary:  errSummary,
                Usage:    totalUsage,
                Workflow: w.Name(),
            }, fmt.Errorf("autopilot stage %q failed: %w", s.name, err)
        }

        totalUsage.InputTokens  += result.Usage.InputTokens
        totalUsage.OutputTokens += result.Usage.OutputTokens
        totalUsage.CacheRead    += result.Usage.CacheRead
        totalUsage.CacheWrite   += result.Usage.CacheWrite
        accToolStatSlices = append(accToolStatSlices, toAgentToolStats(result.ToolStats))
        totalIter += result.Iterations
        lastFinishReason = result.FinishReason
        messages = result.Messages
    }

    // synth summary (기존 로직 유지)
    summary, summaryUsage := synthesizeAssistantSummary(ctx, input.Provider, input.Message, messages, input.OnText)
    totalUsage.InputTokens  += summaryUsage.InputTokens
    totalUsage.OutputTokens += summaryUsage.OutputTokens

    return &WorkflowResult{
        Messages:  messages,
        Summary:   summary,
        Usage:     totalUsage,
        Workflow:  w.Name(),
    }, nil
}
```

### 7. `cmd/elnath/runtime.go` gate 확장

**Before** (line ~387):
```go
if wf.Name() == "single" {
    input.Learning = rt.learningDeps()
}
```

**After**:
```go
switch wf.Name() {
case "single", "team", "ralph", "autopilot":
    input.Learning = rt.learningDeps()
}
```

Research workflow 는 기존 ResearchDeps.LearningStore 경로 유지 (건드리지 말 것).

## Tests (반드시 작성)

### `team_test.go`

`TestTeamWorkflow_Learning`:
- 3 subtask mock. 2 개가 tool error ≥ 3 (예: bash 가 3 error).
- mock `learning.Store` 주입. Append 호출을 capture.
- 실행 후 Append 가 정확히 1 회 호출됐고, Source="agent:team", Rule A lesson 이 포함됐는지 검증.

### `ralph_test.go`

- `TestRalphWorkflow_LearningVerifiedFirstAttempt`: 1-attempt 만에 PASS → Rule E 없음, Source="agent:ralph", RetryCount=0 확인.
- `TestRalphWorkflow_LearningRetryTriggersRuleE`: 3-attempt 만에 PASS → Rule E lesson 존재, text 에 "retried 2 times" 포함.  
  (주의: attempt 1 실패, attempt 2 실패, attempt 3 PASS → RetryCount = 3-1 = 2. 임계값 3 미만. 이 케이스는 Rule E 안 뜨는 게 맞다. 재검토: **Rule E 는 RetryCount ≥ 3, 즉 4회 시도부터 발화**. 테스트 표도 이에 맞춰 작성한다.)

**정정된 ralph 테스트**:

| Test | attempt 성공 시점 | RetryCount | Rule E 발화? |
|------|----------------|-----------|--------------|
| `TestRalphWorkflow_LearningVerifiedFirstAttempt` | 1 | 0 | No |
| `TestRalphWorkflow_LearningBelowThreshold` | 3 | 2 | No |
| `TestRalphWorkflow_LearningRetryTriggersRuleE` | 4 | 3 | Yes |
| `TestRalphWorkflow_LearningCapExceededFinishReason` | never (5-cap) | 4 | Yes + FinishReason="ralph_cap_exceeded" |
| `TestRalphWorkflow_NoPerIterLearning` | any | - | mock Store.Append 호출 ≤ 1 확인 |

### `autopilot_test.go`

- `TestAutopilotWorkflow_LearningAllStagesPass`: 4 stage 전부 성공 → Source="agent:autopilot", Append 1 회.
- `TestAutopilotWorkflow_LearningMidStageFailTriggersLesson`: code stage 실패 → defer 로 lesson 생성됨 (mock Append 1 회 확인).
- `TestAutopilotWorkflow_NoPerStageLearning`: mock Store.Append 호출 ≤ 1 확인.

### `single_test.go` 업데이트

기존 Source assertion 이 `"agent"` 를 기대하면 `"agent:single"` 로 업데이트. 기존 테스트 의도 보존.

### `runtime_test.go` 검토

기존 test 가 "single 이 아닌 workflow 에는 Learning 이 주입되지 않음" 을 assert 하는 게 있다면 gate 확장에 맞춰 업데이트. 필요 시 `TestRuntime_LearningInjectedForAllAgentWorkflows` 추가.

## Verification gates (모두 exit 0 이어야 완료)

```bash
cd /Users/stello/elnath
go vet ./...
go test -race ./internal/learning/... ./internal/orchestrator/... ./cmd/elnath/...
make build
```

`make lint` 도 실행해 gofmt/staticcheck 경고 제거.

## Scope limits (절대 건드리지 말 것)

- `internal/learning/**` — F-3.1 에서 완료
- `internal/orchestrator/research.go` — E-3 path, F-3 무관
- `internal/telegram/**` — 미커밋 변경 진행 중
- `internal/daemon/**` — 미커밋 변경 진행 중
- `internal/scheduler/`, `internal/skill/`, `internal/audit/`, `internal/prompt/*_node.go` — 미커밋 신규 패키지, 건드리지 말 것
- `benchmarks/**` — Gate retry 결과 보존

## 미커밋 변경 공존 (중요)

Working tree 에 F-3 와 무관한 미커밋 변경들 존재 (Telegram 재설계, scheduler/skill/audit/prompt nodes, daemon queue 강화). `cmd/elnath/runtime.go` 와 `cmd/elnath/cmd_daemon.go` 는 그 중 일부가 이미 변경했다.

**규칙**:
- 해당 파일들 수정 시 **기존 미커밋 변경 preserve** 한 상태에서 F-3 변경만 적층. 기존 변경 삭제 금지.
- `runtime.go:387-389` gate 변경이 F-3 의 유일한 cmd/elnath 변경 의도. 그 외 line 은 건드리지 말 것.
- `cmd_daemon.go` 는 건드릴 필요 없음 (learningStore 초기화는 이미 미커밋에 포함될 수 있음; 건드리지 말 것).

## 완료 보고 형식

작업 종료 시 다음을 보고:
1. 수정/추가한 파일 목록 (파일 경로별 line change 개수 요약)
2. `go test -race ./internal/learning/... ./internal/orchestrator/... ./cmd/elnath/...` 최종 출력 PASS 요약
3. `go vet ./...` 결과
4. `make build` 결과
5. 발견한 기존 코드의 issue (있으면) — F-3 범위 밖이면 보고만 하고 고치지 말 것
6. 예상 commit message (spec §8 F-3.2 템플릿 참고)

커밋은 하지 마라. 사용자(stello)가 직접 commit 한다.
