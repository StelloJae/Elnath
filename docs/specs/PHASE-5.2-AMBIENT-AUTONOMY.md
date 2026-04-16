# Phase 5.2: Ambient Autonomy

**Status**: Draft
**Date**: 2026-04-16
**Scope**: 1.5 sessions (~1.5 days)
**Predecessor**: Phase 5.1 Magic Docs (PR #8, merged)
**Successors**: 5.3 Self-Improvement Substrate

## 1. Problem Statement

Elnath는 현재 완전히 reactive하다. 사용자가 메시지를 보내야만 에이전트가 동작한다. Daemon이 백그라운드에서 실행 중이어도, 사용자 입력 없이는 아무 일도 하지 않는다.

Ambient Autonomy는 이 패러다임을 전환한다: 사용자가 wiki 페이지에 boot task를 선언하면, Elnath daemon이 자동으로 스케줄에 따라 실행하고, 결과를 wiki에 축적(Magic Docs)하며, 중요한 결과는 Telegram으로 push한다.

### 사용 시나리오

1. **Daily health check**: "매일 오전 9시에 Stella daemon 상태 점검. 이상 시 알림."
2. **Periodic research**: "6시간마다 포트폴리오 관련 뉴스 확인."
3. **Startup tasks**: "Daemon 시작 시 시스템 환경 점검, wiki에 기록."

## 2. Design Decisions

| 결정 | 선택 | 근거 |
|------|------|------|
| Boot task 정의 | wiki/boot/*.md 페이지 | wiki = universal substrate. 사용자가 markdown 페이지 하나 쓰면 자동화 완료 |
| 스케줄러 위치 | Daemon 내장 goroutine | 외부 cron 의존은 자율 비서 UX에 맞지 않음 |
| 스케줄 형식 | `startup` / `every <dur>` / `daily HH:MM` | full cron은 과도. 3가지면 95% 커버. weekly/monthly는 YAGNI |
| 태스크 실행 | 기존 TaskRunner 인터페이스 사용 | daemon의 agent 실행 인프라 재사용. TaskRunnerResult로 결과 획득 |
| 워크플로우 결정 | Orchestrator 자동 라우팅 | boot task 본문이 user message와 동일. 별도 workflow 지정 불필요 |
| 알림 메커니즘 | Scheduler → TaskRunnerResult.Summary → BotClient.SendMessage | Bus Observer 불필요. Scheduler가 결과를 직접 받아서 push |
| wiki 지식 축적 | MagicDocs (기존) | Boot task 실행 중 Bus에 이벤트 발행 → MagicDocs Observer가 자동 캡처 |
| PageType | `PageTypeBootTask = "boot-task"` 상수 추가 | Boot task는 지식 페이지가 아닌 실행 가능한 태스크 정의 |

## 3. Architecture

### 3.1 Component Overview

```
cmd/elnath/cmd_daemon.go
  │ daemon 시작 시:
  │ scanner := ambient.NewScanner(wikiStore)
  │ tasks := scanner.Scan()
  │ sched := ambient.NewScheduler(cfg, tasks)
  │ sched.Start(ctx)
  │ defer sched.Stop()
  │
  ▼
ambient.Scheduler (goroutine)
  │ • startup tasks → 즉시 실행
  │ • interval tasks → time.Ticker
  │ • daily tasks → time.Timer (다음 실행 시각 계산)
  │
  ▼ (스케줄 트리거 시)
  taskRunner.Run(ctx, TaskPayload{Prompt: task.Prompt}, bus)
  │
  ├─▶ Bus → MagicDocs Observer (기존) → wiki 지식 추출
  │
  ▼
  TaskRunnerResult{Summary, Result}
  │
  ├─ silent=true  → log only
  └─ silent=false → notifyFn(title, summary) → Telegram push
```

### 3.2 Dependency Injection

```go
type Config struct {
    Tasks    []BootTask
    Runner   TaskRunFunc
    NotifyFn NotifyFunc
    MagicDocs *magicdocs.MagicDocs  // nil이면 MagicDocs 비활성
    Logger   *slog.Logger
}

type TaskRunFunc func(ctx context.Context, payload daemon.TaskPayload, sink event.Sink) (daemon.TaskRunnerResult, error)

type NotifyFunc func(ctx context.Context, title, body string) error
```

`TaskRunFunc`은 daemon의 `TaskRunner.Run`을 래핑. `NotifyFunc`은 `telegram.BotClient.SendMessage`를 래핑. 테스트에서 둘 다 mock 가능.

### 3.3 Package Structure

```
internal/ambient/
├── types.go          // BootTask, Schedule, ScheduleType
├── scanner.go        // wiki/boot/*.md 스캔 + 파싱
├── scheduler.go      // ticker/timer 기반 스케줄러 + 태스크 실행 + 알림
├── scanner_test.go
├── scheduler_test.go
└── types_test.go     // Schedule 파싱 테스트
```

### 3.4 Package Dependency Direction

```
event (leaf)
wiki (leaf)
  ↑
ambient → wiki (scanner), daemon (TaskPayload/Result), event (Bus), magicdocs (optional)
  ↑
cmd/elnath (wiring)
```

## 4. Boot Task Page Format

### 4.1 Location

`wiki/boot/*.md` — boot 디렉토리 하위의 모든 markdown 파일.

### 4.2 Frontmatter

```yaml
---
title: "Daily Stella Health Check"
type: boot-task
schedule: "daily 09:00"
silent: false
tags: [stella, health]
---

Stella daemon 상태 점검. 4개 orbisd 노드 확인.
최근 거래 성과 체크. 이상 발견 시 상세 보고.
```

| 필드 | 필수 | 설명 |
|------|------|------|
| `title` | Y | 태스크 이름 (알림 제목으로 사용) |
| `type` | Y | 반드시 `boot-task` |
| `schedule` | Y | `startup`, `every <duration>`, `daily HH:MM` |
| `silent` | N | `true`면 알림 안 함 (기본: `false`) |
| `tags` | N | 분류용 태그 |

본문(Content)이 에이전트에게 전달되는 프롬프트.

### 4.3 Schedule 형식

```
startup              → daemon 시작 시 1회 실행
every 30m            → 30분 간격 (time.ParseDuration)
every 6h             → 6시간 간격
daily 09:00          → 매일 오전 9시 (로컬 시간)
daily 22:30          → 매일 오후 10시 30분
```

파싱 로직:
- `"startup"` → ScheduleStartup
- `"every "` prefix → ScheduleInterval, `time.ParseDuration(rest)`
- `"daily "` prefix → ScheduleDaily, `time.Parse("15:04", rest)`

### 4.4 PageType 상수

`internal/wiki/schema.go`에 추가:

```go
PageTypeBootTask PageType = "boot-task"
```

기존 ParseFrontmatter는 PageType을 string으로 저장하므로 호환 문제 없음.

## 5. Scanner

```go
type Scanner struct {
    store *wiki.Store
}

func NewScanner(store *wiki.Store) *Scanner

func (s *Scanner) Scan() ([]BootTask, error)
```

`Scan()`은:
1. `wiki.Store`에서 `boot/` 디렉토리의 모든 페이지를 읽음
2. `type == "boot-task"`인 페이지만 필터
3. `schedule` frontmatter 필드를 파싱하여 `Schedule` 구조체로 변환
4. 파싱 실패한 페이지는 skip + 에러 로그 (반환값에서 제외)
5. `[]BootTask` 반환

```go
type BootTask struct {
    Path     string
    Title    string
    Prompt   string   // page.Content
    Schedule Schedule
    Silent   bool
    Tags     []string
}
```

### 5.1 Schedule 타입

```go
type ScheduleType int

const (
    ScheduleStartup  ScheduleType = iota
    ScheduleInterval
    ScheduleDaily
)

type Schedule struct {
    Type     ScheduleType
    Interval time.Duration // ScheduleInterval
    DailyAt  TimeOfDay     // ScheduleDaily
}

type TimeOfDay struct {
    Hour   int
    Minute int
}

func ParseSchedule(raw string) (Schedule, error)
```

## 6. Scheduler

### 6.1 구현

```go
type Scheduler struct {
    cfg    Config
    cancel context.CancelFunc
    wg     sync.WaitGroup
    logger *slog.Logger
}

func NewScheduler(cfg Config) *Scheduler

func (s *Scheduler) Start(ctx context.Context)
func (s *Scheduler) Stop()
```

### 6.2 Start 동작

`Start(ctx)`는 각 boot task에 대해:

1. **ScheduleStartup** → 즉시 goroutine으로 `executeTask` 호출
2. **ScheduleInterval** → goroutine + `time.Ticker(task.Schedule.Interval)`. 최초 실행은 즉시.
3. **ScheduleDaily** → goroutine + `time.Timer`. 다음 실행 시각(`nextDailyRun`)을 계산. 실행 후 다음 날로 재설정.

```go
func nextDailyRun(now time.Time, tod TimeOfDay) time.Duration {
    target := time.Date(now.Year(), now.Month(), now.Day(), tod.Hour, tod.Minute, 0, 0, now.Location())
    if !target.After(now) {
        target = target.Add(24 * time.Hour)
    }
    return target.Sub(now)
}
```

### 6.3 Task Execution

```go
func (s *Scheduler) executeTask(ctx context.Context, task BootTask) {
    bus := event.NewBus()
    if s.cfg.MagicDocs != nil {
        bus.Subscribe(s.cfg.MagicDocs.Observer())
    }

    payload := daemon.TaskPayload{
        Type:   daemon.TaskTypeAgent,
        Prompt: task.Prompt,
    }

    result, err := s.cfg.Runner(ctx, payload, bus)
    if err != nil {
        s.logger.Error("boot task failed",
            "title", task.Title,
            "path", task.Path,
            "error", err,
        )
        if !task.Silent {
            _ = s.cfg.NotifyFn(ctx, task.Title, "Task failed: "+err.Error())
        }
        return
    }

    s.logger.Info("boot task completed",
        "title", task.Title,
        "path", task.Path,
    )

    if !task.Silent && s.cfg.NotifyFn != nil {
        summary := result.Summary
        if summary == "" {
            summary = result.Result
        }
        if len(summary) > 2000 {
            summary = summary[:2000] + "..."
        }
        _ = s.cfg.NotifyFn(ctx, task.Title, summary)
    }
}
```

### 6.4 Stop

```go
func (s *Scheduler) Stop() {
    if s.cancel != nil {
        s.cancel()
    }
    s.wg.Wait()
    s.logger.Info("ambient scheduler stopped")
}
```

`cancel()`로 모든 goroutine의 ctx가 취소되어 ticker/timer loop가 종료. `wg.Wait()`로 정리 대기.

## 7. Integration

### 7.1 Daemon Wiring

`cmd/elnath/cmd_daemon.go`에서 daemon 시작 시:

```go
if cfg.Ambient.Enabled {
    scanner := ambient.NewScanner(wikiStore)
    tasks, err := scanner.Scan()
    if err != nil {
        logger.Warn("ambient scan failed", "error", err)
    }
    if len(tasks) > 0 {
        var notifyFn ambient.NotifyFunc
        if telegramBot != nil && cfg.Telegram.ChatID != "" {
            chatID := cfg.Telegram.ChatID
            notifyFn = func(ctx context.Context, title, body string) error {
                msg := fmt.Sprintf("🔔 %s\n\n%s", title, body)
                return telegramBot.SendMessage(ctx, chatID, msg)
            }
        }

        sched := ambient.NewScheduler(ambient.Config{
            Tasks:     tasks,
            Runner:    agentTaskRunner,
            NotifyFn:  notifyFn,
            MagicDocs: magicDocsInstance,
            Logger:    logger.With("component", "ambient"),
        })
        sched.Start(ctx)
        app.RegisterCloser("ambient-scheduler", closerFunc(sched.Stop))
    }
}
```

### 7.2 Configuration

`internal/config/config.go`:

```go
type AmbientConfig struct {
    Enabled bool `yaml:"enabled"`
}
```

`Config` struct에 추가:
```go
Ambient AmbientConfig `yaml:"ambient"`
```

기본값: `Enabled: false` (opt-in).

### 7.3 Wiki PageType 상수

`internal/wiki/schema.go`에 추가:
```go
PageTypeBootTask PageType = "boot-task"
```

## 8. Error Handling

### 8.1 핵심 원칙: ambient 실패 ≠ daemon 중단

Boot task 실행 실패, 알림 실패, 스캔 실패 등 어떤 ambient 에러도 daemon의 정상 동작에 영향 없음.

### 8.2 실패 지점별 처리

| 실패 지점 | 처리 |
|-----------|------|
| Scanner: 페이지 파싱 실패 | 해당 페이지 skip + log warning. 나머지 정상 스캔 |
| Scanner: boot 디렉토리 없음 | 빈 []BootTask 반환 (에러 아님) |
| Schedule 파싱 실패 | 해당 boot task skip + log warning |
| TaskRunner 실행 실패 | log error + silent=false면 에러 알림 push |
| Telegram push 실패 | log warning (best-effort) |
| MagicDocs Observer 실패 | Bus의 panic recovery로 격리 (기존 메커니즘) |
| Scheduler goroutine panic | defer recover() + log error |

## 9. Testing Strategy

### 9.1 단위 테스트

| 대상 | 테스트 |
|------|--------|
| `ParseSchedule` | startup, every 30m, every 6h, daily 09:00, daily 22:30, invalid 입력 |
| `Scanner.Scan` | boot-task 페이지 파싱, 비boot-task 필터링, 빈 디렉토리, 파싱 에러 skip |
| `nextDailyRun` | 현재 시각 전/후의 daily 시각에 대한 다음 실행 시각 계산 |
| `Scheduler.executeTask` | 성공 시 알림, silent 시 알림 없음, 실패 시 에러 알림 |

### 9.2 통합 테스트

- **Startup task**: Start → 즉시 실행 → 결과 확인
- **Interval task**: Start → 짧은 interval(10ms) → 2회 이상 실행 확인
- **Stop**: Start → Stop → goroutine 정리 확인
- **Mock TaskRunner + Mock NotifyFn**: 실제 LLM/Telegram 없이 전체 플로우 검증

### 9.3 Race Detection

`go test -race ./internal/ambient/` — 다중 goroutine (각 boot task마다 하나) 동시 실행에서 race 없음 확인.

## 10. Scope Boundaries

### In Scope

- `internal/ambient/` 패키지 신규 (6 files)
- Scanner: wiki/boot/*.md 스캔 + 파싱
- Scheduler: startup/interval/daily 3종 스케줄
- Task 실행: TaskRunner 인터페이스 사용 + Bus → MagicDocs 연동
- 알림: Telegram push (NotifyFunc 추상화)
- `PageTypeBootTask` 상수 추가
- `AmbientConfig` 추가
- `cmd_daemon.go` wiring
- 단위 + 통합 테스트

### Out of Scope

- weekly/monthly 스케줄 — Phase 6+
- cron expression 파싱 — YAGNI
- Boot task CRUD CLI (`elnath boot add/remove`) — 사용자가 wiki 페이지 직접 편집
- Boot task 실행 이력 대시보드 — Phase 6.5
- 피드백 수집 (유용했다/아니다) — Phase 5.3
- Hot reload (wiki 변경 시 자동 재스캔) — Phase 6+. 현재는 daemon 재시작으로 반영
- CLI 모드 지원 — daemon 전용. CLI에서는 boot task 미실행
