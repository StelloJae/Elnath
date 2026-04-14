# Phase E-1: Ambient Research Loop (LB5 user-facing MVP)

**Status:** SPEC READY  
**Predecessor:** Phase D-1 (Secret Governance) DONE  
**Successor:** Phase E-2 (LB2 Magic Docs / SF4 Event Bus)  
**Branch:** `feat/telegram-redesign`  
**Ref:** Superiority Design v2.2 §Phase 5.2 — SE1 Ambient Autonomy (MVP subset)

---

## 1. Goal

사용자가 `elnath research start <topic>`으로 research task를 큐에 넣으면, daemon이 background에서 기존 `research.Loop`을 실행하고 결과를 wiki에 저장한다. 기존 research loop 코어는 ~40% 완성되어 있다 — 이번 phase는 daemon 통합 + CLI 인터페이스만 추가한다.

**Out of scope (Phase E-2 이후):**
- 주기적 스케줄러 (cron)
- Daemon boot task 자동 실행
- LB2 Magic Docs (conflict resolution, event bus)
- B6 Self-Improvement

## 2. Architecture Overview

```
CLI: elnath research start <topic>
    │
    ▼
┌──────────────────┐
│ TaskPayload{     │
│   Type: "research"│  (new field)
│   Prompt: topic   │
│ }                 │
└──────┬───────────┘
       │ IPC "submit"
       ▼
┌──────────────────┐
│ Daemon queue     │
│ (existing)       │
└──────┬───────────┘
       │ worker picks up
       ▼
┌──────────────────┐
│ runTask()        │
│ if Type=research:│  (new branch)
│   → runResearch()│
│ else:            │
│   → runAgent()   │  (existing)
└──────┬───────────┘
       │
       ▼
┌──────────────────┐
│ research.Loop    │
│ .Run(topic)      │  (existing)
│ → wiki pages     │
│ → Result summary │
└──────────────────┘
```

**핵심 결정:**
- `TaskPayload.Type` 필드 추가 — 빈 문자열이면 legacy agent task (backward compat)
- Daemon의 `runTask` 내부에서 Type에 따라 dispatch
- 기존 research.Loop 재사용 — 새 코드 최소화
- CLI는 기존 IPC "submit" 명령 활용 — 새 IPC 명령 추가 안 함
- Status 조회도 기존 "status" 명령 재사용

## 3. Deliverables

### 3.1 Modified: `internal/daemon/task_payload.go`

`TaskPayload`에 `Type` 필드 추가.

```go
// TaskType classifies what kind of work the daemon should perform.
type TaskType string

const (
    TaskTypeAgent    TaskType = ""         // default: LLM agent with tools
    TaskTypeResearch TaskType = "research" // research.Loop for a topic
)

type TaskPayload struct {
    Type      TaskType           `json:"type,omitempty"`
    Prompt    string             `json:"prompt"`
    SessionID string             `json:"session_id,omitempty"`
    Surface   string             `json:"surface,omitempty"`
    Principal identity.Principal `json:"principal,omitempty"`
}
```

**주의:** 빈 Type은 TaskTypeAgent로 해석된다. 기존 모든 payload가 자동으로 agent task로 분류됨 (backward compat).

`normalizeTaskPayload`에서 Type을 건드리지 않는다. Parse/Encode도 자연스럽게 JSON 필드로 처리됨.

### 3.2 New Package Deps 주의

`internal/research/` 가 `internal/wiki/`, `internal/llm/` 등에 이미 의존한다. `internal/daemon/` 에서 `internal/research/`를 import하면 circular dependency 위험이 있다.

**해결:** daemon에서 직접 research를 import하지 않는다. 대신 daemon 생성 시 `ResearchRunner` 인터페이스를 주입받는다.

### 3.3 New: `internal/daemon/runner.go` (또는 기존 파일 확장)

daemon이 task type에 따라 다른 runner를 호출할 수 있도록 인터페이스 정의.

```go
// TaskRunner executes a single task payload and returns a result.
// Implementations handle specific task types (agent, research, etc).
type TaskRunner interface {
    Run(ctx context.Context, payload TaskPayload, onText func(string)) (TaskRunnerResult, error)
}

type TaskRunnerResult struct {
    Summary string
    Result  string // structured output (JSON encoded or plain)
}
```

Daemon 구조:
```go
type Daemon struct {
    // ... existing fields ...
    agentRunner    TaskRunner  // existing behavior
    researchRunner TaskRunner  // new, optional (can be nil)
}
```

