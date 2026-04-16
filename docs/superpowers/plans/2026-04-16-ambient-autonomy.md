# Ambient Autonomy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a boot task system where wiki pages define scheduled agent tasks that the daemon executes automatically with optional Telegram notifications.

**Architecture:** Scanner reads `wiki/boot/*.md` pages → Scheduler manages goroutines with ticker/timer per task → executes via AgentTaskRunner → pushes results to Telegram if not silent. Runner internally handles Bus + MagicDocs — scheduler stays simple.

**Tech Stack:** Go 1.25+, `internal/wiki` (Store, Schema), `internal/daemon` (TaskResult, AgentTaskRunner), `internal/event` (NopSink), `log/slog`, `time`

**Spec:** `docs/specs/PHASE-5.2-AMBIENT-AUTONOMY.md`

---

## File Structure

| File | Responsibility |
|------|---------------|
| Create: `internal/ambient/types.go` | BootTask, Schedule, TimeOfDay, ScheduleType, ParseSchedule |
| Create: `internal/ambient/types_test.go` | ParseSchedule table-driven tests |
| Create: `internal/ambient/scanner.go` | Read wiki/boot/*.md, filter boot-task pages, extract Extra fields |
| Create: `internal/ambient/scanner_test.go` | Scan with test wiki store, edge cases |
| Create: `internal/ambient/scheduler.go` | Goroutine management, ticker/timer, executeTask, semaphore |
| Create: `internal/ambient/scheduler_test.go` | Startup/interval execution, concurrency limit, notification, stop |
| Modify: `internal/wiki/schema.go:15-21` | Add PageTypeBootTask constant |
| Modify: `internal/config/config.go:13-36` | Add AmbientConfig to Config struct |
| Modify: `cmd/elnath/cmd_daemon.go` | Wire Scanner + Scheduler on daemon startup |

---

### Task 1: Types + ParseSchedule

**Files:**
- Create: `internal/ambient/types.go`
- Create: `internal/ambient/types_test.go`

- [ ] **Step 1: Write the failing test for ParseSchedule**

```go
package ambient

import (
	"testing"
	"time"
)

func TestParseSchedule(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Schedule
		wantErr bool
	}{
		{
			"startup",
			"startup",
			Schedule{Type: ScheduleStartup},
			false,
		},
		{
			"every 30m",
			"every 30m",
			Schedule{Type: ScheduleInterval, Interval: 30 * time.Minute},
			false,
		},
		{
			"every 6h",
			"every 6h",
			Schedule{Type: ScheduleInterval, Interval: 6 * time.Hour},
			false,
		},
		{
			"daily 09:00",
			"daily 09:00",
			Schedule{Type: ScheduleDaily, DailyAt: TimeOfDay{Hour: 9, Minute: 0}},
			false,
		},
		{
			"daily 22:30",
			"daily 22:30",
			Schedule{Type: ScheduleDaily, DailyAt: TimeOfDay{Hour: 22, Minute: 30}},
			false,
		},
		{"empty", "", Schedule{}, true},
		{"unknown", "weekly mon", Schedule{}, true},
		{"bad duration", "every notaduration", Schedule{}, true},
		{"bad time", "daily 25:99", Schedule{}, true},
		{"every no value", "every ", Schedule{}, true},
		{"daily no value", "daily ", Schedule{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSchedule(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseSchedule(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got.Type != tt.want.Type {
					t.Errorf("Type = %d, want %d", got.Type, tt.want.Type)
				}
				if got.Interval != tt.want.Interval {
					t.Errorf("Interval = %v, want %v", got.Interval, tt.want.Interval)
				}
				if got.DailyAt != tt.want.DailyAt {
					t.Errorf("DailyAt = %+v, want %+v", got.DailyAt, tt.want.DailyAt)
				}
			}
		})
	}
}

func TestNextDailyRun(t *testing.T) {
	loc := time.Local

	// 현재 08:00, daily 09:00 → ~1시간 후
	now := time.Date(2026, 4, 16, 8, 0, 0, 0, loc)
	d := nextDailyRun(now, TimeOfDay{Hour: 9, Minute: 0})
	if d != 1*time.Hour {
		t.Errorf("before target: got %v, want 1h", d)
	}

	// 현재 10:00, daily 09:00 → 다음 날 09:00 (~23시간)
	now2 := time.Date(2026, 4, 16, 10, 0, 0, 0, loc)
	d2 := nextDailyRun(now2, TimeOfDay{Hour: 9, Minute: 0})
	expected := time.Date(2026, 4, 17, 9, 0, 0, 0, loc).Sub(now2)
	if d2 != expected {
		t.Errorf("after target: got %v, want %v", d2, expected)
	}

	// 정확히 09:00 → 다음 날
	now3 := time.Date(2026, 4, 16, 9, 0, 0, 0, loc)
	d3 := nextDailyRun(now3, TimeOfDay{Hour: 9, Minute: 0})
	expected3 := time.Date(2026, 4, 17, 9, 0, 0, 0, loc).Sub(now3)
	if d3 != expected3 {
		t.Errorf("exact target: got %v, want %v", d3, expected3)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/ambient/ -run "TestParseSchedule|TestNextDailyRun" -v`
Expected: FAIL — `ParseSchedule` undefined

- [ ] **Step 3: Implement types.go**

```go
package ambient

import (
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/event"

	"github.com/stello/elnath/internal/daemon"
)

// TaskRunFunc matches daemon.AgentTaskRunner signature.
type TaskRunFunc func(ctx context.Context, payload string, sink event.Sink) (daemon.TaskResult, error)

// NotifyFunc sends a notification (e.g. Telegram push).
type NotifyFunc func(ctx context.Context, title, body string) error

// BootTask represents a scheduled agent task defined in a wiki page.
type BootTask struct {
	Path     string
	Title    string
	Prompt   string
	Schedule Schedule
	Silent   bool
	Tags     []string
}

type ScheduleType int

const (
	ScheduleStartup  ScheduleType = iota
	ScheduleInterval
	ScheduleDaily
)

type Schedule struct {
	Type     ScheduleType
	Interval time.Duration
	DailyAt  TimeOfDay
}

type TimeOfDay struct {
	Hour   int
	Minute int
}

func ParseSchedule(raw string) (Schedule, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Schedule{}, fmt.Errorf("empty schedule")
	}

	if s == "startup" {
		return Schedule{Type: ScheduleStartup}, nil
	}

	if strings.HasPrefix(s, "every ") {
		rest := strings.TrimSpace(s[6:])
		if rest == "" {
			return Schedule{}, fmt.Errorf("missing duration after 'every'")
		}
		d, err := time.ParseDuration(rest)
		if err != nil {
			return Schedule{}, fmt.Errorf("invalid duration %q: %w", rest, err)
		}
		return Schedule{Type: ScheduleInterval, Interval: d}, nil
	}

	if strings.HasPrefix(s, "daily ") {
		rest := strings.TrimSpace(s[6:])
		if rest == "" {
			return Schedule{}, fmt.Errorf("missing time after 'daily'")
		}
		t, err := time.Parse("15:04", rest)
		if err != nil {
			return Schedule{}, fmt.Errorf("invalid time %q: %w", rest, err)
		}
		return Schedule{
			Type:    ScheduleDaily,
			DailyAt: TimeOfDay{Hour: t.Hour(), Minute: t.Minute()},
		}, nil
	}

	return Schedule{}, fmt.Errorf("unknown schedule format: %q", s)
}

func nextDailyRun(now time.Time, tod TimeOfDay) time.Duration {
	target := time.Date(now.Year(), now.Month(), now.Day(), tod.Hour, tod.Minute, 0, 0, now.Location())
	if !target.After(now) {
		target = target.AddDate(0, 0, 1)
	}
	return target.Sub(now)
}
```

NOTE: The import of `context`, `daemon`, and `event` may cause "imported and not used" if types.go only defines types. Move `TaskRunFunc` and `NotifyFunc` to types.go only if they compile. Otherwise, move them to scheduler.go and keep types.go import-free. Use your judgment — the key is that it compiles.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/stello/elnath && go test ./internal/ambient/ -run "TestParseSchedule|TestNextDailyRun" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ambient/types.go internal/ambient/types_test.go
git commit -m "feat(ambient): add boot task types and schedule parsing"
```

---

### Task 2: Scanner

**Files:**
- Create: `internal/ambient/scanner.go`
- Create: `internal/ambient/scanner_test.go`

- [ ] **Step 1: Write the failing test**

```go
package ambient

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stello/elnath/internal/wiki"
)

func testWikiStore(t *testing.T) *wiki.Store {
	t.Helper()
	store, err := wiki.NewStore(filepath.Join(t.TempDir(), "wiki"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func TestScanner_ScansBootTasks(t *testing.T) {
	store := testWikiStore(t)

	// Create a boot task page
	err := store.Create(&wiki.Page{
		Path:    "boot/health-check.md",
		Title:   "Health Check",
		Type:    wiki.PageTypeBootTask,
		Content: "Check system health",
		Extra: map[string]any{
			"schedule": "daily 09:00",
			"silent":   false,
		},
		Tags: []string{"health"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Create a non-boot-task page in boot dir (should be filtered)
	err = store.Create(&wiki.Page{
		Path:    "boot/readme.md",
		Title:   "Boot Readme",
		Type:    wiki.PageTypeAnalysis,
		Content: "Not a boot task",
	})
	if err != nil {
		t.Fatalf("Create readme: %v", err)
	}

	// Create a boot-task outside boot dir (should be filtered)
	err = store.Create(&wiki.Page{
		Path:    "analyses/some-analysis.md",
		Title:   "Analysis",
		Type:    wiki.PageTypeAnalysis,
		Content: "Not in boot dir",
	})
	if err != nil {
		t.Fatalf("Create analysis: %v", err)
	}

	scanner := NewScanner(store, slog.Default())
	tasks, err := scanner.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}

	task := tasks[0]
	if task.Title != "Health Check" {
		t.Errorf("Title = %q, want %q", task.Title, "Health Check")
	}
	if task.Prompt != "Check system health\n" {
		t.Errorf("Prompt = %q, want %q", task.Prompt, "Check system health\n")
	}
	if task.Schedule.Type != ScheduleDaily {
		t.Errorf("Schedule.Type = %d, want ScheduleDaily", task.Schedule.Type)
	}
	if task.Schedule.DailyAt.Hour != 9 {
		t.Errorf("DailyAt.Hour = %d, want 9", task.Schedule.DailyAt.Hour)
	}
}

func TestScanner_EmptyBootDir(t *testing.T) {
	store := testWikiStore(t)
	scanner := NewScanner(store, slog.Default())
	tasks, err := scanner.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("got %d tasks, want 0", len(tasks))
	}
}

func TestScanner_InvalidScheduleSkipped(t *testing.T) {
	store := testWikiStore(t)

	store.Create(&wiki.Page{
		Path:    "boot/bad-schedule.md",
		Title:   "Bad Schedule",
		Type:    wiki.PageTypeBootTask,
		Content: "Some task",
		Extra: map[string]any{
			"schedule": "weekly mon",
		},
	})
	store.Create(&wiki.Page{
		Path:    "boot/good-task.md",
		Title:   "Good Task",
		Type:    wiki.PageTypeBootTask,
		Content: "Good task",
		Extra: map[string]any{
			"schedule": "startup",
		},
	})

	scanner := NewScanner(store, slog.Default())
	tasks, _ := scanner.Scan()
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1 (bad schedule should be skipped)", len(tasks))
	}
	if tasks[0].Title != "Good Task" {
		t.Errorf("Title = %q, want Good Task", tasks[0].Title)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/ambient/ -run "TestScanner" -v`
Expected: FAIL — `NewScanner` undefined

- [ ] **Step 3: Implement scanner.go**

```go
package ambient

import (
	"log/slog"
	"strings"

	"github.com/stello/elnath/internal/wiki"
)

type Scanner struct {
	store  *wiki.Store
	logger *slog.Logger
}

func NewScanner(store *wiki.Store, logger *slog.Logger) *Scanner {
	return &Scanner{store: store, logger: logger}
}

func (s *Scanner) Scan() ([]BootTask, error) {
	pages, err := s.store.List()
	if err != nil {
		return nil, err
	}

	var tasks []BootTask
	for _, page := range pages {
		if !strings.HasPrefix(page.Path, "boot/") {
			continue
		}
		if page.Type != wiki.PageTypeBootTask {
			continue
		}

		scheduleRaw, _ := page.Extra["schedule"].(string)
		if scheduleRaw == "" {
			s.logger.Warn("boot task missing schedule", "path", page.Path)
			continue
		}

		schedule, err := ParseSchedule(scheduleRaw)
		if err != nil {
			s.logger.Warn("boot task invalid schedule",
				"path", page.Path,
				"schedule", scheduleRaw,
				"error", err,
			)
			continue
		}

		silent, _ := page.Extra["silent"].(bool)

		tasks = append(tasks, BootTask{
			Path:     page.Path,
			Title:    page.Title,
			Prompt:   page.Content,
			Schedule: schedule,
			Silent:   silent,
			Tags:     page.Tags,
		})
	}

	return tasks, nil
}
```

NOTE: `wiki.Store.List()` returns `([]*wiki.Page, error)`. Check the actual return type — it might be `[]*Page` or `[]Page`. Also check if `wiki.PageTypeBootTask` exists yet — if not, you'll add it in Task 4. For now, you can define a local constant `const bootTaskType wiki.PageType = "boot-task"` or add the constant to wiki/schema.go first. Use your judgment to make things compile.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/stello/elnath && go test ./internal/ambient/ -run "TestScanner" -v`
Expected: PASS

- [ ] **Step 5: Run all package tests + race**

Run: `cd /Users/stello/elnath && go test -race ./internal/ambient/ -v`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add internal/ambient/scanner.go internal/ambient/scanner_test.go
git commit -m "feat(ambient): add boot task scanner for wiki/boot/*.md"
```

---

### Task 3: Scheduler

**Files:**
- Create: `internal/ambient/scheduler.go`
- Create: `internal/ambient/scheduler_test.go`

- [ ] **Step 1: Write the failing test**

```go
package ambient

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/event"
)

func TestScheduler_StartupTask(t *testing.T) {
	var called atomic.Int32
	runner := func(ctx context.Context, payload string, sink event.Sink) (daemon.TaskResult, error) {
		called.Add(1)
		return daemon.TaskResult{Summary: "done"}, nil
	}

	sched := NewScheduler(Config{
		Tasks: []BootTask{{
			Title:    "startup test",
			Prompt:   "do something",
			Schedule: Schedule{Type: ScheduleStartup},
		}},
		Runner:        runner,
		MaxConcurrent: 2,
		Logger:        slog.Default(),
	})

	ctx := context.Background()
	sched.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	sched.Stop()

	if c := called.Load(); c != 1 {
		t.Errorf("startup task called %d times, want 1", c)
	}
}

func TestScheduler_IntervalTask(t *testing.T) {
	var called atomic.Int32
	runner := func(ctx context.Context, payload string, sink event.Sink) (daemon.TaskResult, error) {
		called.Add(1)
		return daemon.TaskResult{Summary: "ok"}, nil
	}

	sched := NewScheduler(Config{
		Tasks: []BootTask{{
			Title:    "interval test",
			Prompt:   "check",
			Schedule: Schedule{Type: ScheduleInterval, Interval: 20 * time.Millisecond},
		}},
		Runner:        runner,
		MaxConcurrent: 2,
		Logger:        slog.Default(),
	})

	ctx := context.Background()
	sched.Start(ctx)
	time.Sleep(80 * time.Millisecond)
	sched.Stop()

	c := called.Load()
	if c < 2 {
		t.Errorf("interval task called %d times, want >= 2", c)
	}
}

func TestScheduler_NotifiesOnCompletion(t *testing.T) {
	var mu sync.Mutex
	var notifications []string

	runner := func(ctx context.Context, payload string, sink event.Sink) (daemon.TaskResult, error) {
		return daemon.TaskResult{Summary: "all good"}, nil
	}
	notifyFn := func(ctx context.Context, title, body string) error {
		mu.Lock()
		notifications = append(notifications, title+": "+body)
		mu.Unlock()
		return nil
	}

	sched := NewScheduler(Config{
		Tasks: []BootTask{{
			Title:    "notify test",
			Prompt:   "check",
			Schedule: Schedule{Type: ScheduleStartup},
			Silent:   false,
		}},
		Runner:        runner,
		NotifyFn:      notifyFn,
		MaxConcurrent: 2,
		Logger:        slog.Default(),
	})

	ctx := context.Background()
	sched.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	sched.Stop()

	mu.Lock()
	defer mu.Unlock()
	if len(notifications) != 1 {
		t.Fatalf("got %d notifications, want 1", len(notifications))
	}
	if notifications[0] != "notify test: all good" {
		t.Errorf("notification = %q", notifications[0])
	}
}

func TestScheduler_SilentNoNotify(t *testing.T) {
	var notified atomic.Int32

	runner := func(ctx context.Context, payload string, sink event.Sink) (daemon.TaskResult, error) {
		return daemon.TaskResult{Summary: "done"}, nil
	}
	notifyFn := func(ctx context.Context, title, body string) error {
		notified.Add(1)
		return nil
	}

	sched := NewScheduler(Config{
		Tasks: []BootTask{{
			Title:    "silent test",
			Prompt:   "check",
			Schedule: Schedule{Type: ScheduleStartup},
			Silent:   true,
		}},
		Runner:        runner,
		NotifyFn:      notifyFn,
		MaxConcurrent: 2,
		Logger:        slog.Default(),
	})

	ctx := context.Background()
	sched.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	sched.Stop()

	if n := notified.Load(); n != 0 {
		t.Errorf("silent task notified %d times, want 0", n)
	}
}

func TestScheduler_ConcurrencyLimit(t *testing.T) {
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	runner := func(ctx context.Context, payload string, sink event.Sink) (daemon.TaskResult, error) {
		c := concurrent.Add(1)
		for {
			old := maxConcurrent.Load()
			if c <= old || maxConcurrent.CompareAndSwap(old, c) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		concurrent.Add(-1)
		return daemon.TaskResult{Summary: "done"}, nil
	}

	tasks := make([]BootTask, 5)
	for i := range tasks {
		tasks[i] = BootTask{
			Title:    "concurrent",
			Prompt:   "work",
			Schedule: Schedule{Type: ScheduleStartup},
			Silent:   true,
		}
	}

	sched := NewScheduler(Config{
		Tasks:         tasks,
		Runner:        runner,
		MaxConcurrent: 2,
		Logger:        slog.Default(),
	})

	ctx := context.Background()
	sched.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	sched.Stop()

	if mc := maxConcurrent.Load(); mc > 2 {
		t.Errorf("max concurrent = %d, want <= 2", mc)
	}
}

func TestScheduler_StopGraceful(t *testing.T) {
	runner := func(ctx context.Context, payload string, sink event.Sink) (daemon.TaskResult, error) {
		select {
		case <-ctx.Done():
			return daemon.TaskResult{}, ctx.Err()
		case <-time.After(10 * time.Second):
			return daemon.TaskResult{Summary: "done"}, nil
		}
	}

	sched := NewScheduler(Config{
		Tasks: []BootTask{{
			Title:    "long task",
			Prompt:   "work",
			Schedule: Schedule{Type: ScheduleInterval, Interval: 10 * time.Millisecond},
			Silent:   true,
		}},
		Runner:        runner,
		MaxConcurrent: 2,
		Logger:        slog.Default(),
	})

	ctx := context.Background()
	sched.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		sched.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK — stopped within reasonable time
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return within 5s")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/stello/elnath && go test ./internal/ambient/ -run "TestScheduler" -v`
Expected: FAIL — `NewScheduler` undefined

- [ ] **Step 3: Implement scheduler.go**

```go
package ambient

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/stello/elnath/internal/event"
)

const defaultMaxConcurrent = 2

type Config struct {
	Tasks         []BootTask
	Runner        TaskRunFunc
	NotifyFn      NotifyFunc
	MaxConcurrent int
	Logger        *slog.Logger
}

type Scheduler struct {
	cfg    Config
	cancel context.CancelFunc
	wg     sync.WaitGroup
	sem    chan struct{}
	logger *slog.Logger
}

func NewScheduler(cfg Config) *Scheduler {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = defaultMaxConcurrent
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Scheduler{
		cfg:    cfg,
		sem:    make(chan struct{}, cfg.MaxConcurrent),
		logger: cfg.Logger,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	schedCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	for _, task := range s.cfg.Tasks {
		switch task.Schedule.Type {
		case ScheduleStartup:
			s.wg.Add(1)
			go func(t BootTask) {
				defer s.wg.Done()
				s.executeTask(schedCtx, t)
			}(task)

		case ScheduleInterval:
			s.wg.Add(1)
			go func(t BootTask) {
				defer s.wg.Done()
				s.executeTask(schedCtx, t)
				ticker := time.NewTicker(t.Schedule.Interval)
				defer ticker.Stop()
				for {
					select {
					case <-schedCtx.Done():
						return
					case <-ticker.C:
						s.executeTask(schedCtx, t)
					}
				}
			}(task)

		case ScheduleDaily:
			s.wg.Add(1)
			go func(t BootTask) {
				defer s.wg.Done()
				for {
					delay := nextDailyRun(time.Now(), t.Schedule.DailyAt)
					timer := time.NewTimer(delay)
					select {
					case <-schedCtx.Done():
						timer.Stop()
						return
					case <-timer.C:
						s.executeTask(schedCtx, t)
					}
				}
			}(task)
		}
	}

	s.logger.Info("ambient scheduler started", "tasks", len(s.cfg.Tasks))
}

func (s *Scheduler) executeTask(ctx context.Context, task BootTask) {
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-s.sem }()

	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("boot task panic", "title", task.Title, "recover", r)
		}
	}()

	result, err := s.cfg.Runner(ctx, task.Prompt, event.NopSink{})
	if err != nil {
		s.logger.Error("boot task failed",
			"title", task.Title,
			"path", task.Path,
			"error", err,
		)
		if !task.Silent && s.cfg.NotifyFn != nil {
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

func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	s.logger.Info("ambient scheduler stopped")
}
```

NOTE: `TaskRunFunc` and `NotifyFunc` may already be defined in types.go. If they cause "already declared" errors, remove duplicates. Also check that `daemon.TaskResult` has `Summary` and `Result` fields (it does — confirmed in red team). Ensure imports compile: `context`, `event` (for NopSink).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/stello/elnath && go test -race ./internal/ambient/ -v -timeout 30s`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ambient/scheduler.go internal/ambient/scheduler_test.go
git commit -m "feat(ambient): add scheduler with startup/interval/daily support"
```

---

### Task 4: Config + PageType

**Files:**
- Modify: `internal/wiki/schema.go:15-21`
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add PageTypeBootTask to wiki/schema.go**

After the existing PageType constants, add:

```go
PageTypeBootTask PageType = "boot-task"
```

- [ ] **Step 2: Add AmbientConfig to config.go**

Add struct after `LLMExtractionConfig` (or `MagicDocsConfig`):

```go
type AmbientConfig struct {
	Enabled       bool `yaml:"enabled"`
	MaxConcurrent int  `yaml:"max_concurrent"`
}
```

Add field to `Config` struct:

```go
Ambient       AmbientConfig       `yaml:"ambient"`
```

- [ ] **Step 3: Verify it compiles**

Run: `cd /Users/stello/elnath && go build ./internal/wiki/ && go build ./internal/config/`
Expected: no errors

- [ ] **Step 4: Run existing tests**

Run: `cd /Users/stello/elnath && go test ./internal/wiki/ && go test ./internal/config/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/wiki/schema.go internal/config/config.go
git commit -m "feat: add PageTypeBootTask and AmbientConfig"
```

---

### Task 5: Daemon Wiring

**Files:**
- Modify: `cmd/elnath/cmd_daemon.go`

- [ ] **Step 1: Read cmd_daemon.go to find insertion point**

Read the file to find:
1. Where the daemon starts (after DB, provider, wiki store setup)
2. Where `agentTaskRunner` is created/available
3. Where `telegramBot` is available (if Telegram is configured)
4. The `ctx` and `app` variables in scope

- [ ] **Step 2: Add ambient import and wiring**

Add import: `"github.com/stello/elnath/internal/ambient"`

After daemon setup but before the main loop/serve, add:

```go
if cfg.Ambient.Enabled {
    ambientScanner := ambient.NewScanner(wikiStore, app.Logger.With("component", "ambient-scanner"))
    bootTasks, scanErr := ambientScanner.Scan()
    if scanErr != nil {
        app.Logger.Warn("ambient scan failed", "error", scanErr)
    }
    if len(bootTasks) > 0 {
        var notifyFn ambient.NotifyFunc
        // Wire Telegram notification if bot is available
        // Check how telegramBot and chatID are available in this scope
        // If available:
        // notifyFn = func(ctx context.Context, title, body string) error {
        //     msg := fmt.Sprintf("🔔 %s\n\n%s", title, body)
        //     return telegramBot.SendMessage(ctx, chatID, msg)
        // }

        maxConc := cfg.Ambient.MaxConcurrent
        if maxConc <= 0 {
            maxConc = 2
        }

        ambientSched := ambient.NewScheduler(ambient.Config{
            Tasks:         bootTasks,
            Runner:        agentTaskRunner,  // or however the runner is named in this scope
            NotifyFn:      notifyFn,
            MaxConcurrent: maxConc,
            Logger:        app.Logger.With("component", "ambient"),
        })
        ambientSched.Start(ctx)
        defer ambientSched.Stop()
        app.Logger.Info("ambient scheduler active", "boot_tasks", len(bootTasks))
    }
}
```

IMPORTANT: You MUST read `cmd_daemon.go` first to find the actual variable names for: the wiki store, the agent task runner, the Telegram bot, and the context. The pseudocode above uses placeholder names — adapt to the actual code.

- [ ] **Step 3: Verify it compiles**

Run: `cd /Users/stello/elnath && go build ./cmd/elnath/`
Expected: no errors

- [ ] **Step 4: Run daemon tests**

Run: `cd /Users/stello/elnath && go test ./cmd/elnath/ -timeout 60s`
Expected: PASS (ambient disabled by default)

- [ ] **Step 5: Commit**

```bash
git add cmd/elnath/cmd_daemon.go
git commit -m "feat(daemon): wire ambient scheduler on startup"
```

---

### Task 6: Final Verification

- [ ] **Step 1: Run full project test suite**

Run: `cd /Users/stello/elnath && make test`
Expected: ALL PASS

- [ ] **Step 2: Run lint**

Run: `cd /Users/stello/elnath && make lint`
Expected: clean

- [ ] **Step 3: Check ambient test coverage**

Run: `cd /Users/stello/elnath && go test -cover ./internal/ambient/`
Expected: ≥80% coverage

- [ ] **Step 4: Verify file structure**

Run: `find /Users/stello/elnath/internal/ambient -name "*.go" | sort`
Expected:
```
internal/ambient/scanner.go
internal/ambient/scanner_test.go
internal/ambient/scheduler.go
internal/ambient/scheduler_test.go
internal/ambient/types.go
internal/ambient/types_test.go
```

- [ ] **Step 5: Final commit if needed**

```bash
git commit -m "feat: Phase 5.2 Ambient Autonomy — boot task scheduler with wiki-defined proactive agents"
```
