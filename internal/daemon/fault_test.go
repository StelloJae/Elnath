package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/fault"
	"github.com/stello/elnath/internal/fault/faulttype"
)

type onceFaultInjector struct {
	triggered atomic.Bool
	scenario  *faulttype.Scenario
}

func (i *onceFaultInjector) Active() bool { return true }

func (i *onceFaultInjector) ShouldFault(s *faulttype.Scenario) bool {
	if s != nil && s.FaultType != faulttype.FaultWorkerPanic {
		return false
	}
	return i.triggered.CompareAndSwap(false, true)
}

func (i *onceFaultInjector) InjectFault(context.Context, *faulttype.Scenario) error { return nil }

func TestDaemonStartFaultGuardRejectsMismatchedConfig(t *testing.T) {
	t.Setenv("ELNATH_FAULT_PROFILE", "tool-bash-transient-fail")
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	d := New(q, filepath.Join(t.TempDir(), "guard.sock"), 1, mockTaskRunner{text: "ok"}.run, nil)
	d.WithFaultGuardConfig(fault.GuardConfig{Enabled: false})
	err = d.Start(context.Background())
	if err == nil {
		t.Fatal("Start() error = nil, want guard failure")
	}
	if !strings.Contains(err.Error(), "fault_injection.enabled=false") {
		t.Fatalf("Start() error = %q, want guard mismatch", err)
	}
}

func TestDaemonWorkerPanicFaultMarksTaskFailedAndKeepsDaemonAlive(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	runner := func(_ context.Context, payload string, _ event.Sink) (TaskResult, error) {
		return TaskResult{Result: payload, Summary: payload}, nil
	}
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("elnath-fault-%d.sock", time.Now().UnixNano()))
	d := New(q, socketPath, 1, runner, nil)
	d.MarkFaultGuardChecked()
	d.WithFaultInjection(&onceFaultInjector{}, &fault.Scenario{Name: "ipc-worker-panic-recover", Category: fault.CategoryIPC, FaultType: fault.FaultWorkerPanic})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- d.Start(ctx)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("daemon did not stop within timeout")
		}
	})

	first := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, "first")})
	if !first.OK {
		t.Fatalf("first submit not OK: %s", first.Err)
	}
	firstTask := pollTaskStatus(t, q, extractTaskID(t, first), StatusFailed, 5*time.Second)
	if !strings.Contains(firstTask.Result, "fault: injected worker panic") {
		t.Fatalf("first task result = %q, want injected worker panic", firstTask.Result)
	}

	second := sendIPC(t, socketPath, IPCRequest{Command: "submit", Payload: mustMarshalString(t, "second")})
	if !second.OK {
		t.Fatalf("second submit not OK: %s", second.Err)
	}
	secondTask := pollTaskStatus(t, q, extractTaskID(t, second), StatusDone, 5*time.Second)
	if secondTask.Result != "second" {
		t.Fatalf("second task result = %q, want second", secondTask.Result)
	}
}
