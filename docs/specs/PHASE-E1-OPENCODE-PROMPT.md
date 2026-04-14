# OpenCode Delegation Prompt: Phase E-1 Ambient Research Loop

3 phase로 나뉜다. 각 phase 완료 후 `go test -race` + `go vet` 검증.

---

## Phase 1: TaskPayload.Type + TaskRunner 인터페이스 (decouple)

```
elnath 프로젝트 (`/Users/stello/elnath/`, feat/telegram-redesign 브랜치)에서 Phase E-1 작업을 시작한다.

목표: TaskPayload에 Type 필드를 추가하고, daemon에 TaskRunner 인터페이스를 도입해 기존 agent 실행 코드를 분리한다. 이는 research runner를 나중에 주입할 수 있게 하는 기반 작업이다.

### 참고할 기존 코드

internal/daemon/task_payload.go 의 현재 구조:
```go
type TaskPayload struct {
    Prompt    string             `json:"prompt"`
    SessionID string             `json:"session_id,omitempty"`
    Surface   string             `json:"surface,omitempty"`
    Principal identity.Principal `json:"principal,omitempty"`
}
```

ParseTaskPayload/EncodeTaskPayload는 JSON이거나 plain string을 양방향 지원한다.

internal/daemon/daemon.go의 runTask 메서드가 현재 task 실행의 중심이다. 이를 먼저 read하고 구조를 파악한 뒤 리팩토링한다.

### 작업 1: internal/daemon/task_payload.go — Type 필드 추가

변경:

1. TaskType 타입 및 상수 정의:
```go
type TaskType string

const (
    TaskTypeAgent    TaskType = ""
    TaskTypeResearch TaskType = "research"
)
```

2. TaskPayload struct에 Type 필드 추가 (Prompt 위에):
```go
type TaskPayload struct {
    Type      TaskType           `json:"type,omitempty"`
    Prompt    string             `json:"prompt"`
    SessionID string             `json:"session_id,omitempty"`
    Surface   string             `json:"surface,omitempty"`
    Principal identity.Principal `json:"principal,omitempty"`
}
```

3. `normalizeTaskPayload`에서는 Type을 수정하지 않는다 (이미 올바른 값으로 들어옴).

4. `EncodeTaskPayload`의 short-circuit 조건에 Type 체크 추가:
```go
if payload.Type == TaskTypeAgent && payload.SessionID == "" && payload.Surface == "" && payload.Principal.IsZero() {
    return payload.Prompt
}
```

### 작업 2: internal/daemon/task_payload_test.go — 테스트 확장

기존 테스트를 읽고, 다음 케이스 추가:

- Type="research" round-trip: Encode → Parse → Type 유지
- 빈 Type (legacy JSON) → Parse → Type == TaskTypeAgent (빈 문자열)
- Plain string payload → Type == TaskTypeAgent
- Type="research" + Prompt 만 있는 TaskPayload → Encode → JSON 형태 (short-circuit 안 함)

### 작업 3: internal/daemon/runner.go (신규)

TaskRunner 인터페이스를 정의한다. 기존 agent 실행 코드를 이 인터페이스로 추상화한다.

```go
package daemon

import "context"

// TaskRunnerResult carries the output of a single task execution.
type TaskRunnerResult struct {
    Summary string
    Result  string
}

// TaskRunner executes a task payload.
// Implementations handle specific TaskType values.
type TaskRunner interface {
    Run(ctx context.Context, payload TaskPayload, onText func(string)) (TaskRunnerResult, error)
}
```

### 작업 4: internal/daemon/daemon.go — TaskRunner 필드 및 dispatch

**중요:** 기존 runTask 메서드를 먼저 read하고 그 로직을 이해할 것. 그 안의 agent 실행 부분을 `agentRunner` TaskRunner로 추출한다.

단계:

1. Daemon struct에 필드 추가:
```go
type Daemon struct {
    // ... existing fields ...
    agentRunner    TaskRunner
    researchRunner TaskRunner
}
```

2. 기존 Daemon 생성자(NewDaemon 또는 유사)에 agentRunner 주입 파라미터 추가. 기존 호출부는 수정해야 한다.

3. Setter 메서드 추가:
```go
func (d *Daemon) SetResearchRunner(r TaskRunner) {
    d.researchRunner = r
}
```

4. runTask 리팩토링:
   - `ParseTaskPayload(task.Payload)` → payload
   - `payload.Type`에 따라 runner 선택:
     ```go
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
     ```
   - `result, err := runner.Run(ctx, payload, onText)` 호출
   - 기존 결과 처리 로직 (status 업데이트, completion 전송 등) 유지

5. 기존 agent 실행 로직을 별도 타입 `agentTaskRunner`로 추출:
   - 기존 daemon 내부에 있던 LLM call / agent loop 실행 코드를 `func (r *agentTaskRunner) Run(...)` 메서드로 이동
   - 기존 의존성(agent.Agent, conversation manager 등)을 agentTaskRunner에 필드로 보관
   - NewDaemon 호출부에서 agentTaskRunner를 먼저 생성 후 주입

**주의:** 이 리팩토링은 daemon.go 전체를 건드릴 수 있다. 기존 테스트가 모두 통과하는지 확인 필수. 기존 단위 테스트의 expected 동작이 바뀌면 안 된다.

만약 기존 runTask의 구조가 복잡해서 깔끔한 분리가 어렵다면, **최소 침습 대안**:
- runTask 내부에 Type 분기만 추가하고, TaskTypeResearch 케이스에서 researchRunner.Run 호출
- TaskTypeAgent (default)는 기존 로직 그대로 유지
- agentRunner 추출은 Phase E-2로 미룸

어느 쪽이 안전한지 판단해서 진행한다. **최소 침습 대안이 기본 선택.**

### 검증

```bash
go test -race ./internal/daemon/...
go vet ./internal/daemon/...
```

통과 확인. 기존 daemon 테스트가 모두 통과해야 한다 (agent task 회귀 없음).
```

