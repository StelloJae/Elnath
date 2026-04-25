package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeBashRunner records each invocation and returns canned results so
// tests can verify BashTool.Execute delegates correctly.
type fakeBashRunner struct {
	mu         sync.Mutex
	probe      BashRunnerProbe
	runErr     error
	runResult  BashRunResult
	runCalls   []BashRunRequest
	closeCalls int
}

func (f *fakeBashRunner) Name() string { return "fake" }

func (f *fakeBashRunner) Probe(_ context.Context) BashRunnerProbe { return f.probe }

func (f *fakeBashRunner) Run(_ context.Context, req BashRunRequest) (BashRunResult, error) {
	f.mu.Lock()
	f.runCalls = append(f.runCalls, req)
	f.mu.Unlock()
	return f.runResult, f.runErr
}

func (f *fakeBashRunner) Close(_ context.Context) error {
	f.mu.Lock()
	f.closeCalls++
	f.mu.Unlock()
	return nil
}

func TestBashTool_DelegatesToRunner(t *testing.T) {
	root := t.TempDir()
	guard := NewPathGuard(root, nil)

	fake := &fakeBashRunner{
		runResult: BashRunResult{
			Output:         "BASH RESULT\nstatus: success\nfake-runner-marker\n",
			IsError:        false,
			Classification: "success",
		},
	}
	bt := NewBashToolWithRunner(guard, fake)

	ctx := WithSessionID(context.Background(), "sess-A")
	res, err := bt.Execute(ctx, json.RawMessage(`{"command":"echo hi"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "fake-runner-marker") {
		t.Fatalf("expected runner-supplied output to surface; got %q", res.Output)
	}

	fake.mu.Lock()
	calls := fake.runCalls
	fake.mu.Unlock()
	if got := len(calls); got != 1 {
		t.Fatalf("runner.Run called %d times, want 1", got)
	}
	req := calls[0]
	if req.Command != "echo hi" {
		t.Errorf("Command = %q, want %q", req.Command, "echo hi")
	}
	if req.SessionDir == "" {
		t.Errorf("SessionDir not populated: %q", req.SessionDir)
	}
	if req.WorkDir == "" {
		t.Errorf("WorkDir not populated: %q", req.WorkDir)
	}
	if req.DisplayCWD == "" {
		t.Errorf("DisplayCWD not populated")
	}
}

func TestBashTool_RunnerErrorSurfacesAsToolError(t *testing.T) {
	root := t.TempDir()
	guard := NewPathGuard(root, nil)

	fake := &fakeBashRunner{
		runErr: errors.New("substrate unavailable"),
	}
	bt := NewBashToolWithRunner(guard, fake)

	ctx := WithSessionID(context.Background(), "sess-A")
	res, err := bt.Execute(ctx, json.RawMessage(`{"command":"echo hi"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError when runner returns error")
	}
	if !strings.Contains(res.Output, "substrate unavailable") {
		t.Errorf("expected runner error message in output, got %q", res.Output)
	}
}

func TestBashTool_RunnerIsErrorPropagates(t *testing.T) {
	root := t.TempDir()
	guard := NewPathGuard(root, nil)

	fake := &fakeBashRunner{
		runResult: BashRunResult{
			Output:         "BASH RESULT\nstatus: timeout\n",
			IsError:        true,
			Classification: "timeout",
		},
	}
	bt := NewBashToolWithRunner(guard, fake)

	ctx := WithSessionID(context.Background(), "sess-A")
	res, err := bt.Execute(ctx, json.RawMessage(`{"command":"sleep 60"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError to propagate from runner result")
	}
	if !strings.Contains(res.Output, "status: timeout") {
		t.Errorf("expected runner output body to surface, got %q", res.Output)
	}
}

func TestDirectRunner_NameAndProbe(t *testing.T) {
	r := NewDirectRunner()
	if r.Name() != "direct" {
		t.Errorf("Name = %q, want %q", r.Name(), "direct")
	}
	p := r.Probe(context.Background())
	if !p.Available {
		t.Errorf("DirectRunner should always be available, got %+v", p)
	}
	if p.Name != "direct" {
		t.Errorf("probe.Name = %q, want %q", p.Name, "direct")
	}
	if p.Platform == "" {
		t.Errorf("probe.Platform must be populated")
	}
	if p.Message == "" {
		t.Errorf("probe.Message should describe the runner")
	}
}

func TestDirectRunner_CloseIsNoop(t *testing.T) {
	r := NewDirectRunner()
	if err := r.Close(context.Background()); err != nil {
		t.Errorf("Close should be no-op, got err: %v", err)
	}
	// Multiple Close calls must remain safe.
	if err := r.Close(context.Background()); err != nil {
		t.Errorf("second Close should also be no-op, got: %v", err)
	}
}

func TestDirectRunner_ImplementsBashRunner(t *testing.T) {
	var _ BashRunner = (*DirectRunner)(nil)
}

func TestBashRunResult_HasViolationsField(t *testing.T) {
	var res BashRunResult
	res.Violations = append(res.Violations, SandboxViolation{Kind: "test"})
	if len(res.Violations) != 1 || res.Violations[0].Kind != "test" {
		t.Errorf("Violations field not assignable as expected")
	}
}
