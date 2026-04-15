package fault

import (
	"bytes"
	"io"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestCheckGuardsFastPathWhenEnvEmpty(t *testing.T) {
	t.Setenv(envFaultProfile, "")
	name, err := CheckGuards(GuardConfig{Enabled: false})
	if err != nil {
		t.Fatalf("CheckGuards() error = %v, want nil", err)
	}
	if name != "" {
		t.Fatalf("CheckGuards() name = %q, want empty", name)
	}
}

func TestCheckGuardsRejectsEnvWithoutConfigEnable(t *testing.T) {
	t.Setenv(envFaultProfile, "tool-bash-transient-fail")
	if _, err := CheckGuards(GuardConfig{Enabled: false}); err == nil {
		t.Fatal("CheckGuards() error = nil, want config mismatch error")
	}
}

func TestCheckGuardsWarnsAndReturnsScenario(t *testing.T) {
	t.Setenv(envFaultProfile, "tool-bash-transient-fail")
	stderr := captureGuardStderr(t, func() {
		name, err := CheckGuards(GuardConfig{Enabled: true})
		if err != nil {
			t.Fatalf("CheckGuards() error = %v, want nil", err)
		}
		if name != "tool-bash-transient-fail" {
			t.Fatalf("CheckGuards() name = %q, want tool-bash-transient-fail", name)
		}
	})
	if !bytes.Contains([]byte(stderr), []byte("FAULT INJECTION ACTIVE")) {
		t.Fatalf("stderr = %q, want active warning", stderr)
	}
}

func TestCheckGuardsAbortsOnInterrupt(t *testing.T) {
	t.Setenv(envFaultProfile, "tool-bash-transient-fail")
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess() error = %v", err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = proc.Signal(syscall.SIGINT)
	}()
	if _, err := CheckGuards(GuardConfig{Enabled: true}); err == nil {
		t.Fatal("CheckGuards() error = nil, want interrupt error")
	}
}

func captureGuardStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()

	fn()
	_ = w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	return string(data)
}