---

## Phase 2: research.TaskRunner + cmd_daemon.go 통합

```
Phase E-1 Phase 2. Phase 1에서 TaskPayload.Type + daemon TaskRunner 인터페이스가 준비됐다.

### 참고할 기존 코드

internal/research/loop.go 의 `NewLoop(...)` 시그니처와 `(l *Loop) Run(ctx, topic) (*ResearchResult, error)` 를 먼저 read한다.

`ResearchResult` 구조도 확인 (Topic, Rounds, Summary, TotalCost 등).

internal/research/hypothesis.go, internal/research/experiment.go의 생성자 시그니처도 확인.

### 작업 1: internal/research/runner.go (신규)

research package에 daemon.TaskRunner를 구현하는 TaskRunner를 만든다.

```go
package research

import (
    "context"
    "encoding/json"
    "log/slog"

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
    logger       *slog.Logger
    maxRounds    int
    costCapUSD   float64
}

type TaskRunnerOption func(*TaskRunner)

func WithRunnerMaxRounds(n int) TaskRunnerOption {
    return func(r *TaskRunner) {
        if n > 0 {
            r.maxRounds = n
        }
    }
}

func WithRunnerCostCap(usd float64) TaskRunnerOption {
    return func(r *TaskRunner) {
        if usd > 0 {
            r.costCapUSD = usd
        }
    }
}

func NewTaskRunner(
    provider llm.Provider,
    model string,
    wikiIdx WikiSearcher,
    wikiStore *wiki.Store,
    usageTracker *llm.UsageTracker,
    logger *slog.Logger,
    opts ...TaskRunnerOption,
) *TaskRunner {
    r := &TaskRunner{
        provider:     provider,
        model:        model,
        wikiIdx:      wikiIdx,
        wikiStore:    wikiStore,
        usageTracker: usageTracker,
        logger:       logger,
        maxRounds:    5,
        costCapUSD:   5.0,
    }
    for _, opt := range opts {
        opt(r)
    }
    return r
}

// Run implements daemon.TaskRunner.
func (r *TaskRunner) Run(ctx context.Context, payload daemon.TaskPayload, onText func(string)) (daemon.TaskRunnerResult, error)
```

Run 로직:

1. `topic := strings.TrimSpace(payload.Prompt)` — 빈 문자열이면 에러
2. HypothesisGenerator 생성:
```go
hg := NewHypothesisGenerator(r.provider, r.model, r.logger)  // 실제 시그니처는 hypothesis.go 확인
```
3. ExperimentRunner 생성:
```go
er := NewExperimentRunner(r.provider, r.model, nil /* tools */, r.logger)
// ExperimentRunner가 tools.Registry를 요구하면 nil 또는 기본 registry 전달
// 시그니처는 experiment.go에서 확인
```
4. Loop 생성:
```go
loop := NewLoop(
    hg, er,
    r.wikiIdx, r.wikiStore,
    r.usageTracker,
    r.provider, r.model, r.logger,
    WithMaxRounds(r.maxRounds),
    WithCostCap(r.costCapUSD),
    WithOnText(onText),
    WithSessionID(payload.SessionID),
)
```
5. `result, err := loop.Run(ctx, topic)` 실행
6. `err != nil` → daemon.TaskRunnerResult{}, err 반환
7. JSON으로 full result 인코딩:
```go
raw, _ := json.Marshal(result)
```
8. 반환:
```go
return daemon.TaskRunnerResult{
    Summary: result.Summary,
    Result:  string(raw),
}, nil
```

**중요:** `research.NewLoop`, `NewHypothesisGenerator`, `NewExperimentRunner` 의 실제 시그니처는 각 파일을 read해서 정확히 맞춰라. 의존성(특히 ExperimentRunner가 tools.Registry를 받는지)을 확인하고, 필요한 경우 빈 registry (`tools.NewRegistry()`)를 전달.

### 작업 2: internal/research/runner_test.go

- NewTaskRunner: 옵션 없이 생성 → maxRounds=5, costCapUSD=5.0 기본값
- WithRunnerMaxRounds(3) → maxRounds=3
- WithRunnerMaxRounds(-1) → 기본값 유지 (음수 거부)
- Run: 빈 Prompt → 에러
- Run: mock provider + mock wiki store로 정상 실행 → TaskRunnerResult 반환, Summary 비어있지 않음

Mock 작성 시 기존 research 테스트의 mock 패턴을 참조. research 패키지에 이미 mock helper가 있을 수 있다.

### 작업 3: cmd/elnath/cmd_daemon.go — research runner 생성 + 주입

기존 cmd_daemon.go를 read하여 daemon 생성 부분 확인.

daemon 생성 후 research runner 주입 코드 추가:

```go
import (
    "github.com/stello/elnath/internal/research"
)

