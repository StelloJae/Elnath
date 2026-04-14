# OpenCode Delegation Prompt: Phase E-2 Periodic Task Scheduler

2 phase. 각 phase 완료 후 `go test -race` + `go vet` 검증.

---

## Phase 1: internal/scheduler/ 패키지 (pure logic)

```
elnath 프로젝트 (`/Users/stello/elnath/`, feat/telegram-redesign 브랜치)에서 Phase E-2 작업을 시작한다.

목표: `internal/scheduler/` 패키지를 신설한다. YAML 파일에서 scheduled task 목록을 로드하고, 각 task를 interval 주기로 queue에 enqueue한다.

### 참고할 기존 코드

internal/daemon/task_payload.go — TaskType 상수 (TaskTypeAgent, TaskTypeResearch), TaskPayload struct, EncodeTaskPayload 함수

internal/daemon/queue.go의 Enqueue 시그니처:
```go
func (q *Queue) Enqueue(ctx context.Context, payload string, idemKey string) (int64, bool, error)
```
- 반환: (taskID, existed, error)
- existed=true면 중복 (pending/running 상태의 동일 idemKey 존재)
- 완료된 task는 dedup 대상 아님 (같은 key로 다시 enqueue 가능)

### 작업 1: internal/scheduler/task.go

```go
package scheduler

import (
    "fmt"
    "time"
)

type ScheduledTask struct {
    Name       string
    Type       string        // "agent" | "research" (empty = agent)
    Prompt     string
    Interval   time.Duration
    RunOnStart bool
    Enabled    bool
    SessionID  string
    Surface    string
}

func (t *ScheduledTask) Validate() error {
    if t.Name == "" {
        return fmt.Errorf("scheduled task name required")
    }
    if t.Prompt == "" {
        return fmt.Errorf("scheduled task %q: prompt required", t.Name)
    }
    if t.Interval < time.Minute {
        return fmt.Errorf("scheduled task %q: interval must be >= 1m (got %s)", t.Name, t.Interval)
    }
    if t.Type != "" && t.Type != "agent" && t.Type != "research" {
        return fmt.Errorf("scheduled task %q: invalid type %q (must be agent or research)", t.Name, t.Type)
    }
    return nil
}
```

### 작업 2: internal/scheduler/config.go

```go
package scheduler

import (
    "fmt"
    "os"
    "time"

    "gopkg.in/yaml.v3"
)

type rawTask struct {
    Name       string `yaml:"name"`
    Type       string `yaml:"type"`
    Prompt     string `yaml:"prompt"`
    Interval   string `yaml:"interval"`
    RunOnStart bool   `yaml:"run_on_start"`
    Enabled    *bool  `yaml:"enabled"`
    SessionID  string `yaml:"session_id"`
    Surface    string `yaml:"surface"`
}

type rawConfig struct {
    ScheduledTasks []rawTask `yaml:"scheduled_tasks"`
}
```

함수:

`LoadConfig(path string) ([]ScheduledTask, error)`

로직:
1. `data, err := os.ReadFile(path)` → os.IsNotExist 이면 `return nil, nil` (no-op)
2. 다른 에러 → 에러 반환
3. `yaml.Unmarshal(data, &raw)` → 에러 처리
4. 빈 `raw.ScheduledTasks` → `return nil, nil`
5. 결과 slice 준비, seen map으로 중복 이름 체크
6. 각 raw에 대해:
   - `interval, err := time.ParseDuration(raw.Interval)` → 에러 시 `fmt.Errorf("scheduled task %q: parse interval: %w", raw.Name, err)`
   - enabled := true (기본값); `raw.Enabled != nil` 이면 `*raw.Enabled`
   - 중복 name → 에러
   - seen[raw.Name] = true
   - ScheduledTask 생성
   - `task.Validate()` 호출, 에러 반환
   - enabled=false이면 `slog.Info("scheduler: task disabled", "name", task.Name)` (logger 없으면 skip, 이 함수는 logger 주입 안 받음)
   - enabled=true면 결과 slice에 append
7. 최종 slice 반환

주의: LoadConfig는 logger를 받지 않는다. disabled task 로그는 Scheduler가 생성 시점에 찍어도 된다 — 이 경우 LoadConfig는 모든 task를 반환하고, Scheduler.New에서 enabled=false는 제외. **간단하게: LoadConfig가 enabled=false 제외해서 반환하고, 로그는 생략**. 사용자가 disabled로 두는 건 설정상 의도된 동작이므로 로그 필수 아님.

### 작업 3: internal/scheduler/scheduler.go

```go
package scheduler

