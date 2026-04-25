package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// captureSlogOutput swaps slog.Default with a buffered TextHandler for the
// duration of the test. Returns the buffer and a restore func.
func captureSlogOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

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

// ---------------------------------------------------------------------------
// B3b-0.5 telemetry + sandbox-mode wiring tests
// ---------------------------------------------------------------------------

func TestBashTool_EmitsTelemetryForDirectRunner(t *testing.T) {
	buf := captureSlogOutput(t)

	root := t.TempDir()
	bt := NewBashTool(NewPathGuard(root, nil))

	ctx := WithSessionID(context.Background(), "sess-A")
	if _, err := bt.Execute(ctx, json.RawMessage(`{"command":"echo hi"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := buf.String()
	required := []string{
		"runner_name=direct",
		"execution_mode=direct_host_guarded",
		"sandbox_enforced=false",
		"policy_name=direct",
		"duration_ms=",
		"classification=",
		"violation_count=0",
		"timed_out=false",
		"canceled=false",
	}
	for _, want := range required {
		if !strings.Contains(out, want) {
			t.Errorf("telemetry missing %q in output:\n%s", want, out)
		}
	}
}

func TestBashTool_TelemetryDoesNotIncludeFullCommand(t *testing.T) {
	buf := captureSlogOutput(t)

	root := t.TempDir()
	bt := NewBashTool(NewPathGuard(root, nil))

	const secretMarker = "TOKEN_VALUE_LEAKED_xyz123abc"
	cmd := fmt.Sprintf("echo %s", secretMarker)
	ctx := WithSessionID(context.Background(), "sess-A")
	payload, _ := json.Marshal(map[string]any{"command": cmd})
	if _, err := bt.Execute(ctx, payload); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, secretMarker) {
		t.Errorf("telemetry must not log full command body (secret leak risk):\n%s", out)
	}
	if !strings.Contains(out, "command_len=") {
		t.Errorf("expected command_len field in telemetry; got:\n%s", out)
	}
}

func TestBashTool_EmitsTelemetryEvenWhenRunnerFails(t *testing.T) {
	buf := captureSlogOutput(t)

	fake := &fakeBashRunner{runErr: errors.New("substrate gone")}
	bt := NewBashToolWithRunner(NewPathGuard(t.TempDir(), nil), fake)

	ctx := WithSessionID(context.Background(), "sess-A")
	if _, err := bt.Execute(ctx, json.RawMessage(`{"command":"echo hi"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "runner_error=") {
		t.Errorf("telemetry missing runner_error field on runner failure:\n%s", out)
	}
	if !strings.Contains(out, "substrate gone") {
		t.Errorf("telemetry missing runner error message:\n%s", out)
	}
}

func TestNewBashRunnerForConfig_DirectMode(t *testing.T) {
	for _, mode := range []string{"", "direct"} {
		runner, err := NewBashRunnerForConfig(SandboxConfig{Mode: mode})
		if err != nil {
			t.Errorf("mode %q: expected success, got %v", mode, err)
			continue
		}
		if runner.Name() != "direct" {
			t.Errorf("mode %q: expected DirectRunner, got %q", mode, runner.Name())
		}
	}
}

func TestNewBashRunnerForConfig_SeatbeltOnCurrentPlatform(t *testing.T) {
	runner, err := NewBashRunnerForConfig(SandboxConfig{Mode: "seatbelt"})
	if runtime.GOOS == "darwin" {
		if err != nil {
			t.Fatalf("expected seatbelt mode to succeed on darwin: %v", err)
		}
		if runner == nil || runner.Name() != "seatbelt" {
			t.Fatalf("expected SeatbeltRunner, got %v", runner)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected unsupported error on %s", runtime.GOOS)
	}
	if runner != nil {
		t.Errorf("expected nil runner on unsupported platform, got %v", runner)
	}
	if !strings.Contains(err.Error(), "darwin") && !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("expected platform-specific error, got: %v", err)
	}
}

// TestNewBashRunnerForConfig_BwrapUnsupported has been superseded by
// TestNewBashRunnerForConfig_BwrapOnCurrentPlatform in
// bash_runner_bwrap_test.go: bwrap is now wired in the factory and
// the diagnostic message depends on platform availability rather than
// a hardcoded "not yet implemented" string.

func TestNewBashRunnerForConfig_UnknownMode(t *testing.T) {
	_, err := NewBashRunnerForConfig(SandboxConfig{Mode: "ferret"})
	if err == nil {
		t.Fatalf("expected error for unknown mode")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("expected 'unknown' in error, got: %v", err)
	}
}

func TestBashSchema_DoesNotExposeSandboxBypass(t *testing.T) {
	bt := NewBashTool(NewPathGuard(t.TempDir(), nil))
	schema := strings.ToLower(string(bt.Schema()))
	forbidden := []string{
		"dangerously_disable_sandbox",
		"disable_sandbox",
		"sandbox_mode",
		"allow_unsandboxed",
		"bypass_sandbox",
	}
	for _, f := range forbidden {
		if strings.Contains(schema, f) {
			t.Errorf("bash schema must not expose %q (LLM bypass forbidden)", f)
		}
	}
}

func TestBashTool_ToolParamsCannotBypassSandbox(t *testing.T) {
	fake := &fakeBashRunner{
		runResult: BashRunResult{Output: "fake-ok", IsError: false, Classification: "success"},
	}
	bt := NewBashToolWithRunner(NewPathGuard(t.TempDir(), nil), fake)

	ctx := WithSessionID(context.Background(), "sess-A")
	// Embed plausible bypass fields. The schema does not know them, so the
	// underlying bashParams Unmarshal silently ignores the extras and the
	// configured runner remains the only execution path.
	payload := json.RawMessage(`{
		"command":"echo hi",
		"dangerously_disable_sandbox":true,
		"sandbox_mode":"none",
		"allow_unsandboxed":true,
		"runner":"escape"
	}`)
	res, err := bt.Execute(ctx, payload)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	fake.mu.Lock()
	calls := len(fake.runCalls)
	fake.mu.Unlock()
	if calls != 1 {
		t.Errorf("runner should still be called exactly once via fixed BashTool field; got %d", calls)
	}
}