`runTask`에서:
```go
func (d *Daemon) runTask(ctx context.Context, task *Task) (TaskResult, error) {
    payload := ParseTaskPayload(task.Payload)

    var runner TaskRunner
    switch payload.Type {
    case TaskTypeResearch:
        if d.researchRunner == nil {
            return TaskResult{}, fmt.Errorf("research runner not configured")
        }
        runner = d.researchRunner
    default:
        runner = d.agentRunner
    }

    result, err := runner.Run(ctx, payload, onText)
    // ... existing result handling ...
}
```

**주의:** 현재 daemon.go의 runTask 구현을 먼저 읽고, 기존 코드를 TaskRunner로 감싸는 리팩토링이 필요하다. 기존 agent 실행 코드를 `AgentRunner` 타입으로 추출한 뒤 TaskRunner 인터페이스를 구현하게 만든다.

### 3.4 New: `internal/research/runner.go`

Research용 TaskRunner 구현. `research.Loop`을 래핑한다.

```go
package research

import (
    "context"

    "github.com/stello/elnath/internal/daemon"
    "github.com/stello/elnath/internal/llm"
    "github.com/stello/elnath/internal/wiki"
)

type TaskRunner struct {
    provider     llm.Provider
    model        string
    wikiIdx      WikiSearcher
    wikiStore    *wiki.Store
    usageTracker *llm.UsageTracker
    maxRounds    int
    costCapUSD   float64
    logger       *slog.Logger
}

func NewTaskRunner(
    provider llm.Provider,
    model string,
    wikiIdx WikiSearcher,
    wikiStore *wiki.Store,
    usageTracker *llm.UsageTracker,
    logger *slog.Logger,
    opts ...TaskRunnerOption,
) *TaskRunner

// Run implements daemon.TaskRunner.
func (r *TaskRunner) Run(ctx context.Context, payload daemon.TaskPayload, onText func(string)) (daemon.TaskRunnerResult, error)
```

Run 로직:
1. `payload.Prompt` — topic 문자열
2. HypothesisGenerator, ExperimentRunner 생성 (기존 코드)
3. `research.NewLoop(...)` 호출
4. `loop.Run(ctx, topic)` 실행
5. `ResearchResult`를 `daemon.TaskRunnerResult`로 변환:
   - Summary: `result.Summary`
   - Result: JSON 인코딩된 full result (rounds 포함)

옵션:
```go
func WithMaxRounds(n int) TaskRunnerOption
func WithCostCap(usd float64) TaskRunnerOption
func WithTaskRunnerSessionID(id string) TaskRunnerOption
```

Circular dependency 주의: `internal/research`가 `internal/daemon`을 import하면 OK (daemon은 research를 import하지 않음). 단방향이다.

### 3.5 New: `cmd/elnath/cmd_research.go`

CLI 서브커맨드 추가. 기존 CLI dispatcher 패턴 (cmd_daemon.go, cmd_run.go 등) 따름.

```go
package main

// cmdResearch handles "elnath research <subcommand>" dispatch.
func cmdResearch(args []string) int {
    if len(args) == 0 {
        return researchHelp()
    }
    switch args[0] {
    case "start":
        return researchStart(args[1:])
    case "status":
        return researchStatus(args[1:])
    case "result":
        return researchResult(args[1:])
    case "help", "--help", "-h":
        return researchHelp()
    default:
        fmt.Fprintf(os.Stderr, "unknown research subcommand: %s\n", args[0])
        return 2
    }
}
```

**서브커맨드:**

1. `elnath research start <topic>` — 새 research task 큐에 제출
   - Daemon socket 열기
   - `TaskPayload{Type: TaskTypeResearch, Prompt: topic}` 생성
   - `{"command":"submit","payload":"<encoded>"}` IPC 요청
   - 응답의 task_id 출력: `"Research task queued: 42"`

2. `elnath research status` — 모든 task 나열, Type="research"인 것만 필터
   - `{"command":"status"}` IPC 요청
   - 응답 파싱, payload에서 Type 필드 확인
   - 출력 형식:
     ```
     ID   Status    Topic                    Started
     42   running   go-idiomatic-patterns    2m ago
     41   done      elnath-ml-strategies     1h ago
     ```

3. `elnath research result <task_id>` — 완료된 research의 결과 출력
   - status 명령으로 task 목록 가져옴
   - 주어진 ID 찾기
   - `task.Result` 필드 (JSON) 파싱 → `ResearchResult` 구조
   - Summary 섹션 출력 + round-by-round findings

