package scheduler

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stello/elnath/internal/daemon"
)

type mockEnq struct {
	mu       sync.Mutex
	calls    []string
	payloads []string
	err      error
	existed  bool
}

func (m *mockEnq) Enqueue(_ context.Context, payload, idemKey string) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, idemKey)
	m.payloads = append(m.payloads, payload)
	return int64(len(m.calls)), m.existed, m.err
}

func (m *mockEnq) snapshot() ([]string, []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	calls := append([]string(nil), m.calls...)
	payloads := append([]string(nil), m.payloads...)
	return calls, payloads
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitForCallCount(t *testing.T, enq *mockEnq, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		calls, _ := enq.snapshot()
		if len(calls) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls, _ := enq.snapshot()
	t.Fatalf("calls = %d after %s, want at least %d", len(calls), timeout, want)
}

func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scheduled_tasks.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestScheduledTaskValidate(t *testing.T) {
	tests := []struct {
		name    string
		task    ScheduledTask
		wantErr string
	}{
		{
			name:    "missing name",
			task:    ScheduledTask{Prompt: "hello", Interval: time.Minute},
			wantErr: "scheduled task name required",
		},
		{
			name:    "missing prompt",
			task:    ScheduledTask{Name: "hello", Interval: time.Minute},
			wantErr: "prompt required",
		},
		{
			name:    "whitespace name",
			task:    ScheduledTask{Name: "   ", Prompt: "hello", Interval: time.Minute},
			wantErr: "scheduled task name required",
		},
		{
			name:    "whitespace prompt",
			task:    ScheduledTask{Name: "hello", Prompt: "   ", Interval: time.Minute},
			wantErr: "prompt required",
		},
		{
			name:    "interval too short",
			task:    ScheduledTask{Name: "hello", Prompt: "go", Interval: 30 * time.Second},
			wantErr: "interval must be >= 1m",
		},
		{
			name:    "invalid type",
			task:    ScheduledTask{Name: "hello", Prompt: "go", Interval: time.Minute, Type: "foo"},
			wantErr: "invalid type",
		},
		{
			name: "empty type defaults to agent",
			task: ScheduledTask{Name: "hello", Prompt: "go", Interval: time.Minute},
		},
		{
			name: "valid task",
			task: ScheduledTask{Name: "hello", Prompt: "go", Interval: time.Hour, Type: "research", RunOnStart: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.task.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() error = nil, want %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadConfigMissingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	tasks, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if tasks != nil {
		t.Fatalf("LoadConfig() tasks = %#v, want nil", tasks)
	}
}

func TestLoadConfigEmptyFile(t *testing.T) {
	path := writeConfigFile(t, "")
	tasks, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if tasks != nil {
		t.Fatalf("LoadConfig() tasks = %#v, want nil", tasks)
	}
}

func TestLoadConfigValidYAML(t *testing.T) {
	path := writeConfigFile(t, `scheduled_tasks:
  - name: task1
    prompt: hello
    interval: 1h
  - name: task2
    type: research
    prompt: go patterns
    interval: 24h
    run_on_start: true
`)

	tasks, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(tasks))
	}
	if tasks[0].Name != "task1" || tasks[0].Prompt != "hello" || tasks[0].Interval != time.Hour {
		t.Fatalf("tasks[0] = %+v", tasks[0])
	}
	if tasks[1].Name != "task2" || tasks[1].Type != "research" || tasks[1].Prompt != "go patterns" || tasks[1].Interval != 24*time.Hour || !tasks[1].RunOnStart {
		t.Fatalf("tasks[1] = %+v", tasks[1])
	}
}

func TestLoadConfigSkipsDisabledTasks(t *testing.T) {
	path := writeConfigFile(t, `scheduled_tasks:
  - name: task1
    prompt: hello
    interval: 1h
    enabled: false
  - name: task2
    prompt: world
    interval: 2h
`)

	tasks, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	if tasks[0].Name != "task2" {
		t.Fatalf("tasks[0].Name = %q, want task2", tasks[0].Name)
	}
}

func TestLoadConfigRejectsInvalidInterval(t *testing.T) {
	path := writeConfigFile(t, `scheduled_tasks:
  - name: task1
    prompt: hello
    interval: nope
`)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want parse interval error")
	}
	if !strings.Contains(err.Error(), "parse interval") {
		t.Fatalf("LoadConfig() error = %q, want parse interval", err.Error())
	}
}

func TestLoadConfigRejectsDuplicateNames(t *testing.T) {
	path := writeConfigFile(t, `scheduled_tasks:
  - name: task1
    prompt: hello
    interval: 1h
  - name: task1
    prompt: world
    interval: 2h
`)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want duplicate name error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("LoadConfig() error = %q, want duplicate", err.Error())
	}
}