// ... 기존 daemon 생성 코드 ...

if wikiIdx != nil && wikiStore != nil {
    rr := research.NewTaskRunner(
        provider,
        model,
        wikiIdx,
        wikiStore,
        usageTracker, // 기존에 있어야 함. 없으면 nil 전달
        app.Logger,
    )
    d.SetResearchRunner(rr)
}
```

`usageTracker`가 기존 cmd_daemon.go에 없다면 nil 전달하거나, 간단히 `llm.NewUsageTracker(db)` 생성.

### 검증

```bash
go test -race ./internal/research/... ./internal/daemon/... ./cmd/elnath/...
go vet ./...
make build
```

모두 통과.
```

---

## Phase 3: CLI cmd_research.go + main.go dispatch

```
Phase E-1 Phase 3. Phase 1-2 완료. 이제 사용자 대면 CLI를 추가한다.

### 참고할 기존 코드

cmd/elnath/ 디렉토리에서 기존 서브커맨드 구현 패턴 확인:
- cmd_daemon.go, cmd_run.go, cmd_version.go 등
- main.go의 switch dispatch

Daemon IPC 통신 패턴도 확인. 기존에 `elnath daemon` 외 client → daemon 통신 코드가 있다면 (예: task submit) 재사용.

grep으로 "submit" 관련 IPC 클라이언트 코드 찾기:
```
grep -r "\"command\":\\s*\"submit\"" cmd/elnath/ internal/daemon/
```

### 작업 1: cmd/elnath/cmd_research.go (신규)

```go
package main

import (
    "encoding/json"
    "fmt"
    "os"
    "strings"
    "time"

    "github.com/stello/elnath/internal/daemon"
    "github.com/stello/elnath/internal/research"
)

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

func researchHelp() int {
    fmt.Println(`Usage: elnath research <subcommand> [args]

Subcommands:
  start <topic>     Queue a new research task for <topic>
  status            List all research tasks (pending/running/done)
  result <task_id>  Show final result of a completed research task
  help              Show this help
`)
    return 0
}
```

**researchStart:**
```go
func researchStart(args []string) int {
    if len(args) == 0 {
        fmt.Fprintln(os.Stderr, "error: topic required")
        return 2
    }
    topic := strings.Join(args, " ")

    payload := daemon.TaskPayload{
        Type:   daemon.TaskTypeResearch,
        Prompt: topic,
    }
    encoded := daemon.EncodeTaskPayload(payload)

    // 기존 daemon client 헬퍼 사용. 예상 함수명: daemonSubmit() 또는 sendDaemonCommand()
    // 없으면 직접 socket 열기

    resp, err := sendDaemonCommand(map[string]any{
        "command": "submit",
        "payload": encoded,
    })
    if err != nil {
        fmt.Fprintf(os.Stderr, "error: %v\n", err)
        return 1
    }

    // resp 파싱해서 task_id 출력
    // 응답 구조: {"ok":true,"data":{"task_id":42,"existed":false}}
    data, _ := resp["data"].(map[string]any)
    id := fmt.Sprintf("%v", data["task_id"])
    fmt.Printf("Research task queued: %s (topic: %s)\n", id, topic)
    return 0
}
```

**researchStatus:**
```go
func researchStatus(_ []string) int {
    resp, err := sendDaemonCommand(map[string]any{"command": "status"})
    if err != nil {
        fmt.Fprintf(os.Stderr, "error: %v\n", err)
        return 1
    }

    data, _ := resp["data"].(map[string]any)
    tasks, _ := data["tasks"].([]any)

    fmt.Printf("%-6s %-10s %-40s %s\n", "ID", "STATUS", "TOPIC", "AGE")
    for _, t := range tasks {
        task, _ := t.(map[string]any)
        rawPayload, _ := task["payload"].(string)
        payload := daemon.ParseTaskPayload(rawPayload)
        if payload.Type != daemon.TaskTypeResearch {
            continue
        }
        id := fmt.Sprintf("%v", task["id"])
        status := fmt.Sprintf("%v", task["status"])
        age := formatAge(task["updated_at"])
        fmt.Printf("%-6s %-10s %-40s %s\n", id, status, payload.Prompt, age)
    }
    return 0
}