import (
    "context"
    "log/slog"
    "sync"
    "time"

    "github.com/stello/elnath/internal/daemon"
)

type Enqueuer interface {
    Enqueue(ctx context.Context, payload string, idemKey string) (int64, bool, error)
}

type Scheduler struct {
    tasks  []ScheduledTask
    enq    Enqueuer
    logger *slog.Logger
}

func New(tasks []ScheduledTask, enq Enqueuer, logger *slog.Logger) *Scheduler {
    if logger == nil {
        logger = slog.Default()
    }
    return &Scheduler{tasks: tasks, enq: enq, logger: logger}
}

func (s *Scheduler) Run(ctx context.Context) error {
    var wg sync.WaitGroup
    for _, task := range s.tasks {
        task := task
        wg.Add(1)
        go func() {
            defer wg.Done()
            s.runTask(ctx, task)
        }()
    }
    wg.Wait()
    return nil
}

func (s *Scheduler) runTask(ctx context.Context, task ScheduledTask) {
    if task.RunOnStart {
        s.enqueueOnce(ctx, task)
    }
    ticker := time.NewTicker(task.Interval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            s.enqueueOnce(ctx, task)
        }
    }
}

func (s *Scheduler) enqueueOnce(ctx context.Context, task ScheduledTask) {
    if ctx.Err() != nil {
        return
    }
    taskType := daemon.TaskType(task.Type)
    if taskType == "" {
        taskType = daemon.TaskTypeAgent
    }
    payload := daemon.TaskPayload{
        Type:      taskType,
        Prompt:    task.Prompt,
        SessionID: task.SessionID,
        Surface:   task.Surface,
    }
    encoded := daemon.EncodeTaskPayload(payload)
    idemKey := "scheduled:" + task.Name

    id, existed, err := s.enq.Enqueue(ctx, encoded, idemKey)
    switch {
    case err != nil:
        s.logger.Warn("scheduler: enqueue failed", "task", task.Name, "error", err)
    case existed:
        s.logger.Info("scheduler: skipped (previous run still active)", "task", task.Name)
    default:
        s.logger.Info("scheduler: enqueued", "task", task.Name, "task_id", id)
    }
}
```

### 작업 4: internal/scheduler/scheduler_test.go

**Task.Validate 테스트:**
- 빈 name → 에러
- 빈 prompt → 에러
- Interval=30s → 에러 (<1m)
- Type="foo" → 에러
- Type="" (빈 문자열) → 에러 없음 (agent로 해석)
- 정상 → nil

**LoadConfig 테스트 (t.TempDir에 YAML 파일 작성):**

1. 존재하지 않는 경로 → nil, nil
2. 빈 파일 → nil, nil
3. 정상 YAML:
```yaml
scheduled_tasks:
  - name: task1
    prompt: hello
    interval: 1h
  - name: task2
    type: research
    prompt: go patterns
    interval: 24h
    run_on_start: true
```
→ 2개 반환, 올바른 값
4. `enabled: false` → 결과에서 제외
5. 잘못된 interval → 에러 ("parse interval")
6. 중복 name → 에러 ("duplicate")
7. Type="invalid" → Validate 에러
8. 모든 task disabled → 빈 slice (또는 nil) 반환

**Scheduler.Run 테스트 (mock Enqueuer):**

```go
type mockEnq struct {
    mu    sync.Mutex
    calls []string // idemKeys로 저장
    err   error
    existed bool
}