func TestLoadConfigRejectsInvalidType(t *testing.T) {
	path := writeConfigFile(t, `scheduled_tasks:
  - name: task1
    type: invalid
    prompt: hello
    interval: 1h
`)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want invalid type error")
	}
	if !strings.Contains(err.Error(), "invalid type") {
		t.Fatalf("LoadConfig() error = %q, want invalid type", err.Error())
	}
}

func TestLoadConfigAllDisabledReturnsEmpty(t *testing.T) {
	path := writeConfigFile(t, `scheduled_tasks:
  - name: task1
    prompt: hello
    interval: 1h
    enabled: false
`)

	tasks, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("len(tasks) = %d, want 0", len(tasks))
	}
}

func TestLoadConfigIgnoresDisabledTasksBeforeValidation(t *testing.T) {
	path := writeConfigFile(t, `scheduled_tasks:
  - name: task1
    prompt: hello
    interval: nope
    enabled: false
  - name: task1
    prompt: world
    interval: 1h
`)

	tasks, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	if tasks[0].Name != "task1" || tasks[0].Prompt != "world" {
		t.Fatalf("tasks[0] = %+v", tasks[0])
	}
}

func TestSchedulerRunOnStartEnqueuesImmediately(t *testing.T) {
	enq := &mockEnq{}
	s := New([]ScheduledTask{{Name: "task1", Prompt: "hello", Interval: 10 * time.Millisecond, RunOnStart: true}}, enq, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx)
	}()

	waitForCallCount(t, enq, 1, 200*time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run() did not return after cancel")
	}
}

func TestSchedulerRunStopsAfterCancel(t *testing.T) {
	enq := &mockEnq{}
	s := New([]ScheduledTask{{Name: "task1", Prompt: "hello", Interval: 10 * time.Millisecond}}, enq, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run() did not return after cancel")
	}
}

func TestSchedulerEnqueueOnceHandlesExistingTask(t *testing.T) {
	enq := &mockEnq{existed: true}
	s := New(nil, enq, discardLogger())

	s.enqueueOnce(context.Background(), ScheduledTask{Name: "task1", Prompt: "hello", Interval: time.Minute})

	calls, _ := enq.snapshot()
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}
	if calls[0] != "scheduled:task1" {
		t.Fatalf("calls[0] = %q, want scheduled:task1", calls[0])
	}
}

func TestSchedulerEnqueueOnceMapsTaskTypes(t *testing.T) {
	tests := []struct {
		name     string
		taskType string
		wantType daemon.TaskType
	}{
		{name: "default agent", wantType: daemon.TaskTypeAgent},
		{name: "explicit agent", taskType: "agent", wantType: daemon.TaskTypeAgent},
		{name: "research", taskType: "research", wantType: daemon.TaskTypeResearch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enq := &mockEnq{}
			s := New(nil, enq, discardLogger())

			s.enqueueOnce(context.Background(), ScheduledTask{
				Name:      "task1",
				Type:      tt.taskType,
				Prompt:    "hello",
				Interval:  time.Minute,
				SessionID: "sess-1",
				Surface:   "daemon",
			})

			calls, payloads := enq.snapshot()
			if len(calls) != 1 || len(payloads) != 1 {
				t.Fatalf("calls = %d payloads = %d, want 1 each", len(calls), len(payloads))
			}
			if calls[0] != "scheduled:task1" {
				t.Fatalf("idemKey = %q, want scheduled:task1", calls[0])
			}
			payload := daemon.ParseTaskPayload(payloads[0])
			if payload.Type != tt.wantType {
				t.Fatalf("payload.Type = %q, want %q", payload.Type, tt.wantType)
			}
			if payload.Prompt != "hello" || payload.SessionID != "sess-1" || payload.Surface != "daemon" {
				t.Fatalf("payload = %+v", payload)
			}
		})
	}
}