func formatAge(raw any) string {
    s, _ := raw.(string)
    t, err := time.Parse(time.RFC3339, s)
    if err != nil {
        return "-"
    }
    return time.Since(t).Round(time.Second).String() + " ago"
}
```

**researchResult:**
```go
func researchResult(args []string) int {
    if len(args) == 0 {
        fmt.Fprintln(os.Stderr, "error: task_id required")
        return 2
    }
    wantID := args[0]

    resp, err := sendDaemonCommand(map[string]any{"command": "status"})
    if err != nil {
        fmt.Fprintf(os.Stderr, "error: %v\n", err)
        return 1
    }
    data, _ := resp["data"].(map[string]any)
    tasks, _ := data["tasks"].([]any)

    for _, t := range tasks {
        task, _ := t.(map[string]any)
        id := fmt.Sprintf("%v", task["id"])
        if id != wantID {
            continue
        }
        status := fmt.Sprintf("%v", task["status"])
        if status != "done" {
            fmt.Printf("Task %s status: %s\n", wantID, status)
            return 0
        }
        resultRaw := fmt.Sprintf("%v", task["result"])
        var rr research.ResearchResult
        if err := json.Unmarshal([]byte(resultRaw), &rr); err != nil {
            fmt.Println("Summary:", task["summary"])
            return 0
        }
        fmt.Printf("Topic: %s\n", rr.Topic)
        fmt.Printf("Total cost: $%.4f\n", rr.TotalCost)
        fmt.Println("Summary:")
        fmt.Println(rr.Summary)
        fmt.Printf("\nRounds: %d\n", len(rr.Rounds))
        for i, round := range rr.Rounds {
            fmt.Printf("-- Round %d --\n", i+1)
            // round 구조는 research.RoundResult 에서 확인
        }
        return 0
    }
    fmt.Fprintf(os.Stderr, "task %s not found\n", wantID)
    return 1
}
```

**sendDaemonCommand 헬퍼:** 이미 존재하면 재사용. 없으면 이 파일에 추가:

```go
func sendDaemonCommand(cmd map[string]any) (map[string]any, error) {
    // Unix socket 경로는 config에서 가져옴
    // {DataDir}/elnath.sock 가능성 높음 — cmd_daemon.go 참고
    sockPath := daemonSocketPath() // 헬퍼가 기존에 있는지 확인

    conn, err := net.Dial("unix", sockPath)
    if err != nil {
        return nil, fmt.Errorf("daemon not running: %w", err)
    }
    defer conn.Close()

    enc := json.NewEncoder(conn)
    if err := enc.Encode(cmd); err != nil {
        return nil, err
    }

    var resp map[string]any
    dec := json.NewDecoder(conn)
    if err := dec.Decode(&resp); err != nil {
        return nil, err
    }
    if ok, _ := resp["ok"].(bool); !ok {
        return nil, fmt.Errorf("daemon: %v", resp["error"])
    }
    return resp, nil
}
```

**중요:** 기존 코드에 이미 daemon client 헬퍼가 있다면 재사용. 없으면 위 방식으로 추가. socket 경로 헬퍼도 기존 것을 찾아서 사용.

### 작업 2: cmd/elnath/main.go — research 서브커맨드 dispatch

main.go의 switch 블록을 찾아 case 추가:

```go
case "research":
    return cmdResearch(args)
```

### 작업 3: cmd/elnath/cmd_research_test.go

테스트가 어려울 수 있다 (daemon socket 필요). 최소한:
- researchStart: 빈 args → exit code 2
- researchStart: Type=research 로 EncodeTaskPayload 확인 (daemon 없이 payload 빌드만 검증)
- researchHelp: exit code 0

daemon을 실제로 띄우는 통합 테스트는 생략. 수동 검증에 의존.

### 검증

```bash
go test -race ./...
go vet ./...
make build
```

전부 통과 확인.

### 수동 검증 (선택)

1. daemon 시작: `./elnath daemon` (또는 현재 launchd로 관리 중이면 재시작)
2. research 제출: `./elnath research start "go idiomatic error handling"`
3. 출력: `Research task queued: <id>`
4. 잠시 후 상태 확인: `./elnath research status`
5. 완료 시 결과: `./elnath research result <id>`
6. wiki에 `research/go-idiomatic-error-handling/round-*.md` 생성 확인
```