4. `elnath research help` — 사용법 안내

### 3.6 Modified: `cmd/elnath/main.go`

기존 dispatcher에 research 케이스 추가:

```go
switch cmd {
// ... existing cases ...
case "research":
    return cmdResearch(args)
}
```

### 3.7 Modified: `cmd/elnath/cmd_daemon.go`

Daemon 생성 시 `research.TaskRunner`를 주입.

```go
researchRunner := research.NewTaskRunner(
    provider,
    model,
    wikiIdx,
    wikiStore,
    usageTracker,
    app.Logger,
)
daemon.SetResearchRunner(researchRunner)
```

`Daemon`에 `SetResearchRunner(r TaskRunner)` 메서드 추가.

### 3.8 Tests

#### `internal/daemon/task_payload_test.go` — 기존 테스트 확장

- `Type` 필드 round-trip: Encode → Parse
- 빈 Type → TaskTypeAgent (empty string)
- `type=research` JSON → 올바르게 파싱
- 기존 legacy 문자열 payload → 여전히 TaskTypeAgent로 처리

#### `internal/research/runner_test.go`

- Mock provider + mock wiki → TaskRunner.Run() 호출
- payload.Prompt="test topic" → research.Loop 실행 확인
- Result에 Summary 포함 확인

#### `cmd/elnath/cmd_research_test.go`

- researchStart: TaskPayload Encode 확인
- researchStatus: status 응답 파싱 + Type 필터링

## 4. File Summary

### New Files (3)

| File | LOC (est.) | 역할 |
|------|-----------|------|
| `internal/daemon/runner.go` | ~40 | TaskRunner 인터페이스 + AgentRunner 추출 |
| `internal/research/runner.go` | ~80 | research.TaskRunner (daemon.TaskRunner 구현) |
| `internal/research/runner_test.go` | ~100 | Runner 테스트 |
| `cmd/elnath/cmd_research.go` | ~180 | CLI: start/status/result/help |
| `cmd/elnath/cmd_research_test.go` | ~80 | CLI 테스트 |

### Modified Files (4)

| File | 변경 내용 |
|------|----------|
| `internal/daemon/task_payload.go` | Type 필드 추가, TaskType constants |
| `internal/daemon/task_payload_test.go` | Type 관련 테스트 추가 |
| `internal/daemon/daemon.go` | runTask 분기, researchRunner 필드, SetResearchRunner |
| `cmd/elnath/main.go` | research 서브커맨드 dispatch |
| `cmd/elnath/cmd_daemon.go` | research.TaskRunner 생성 및 주입 |

## 5. Acceptance Criteria

- [ ] `go test -race ./...` 모든 테스트 통과
- [ ] `go vet ./...` 경고 없음
- [ ] `make build` 성공
- [ ] `elnath research help` 사용법 표시
- [ ] daemon 실행 중 `elnath research start "go patterns"` → task ID 반환
- [ ] 수초~수분 후 `elnath research status` → "done" 상태 확인
- [ ] `elnath research result <id>` → summary + rounds 출력
- [ ] wiki에 `research/go-patterns/round-*.md` 페이지 생성 확인
- [ ] 기존 agent task (일반 `/submit`) 여전히 정상 동작 (backward compat)

## 6. Risk

| Risk | Mitigation |
|------|-----------|
| Circular dep (daemon ↔ research) | TaskRunner 인터페이스로 decouple. research가 daemon을 import 하되, 그 역은 아님 |
| Research loop 비용 폭주 | 기존 `WithCostCap(5.0)` 활용. daemon에 주입 시 환경변수로 override 가능하게 |
| 기존 agent task 회귀 | TaskType 빈 문자열 = agent로 라우팅. 모든 기존 payload는 Type 필드 없이 저장됨 |
| CLI ↔ daemon 프로토콜 버전 skew | TaskPayload JSON은 unknown 필드 허용. Type 없이 전송된 task는 legacy agent로 처리 |
| daemon 재시작 시 running research 소실 | 기존 `RecoverStale` 동작 그대로. research는 resume 불가 (재시작 시 failed로 마킹) — Phase E-2에서 round-level resume 고려 |

## 7. Future Work (Phase E-2)

- 주기 스케줄러 (`internal/daemon/scheduler.go`) — cron-like
- Boot task config — YAML로 topic + 주기 지정
- LB2 Magic Docs — atomic wiki write, conflict resolution
- SF4 Page-Read Event Bus
- Research round-level resume (mid-flight recovery)
