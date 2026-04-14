# Phase E-2: Periodic Task Scheduler

**Status:** SPEC READY  
**Predecessor:** Phase E-1 (Ambient Research Loop) DONE  
**Successor:** Phase E-3 (LB2 Magic Docs) or Phase F  
**Branch:** `feat/telegram-redesign`  
**Ref:** Superiority Design v2.2 §Phase 5.2 — SE1 Ambient Autonomy (scheduler 부분)

---

## 1. Goal

Daemon startup 시 YAML 파일에서 scheduled task 목록을 읽어, 각 task를 선언된 interval 주기로 queue에 enqueue한다. 이전 실행이 아직 pending/running이면 자동 skip (queue의 idempotency로 처리). Graceful shutdown 지원.

예시 usecase:
- 매일 `go idiomatic patterns` research 자동 실행
- 매시간 agent task로 시스템 health check
- 매주 기존 wiki 페이지 refresh

**Out of scope:**
- Cron expression (매주 화요일 오전 9시 같은 패턴) — `time.ParseDuration` 으로 충분한 interval만
- Scheduled task 동적 수정 (파일 재로드) — daemon 재시작 필요
- 실행 히스토리 저장/조회 CLI — 기존 `research status` / task queue로 충분

## 2. Architecture Overview

```
~/.elnath/scheduled_tasks.yaml
    │
    ▼ load on daemon startup
┌──────────────────────┐
│ scheduler.Scheduler  │
│ ┌──────────────────┐ │
│ │ Task 1 (24h)     │─┼─────► ticker.C ──┐
│ │ Task 2 (1h)      │─┼─────► ticker.C ──┼─► queue.Enqueue()
│ │ Task 3 (30m)     │─┼─────► ticker.C ──┘   idemKey: "scheduled:<name>"
│ └──────────────────┘ │
└──────────────────────┘
        │
        │ ctx.Done()
        ▼
  graceful stop
```

**핵심 설계 결정:**

- **Per-task goroutine + time.Ticker**: 각 scheduled task가 독립 goroutine. `select`로 `ticker.C` 와 `ctx.Done()` 감시. 간단하고 안전.
- **Idempotency 자연 활용**: queue의 partial unique index가 pending/running 시 중복 차단. Scheduler는 매 tick마다 같은 idemKey로 enqueue 시도 → 이전 것이 끝났으면 새로 들어가고, 아니면 `existed=true`로 skip.
- **Startup behavior**: 등록 직후 첫 tick까지 기다림 (first-fire-delayed). 즉시 실행하려면 `run_on_start: true` 옵션.
- **Config 분리**: `daemon.scheduled_tasks_path` 필드로 별도 YAML 파일 지정. 메인 config.yaml에 task 목록을 섞지 않음 (관심사 분리).

## 3. Deliverables

### 3.1 New Package: `internal/scheduler/`

#### `internal/scheduler/task.go`

```go
package scheduler

import (
    "fmt"
    "time"
)

// ScheduledTask is a single periodic task definition.
type ScheduledTask struct {
    Name        string        // unique identifier, used as idemKey suffix
    Type        string        // "agent" or "research" (maps to daemon.TaskType)
    Prompt      string        // task payload prompt
    Interval    time.Duration // how often to enqueue
    RunOnStart  bool          // if true, fire immediately on daemon start
    Enabled     bool          // if false, skip this task
    SessionID   string        // optional
    Surface     string        // optional (e.g., "scheduler")
}

// Validate returns an error if the task definition is incomplete.
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

#### `internal/scheduler/config.go`

YAML 파일 로딩.

```go
package scheduler

import (
    "fmt"
    "os"
    "time"

    "gopkg.in/yaml.v3"
)

// rawTask is the on-disk YAML shape.
type rawTask struct {
    Name       string `yaml:"name"`
    Type       string `yaml:"type"`
    Prompt     string `yaml:"prompt"`
    Interval   string `yaml:"interval"`    // duration string: "24h", "30m"
    RunOnStart bool   `yaml:"run_on_start"`
    Enabled    *bool  `yaml:"enabled"`      // default: true
    SessionID  string `yaml:"session_id"`
    Surface    string `yaml:"surface"`
}

type rawConfig struct {
    ScheduledTasks []rawTask `yaml:"scheduled_tasks"`
}

// LoadConfig reads a YAML file at path and returns validated tasks.
// Returns nil, nil if the file does not exist (scheduler disabled).
func LoadConfig(path string) ([]ScheduledTask, error)
```

**LoadConfig 로직:**
1. `os.ReadFile(path)` → 파일 없으면 nil, nil 반환 (no error, scheduler 비활성)
2. `yaml.Unmarshal` → rawConfig
3. 각 rawTask를 ScheduledTask로 변환:
   - `time.ParseDuration(raw.Interval)` → Interval
   - `Enabled`가 nil이면 true (기본값)
   - `Validate()` 호출 → 실패 시 에러 반환 (해당 task 이름 포함)
4. Enabled=false인 task는 결과에서 제외 (로그만 출력)
5. 중복 name → 에러
6. 성공 시 []ScheduledTask 반환

#### `internal/scheduler/scheduler.go`

```go
package scheduler