func (m *mockEnq) Enqueue(_ context.Context, payload, idemKey string) (int64, bool, error) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.calls = append(m.calls, idemKey)
    return int64(len(m.calls)), m.existed, m.err
}
```

테스트 케이스:

1. **RunOnStart 즉시 실행**:
   - Scheduler 직접 생성 (LoadConfig 우회, Validate 우회를 위해 struct 직접 채움, Interval=10*time.Millisecond 등 짧게)
   - 주의: Validate를 우회하기 위해 `ScheduledTask{...}` struct literal 사용
   - ctx를 생성, 100ms 후 cancel
   - scheduler.Run(ctx)를 고루틴에서 실행
   - RunOnStart=true task 하나만 등록
   - 50ms 대기 후 mock.calls 확인 → 최소 1개 (첫 실행) 포함
   - cancel() 후 wg.Wait() 대기

2. **Graceful shutdown**:
   - ctx cancel 후 Run이 빠르게 리턴 확인
   - goroutine leak 없음

3. **existed=true 로그**:
   - mock.existed=true 설정
   - enqueueOnce 직접 호출하거나 Run으로 1 tick
   - 에러 없이 리턴 확인

4. **Type 매핑**:
   - task.Type="research" → enqueueOnce 호출 후 mock의 마지막 payload를 parse해서 Type 확인
   - task.Type="" → agent로 해석

테스트에서 interval=10ms 같은 짧은 값을 쓰려면 Validate를 우회해야 한다. struct literal로 직접 생성하면 된다 (`ScheduledTask{Name: "t", Prompt: "p", Interval: 10 * time.Millisecond, RunOnStart: true}`). Validate는 LoadConfig path에서만 호출되므로 문제없음.

### 검증

```bash
go test -race ./internal/scheduler/...
go vet ./internal/scheduler/...
```

통과 확인.
```

---

## Phase 2: Config + Daemon 통합 + cmd_daemon 주입

```
Phase E-2 Phase 2. Phase 1에서 internal/scheduler/ 패키지가 완성됐다. 이제 daemon에 통합한다.

### 작업 1: internal/config/config.go — DaemonConfig 확장

`DaemonConfig` struct에 필드 추가:

```go
type DaemonConfig struct {
    // ... existing fields ...
    ScheduledTasksPath string `yaml:"scheduled_tasks_path"`
}
```

기본값은 빈 문자열 (비활성). defaults.go에 명시적으로 설정할 필요 없음.

### 작업 2: internal/daemon/daemon.go — Scheduler 통합

Daemon struct 수정:

```go
type Daemon struct {
    // ... existing fields ...
    scheduler Scheduler
}

type Scheduler interface {
    Run(ctx context.Context) error
}

func (d *Daemon) WithScheduler(s Scheduler) {
    d.scheduler = s
}
```

**중요:** `daemon.Scheduler` 인터페이스는 daemon 패키지에 정의한다. `scheduler.Scheduler`가 이 인터페이스를 자동으로 만족한다 (Run(ctx) error 시그니처 일치). 이렇게 하면 daemon이 scheduler를 import하지 않아 circular dep 없음.

`Start` 메서드에서 worker pool 기동 부분 근처에 scheduler goroutine 추가:

```go
// 기존 worker 시작 코드 후에...
if d.scheduler != nil {
    d.wg.Add(1)
    go func() {
        defer d.wg.Done()
        if err := d.scheduler.Run(ctx); err != nil && ctx.Err() == nil {
            d.logger.Error("scheduler stopped unexpectedly", "error", err)
        }
    }()
}
```

**기존 daemon.go의 Start 메서드를 먼저 read하고 정확한 위치를 파악**. worker goroutine들이 시작되는 지점 직후에 scheduler goroutine을 추가한다. 이들은 모두 `d.wg`에 등록되어 Stop 시 함께 정리된다.

graceful shutdown은 기존 `d.cancel()` + `d.wg.Wait()` 로 자동 처리됨. scheduler는 ctx.Done() 수신 시 Run이 리턴하므로 별도 처리 불필요.

### 작업 3: cmd/elnath/cmd_daemon.go — scheduler 생성 + 주입

기존 cmd_daemon.go를 읽고, daemon 생성 직후 + `d.Start(ctx)` 호출 직전에 scheduler 주입 코드 추가.

```go
import (
    "path/filepath"
    "github.com/stello/elnath/internal/scheduler"
)