import (
    "context"
    "fmt"
    "log/slog"
    "sync"
    "time"

    "github.com/stello/elnath/internal/daemon"
)

// Enqueuer is the narrow interface the scheduler needs from the daemon queue.
// Implemented by *daemon.Queue.
type Enqueuer interface {
    Enqueue(ctx context.Context, payload string, idemKey string) (int64, bool, error)
}

// Scheduler fires scheduled tasks at their configured intervals.
// Safe to run alongside the daemon worker pool.
type Scheduler struct {
    tasks    []ScheduledTask
    enq      Enqueuer
    logger   *slog.Logger
    now      func() time.Time // injectable for tests
}

// New creates a Scheduler with the given tasks and queue.
func New(tasks []ScheduledTask, enq Enqueuer, logger *slog.Logger) *Scheduler

// Run starts ticker goroutines for each task and blocks until ctx is cancelled.
// Add this goroutine to the daemon's WaitGroup before calling.
func (s *Scheduler) Run(ctx context.Context) error
```

**Run 로직:**
1. `var wg sync.WaitGroup`
2. 각 task에 대해:
   ```go
   wg.Add(1)
   go func(t ScheduledTask) {
       defer wg.Done()
       s.runTask(ctx, t)
   }(task)
   ```
3. `wg.Wait()` → 모든 goroutine 종료 대기
4. return nil

**runTask 로직:**
1. `ticker := time.NewTicker(task.Interval)` — defer stop
2. task.RunOnStart=true → 즉시 한 번 `s.enqueueOnce(ctx, task)` 호출
3. `for { select { case <-ctx.Done(): return; case <-ticker.C: s.enqueueOnce(ctx, task) } }`

**enqueueOnce 로직:**
```go
func (s *Scheduler) enqueueOnce(ctx context.Context, task ScheduledTask) {
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

#### `internal/scheduler/scheduler_test.go`

- **LoadConfig**:
  - 파일 없음 → nil, nil
  - 정상 YAML 2 task → 2개 반환
  - Enabled=false → 결과에서 제외
  - 잘못된 Interval → 에러
  - 중복 name → 에러
  - Invalid type → 에러

- **Task.Validate**:
  - 빈 name → 에러
  - 빈 prompt → 에러
  - Interval < 1m → 에러
  - type "invalid" → 에러
  - 정상 → nil

- **Scheduler.Run** (mock Enqueuer):
  - `RunOnStart=true` → 즉시 enqueue 1번
  - interval 만큼 기다리면 다음 enqueue
  - `ctx.Done()` → 즉시 goroutine 종료
  - `existed=true` 반환 → 로그만, 에러 아님

  테스트에서 interval=1m 은 너무 느리므로, `Validate()` 가 1m 미만을 거부한다면 테스트에서 Interval을 수동으로 짧게 설정 (struct 직접 생성으로 validate 우회). 또는 Validate의 최소값을 낮춰 테스트 가능하게.

  **테스트 전략:** `Scheduler.Run` 을 짧은 interval(예: 50ms) 로 구동하려면 Validate를 건너뛰어야 한다. 방법:
  - Validate의 최소값 체크를 별도 함수로 분리, 테스트에서는 Scheduler를 task slice로 직접 생성 (LoadConfig 우회)
  - 또는 `time.Ticker` 를 mock 가능하도록 추상화 (과도함)
  - **추천:** 테스트 전용 `newTestScheduler` 헬퍼로 ScheduledTask를 직접 생성 + Scheduler.New 호출. Validate는 config loading path에서만 호출됨.

### 3.2 Modified: `internal/config/config.go`

`DaemonConfig`에 필드 추가:

```go
type DaemonConfig struct {
    // ... existing fields ...
    ScheduledTasksPath string `yaml:"scheduled_tasks_path"`
}
```

기본값: `""` (비활성). 사용자가 명시적으로 경로 지정해야 활성화.

### 3.3 Modified: `internal/daemon/daemon.go`

Daemon에 scheduler 필드 + 실행 로직 추가.

```go
type Daemon struct {
    // ... existing fields ...
    scheduler Scheduler // interface
}

// Scheduler abstracts the scheduler to avoid circular import.
type Scheduler interface {
    Run(ctx context.Context) error
}

// WithScheduler attaches a scheduler to run alongside the worker pool.
func (d *Daemon) WithScheduler(s Scheduler) {
    d.scheduler = s
}
```

`Start` 메서드에서 scheduler goroutine 시작:

```go
// ... after worker goroutines are started ...
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

Graceful shutdown은 기존 `d.cancel()` + `d.wg.Wait()` 로 자동 처리됨 (scheduler goroutine이 ctx.Done()에서 종료).

### 3.4 Modified: `cmd/elnath/cmd_daemon.go`

Daemon 생성 후 scheduler 주입:

```go
if cfg.Daemon.ScheduledTasksPath != "" {
    tasks, err := scheduler.LoadConfig(cfg.Daemon.ScheduledTasksPath)
    if err != nil {
        app.Logger.Error("scheduler config load failed", "path", cfg.Daemon.ScheduledTasksPath, "error", err)
        return fmt.Errorf("scheduler: %w", err)
    }
    if len(tasks) > 0 {
        sch := scheduler.New(tasks, queue, app.Logger)
        d.WithScheduler(sch)
        app.Logger.Info("scheduler enabled", "tasks", len(tasks))
    } else {
        app.Logger.Info("scheduler config empty, skipping")
    }
}
```

경로 상대 경로면 `DataDir` 기준으로 resolve.

### 3.5 Example Config File

`~/.elnath/scheduled_tasks.yaml` (사용자가 직접 작성):

```yaml
scheduled_tasks:
  - name: daily-go-research
    type: research
    prompt: "go idiomatic error handling patterns"
    interval: 24h
    run_on_start: false
    enabled: true

  - name: hourly-health-check
    type: agent
    prompt: "Check elnath daemon health: verify all background services, report memory/cpu, flag anomalies"
    interval: 1h
    run_on_start: true
    enabled: false  # 예시로 비활성

  - name: weekly-wiki-audit
    type: agent
    prompt: "Audit wiki/skills/ pages for staleness. Flag any not updated in >30 days"
    interval: 168h
    run_on_start: false
    enabled: true
```

## 4. File Summary

### New Files (4)

| File | LOC (est.) | 역할 |
|------|-----------|------|
| `internal/scheduler/task.go` | ~60 | ScheduledTask struct + Validate |
| `internal/scheduler/config.go` | ~80 | LoadConfig from YAML |
| `internal/scheduler/scheduler.go` | ~100 | Scheduler + Run + ticker goroutines |
| `internal/scheduler/scheduler_test.go` | ~200 | 전체 테스트 |

### Modified Files (3)

| File | 변경 내용 |
|------|----------|
| `internal/config/config.go` | DaemonConfig.ScheduledTasksPath 추가 |
| `internal/daemon/daemon.go` | Scheduler 인터페이스, WithScheduler, Start에서 goroutine 실행 |
| `cmd/elnath/cmd_daemon.go` | scheduler.LoadConfig 호출, daemon에 주입 |

## 5. Acceptance Criteria

- [ ] `go test -race ./internal/scheduler/...` 통과
- [ ] `go test -race ./internal/daemon/... ./cmd/elnath/...` 통과
- [ ] `go vet ./...` 경고 없음
- [ ] `make build` 성공
- [ ] `daemon.scheduled_tasks_path` 미설정 → scheduler 비활성, 기존 daemon 동작 정상
- [ ] 유효한 YAML 경로 설정 → 로그에 "scheduler enabled" 출력
- [ ] RunOnStart=true task → daemon 시작 직후 즉시 enqueue 확인 (queue에 task 존재)
- [ ] 이전 실행이 pending/running 상태 → 다음 tick에서 "skipped (previous run still active)" 로그
- [ ] 완료된 task → 다음 tick에서 정상 enqueue
- [ ] `elnath daemon stop` 또는 Ctrl+C → scheduler goroutine이 정상 종료 (go test에서 goroutine leak 없음)

## 6. Risk

| Risk | Mitigation |
|------|-----------|
| Scheduler가 daemon shutdown 차단 | ctx.Done() select 우선, 모든 goroutine이 `d.wg`에 등록됨 |
| Interval이 너무 짧으면 queue 폭주 | Validate에서 최소 1m 강제 |
| Scheduled task가 계속 실패 → 무한 enqueue | 각 task는 독립. 실패해도 기존 queue의 fail/retry 로직 적용. scheduler는 성공/실패를 신경 쓰지 않음 |
| YAML 파싱 에러로 daemon 시작 실패 | LoadConfig 에러 시 daemon 시작 중단. 사용자가 파일 수정 후 재시작 |
| idemKey 충돌 (사용자가 다른 곳에서 `scheduled:foo` 사용) | `scheduled:` prefix는 scheduler 전용 convention. 사용자 코드에서 이 prefix 쓰지 말 것 (문서화) |
| 시스템 suspend/resume 시 ticker drift | `time.Ticker`는 wall clock 기반. 장시간 suspend 후 resume 시 누락 tick 미보상 (허용) |

## 7. Future Work (Phase E-3+)

- Cron expression 지원 ("0 9 * * MON")
- Scheduled task 히스토리 CLI (`elnath scheduler history`)
- Task 동적 enable/disable (IPC 명령)
- Config hot-reload (SIGHUP)