// ... daemon 생성 후 ...

if cfg.Daemon.ScheduledTasksPath != "" {
    path := cfg.Daemon.ScheduledTasksPath
    if !filepath.IsAbs(path) {
        path = filepath.Join(cfg.DataDir, path)
    }
    tasks, err := scheduler.LoadConfig(path)
    if err != nil {
        app.Logger.Error("scheduler config load failed", "path", path, "error", err)
        return fmt.Errorf("scheduler: %w", err)
    }
    if len(tasks) > 0 {
        sch := scheduler.New(tasks, queue, app.Logger)
        d.WithScheduler(sch)
        app.Logger.Info("scheduler enabled", "path", path, "tasks", len(tasks))
    } else {
        app.Logger.Info("scheduler config empty or all disabled", "path", path)
    }
}

// ... 이후 d.Start(ctx) 호출 ...
```

**주의:** 
- `queue` 변수가 `*daemon.Queue` 타입이어야 `scheduler.New`에 전달 가능 (scheduler.Enqueuer 인터페이스 만족)
- `cfg.Daemon.ScheduledTasksPath`가 상대 경로면 DataDir 기준으로 해석
- 파일이 없어도 (LoadConfig가 nil 반환) 에러 아님

### 작업 4: 테스트

**internal/daemon/daemon_test.go** (기존 파일 있으면 확장, 없으면 신규):
- WithScheduler 호출 후 Start → scheduler.Run이 호출됐는지 확인 (mock scheduler로)
- Stop → scheduler goroutine이 정상 종료 (goroutine leak 없음)

Mock scheduler:
```go
type mockScheduler struct {
    started atomic.Bool
    stopped atomic.Bool
}

func (m *mockScheduler) Run(ctx context.Context) error {
    m.started.Store(true)
    <-ctx.Done()
    m.stopped.Store(true)
    return nil
}
```

**cmd/elnath/cmd_daemon_test.go** (optional):
- ScheduledTasksPath가 비어있으면 scheduler 주입 안 함
- ScheduledTasksPath 유효 → LoadConfig 호출, daemon.WithScheduler 호출 확인

이 테스트는 daemon 전체를 기동해야 해서 복잡할 수 있다. 필수는 아니며, 대신 내부 헬퍼 함수를 분리해서 단위 테스트 가능하게 만드는 것도 옵션.

### 전체 검증

```bash
go test -race ./internal/scheduler/... ./internal/daemon/... ./cmd/elnath/...
go vet ./...
make build
```

전부 통과 확인.

### 수동 검증 (선택)

1. `~/.elnath/scheduled_tasks.yaml` 생성:
```yaml
scheduled_tasks:
  - name: test-ping
    type: agent
    prompt: "Say hello"
    interval: 2m
    run_on_start: true
    enabled: true
```

2. `~/.elnath/config.yaml` 에 `daemon.scheduled_tasks_path: scheduled_tasks.yaml` 추가 (상대 경로 — DataDir 기준)

3. daemon 재시작 → 로그에 "scheduler enabled tasks=1" 확인

4. 즉시 `./elnath research status` 또는 task queue 조회 → test-ping task 큐에 등록됨 확인

5. 2분 후 다시 tick → 이전 것이 done 상태면 재enqueue 확인
```
