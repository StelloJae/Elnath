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

// v42-1b: projectAuditRecords is the platform-agnostic projection that
// substrate runners use to surface permitted-connection events. The
// helper enforces the FIFO retention policy and the "disabled but
// counted" semantics that fall out when maxRetained is 0.

func TestProjectAuditRecords_FIFODropsOverflowAtCap(t *testing.T) {
	const total = 250
	const cap = 200
	decisions := make([]Decision, 0, total)
	for i := 0; i < total; i++ {
		decisions = append(decisions, Decision{
			Allow:    true,
			Source:   SourceNetworkProxy,
			Host:     fmt.Sprintf("h%d.example", i),
			Port:     443,
			Protocol: ProtocolHTTPSConnect,
		})
	}
	records, dropCount := projectAuditRecords(decisions, cap)
	if len(records) != cap {
		t.Errorf("len(records) = %d, want %d", len(records), cap)
	}
	if dropCount != total-cap {
		t.Errorf("dropCount = %d, want %d", dropCount, total-cap)
	}
	if records[0].Host != "h0.example" {
		t.Errorf("FIFO contract broken: records[0].Host = %q, want %q", records[0].Host, "h0.example")
	}
	if records[cap-1].Host != fmt.Sprintf("h%d.example", cap-1) {
		t.Errorf("FIFO contract broken: records[last].Host = %q, want h199.example", records[cap-1].Host)
	}
}

func TestProjectAuditRecords_ZeroCapDisabledButCounted(t *testing.T) {
	decisions := []Decision{
		{Allow: true, Source: SourceNetworkProxy, Host: "a", Port: 1, Protocol: ProtocolHTTPSConnect},
		{Allow: true, Source: SourceNetworkProxy, Host: "b", Port: 2, Protocol: ProtocolHTTPSConnect},
		{Allow: true, Source: SourceNetworkProxy, Host: "c", Port: 3, Protocol: ProtocolHTTPSConnect},
		{Allow: true, Source: SourceNetworkProxy, Host: "d", Port: 4, Protocol: ProtocolHTTPSConnect},
		{Allow: true, Source: SourceNetworkProxy, Host: "e", Port: 5, Protocol: ProtocolHTTPSConnect},
	}
	records, dropCount := projectAuditRecords(decisions, 0)
	if len(records) != 0 {
		t.Errorf("len(records) = %d, want 0 when cap=0", len(records))
	}
	if dropCount != 5 {
		t.Errorf("dropCount = %d, want 5 (disabled-but-counted)", dropCount)
	}
}

// TestProjectAuditRecords_OnlyAllowDecisionsAreProjected pins the
// scope: deny Decisions live in violations, never in audit records.
// A mixed slice must produce records solely from the allow entries.
func TestProjectAuditRecords_OnlyAllowDecisionsAreProjected(t *testing.T) {
	decisions := []Decision{
		{Allow: true, Source: SourceNetworkProxy, Host: "ok.example", Port: 443, Protocol: ProtocolHTTPSConnect},
		{Allow: false, Source: SourceNetworkProxy, Reason: ReasonNotInAllowlist, Host: "blocked.example", Port: 443, Protocol: ProtocolHTTPSConnect},
		{Allow: true, Source: SourceNetworkProxy, Host: "also-ok.example", Port: 443, Protocol: ProtocolHTTPSConnect},
	}
	records, dropCount := projectAuditRecords(decisions, 200)
	if len(records) != 2 {
		t.Errorf("len(records) = %d, want 2 (deny entry must not appear)", len(records))
	}
	if dropCount != 0 {
		t.Errorf("dropCount = %d, want 0", dropCount)
	}
	for _, r := range records {
		if r.Decision != "allow" {
			t.Errorf("record carries non-allow decision %q", r.Decision)
		}
		if strings.Contains(r.Host, "blocked") {
			t.Errorf("deny entry leaked into audit records: %+v", r)
		}
	}
}

// TestProjectAuditRecords_FieldsRespectN6RetentionPolicy pins the
// SandboxAuditRecord shape: only Host/Port/Protocol/Source/Decision
// must appear. The struct itself enforces this (no Path/Reason/Headers
// fields), but the test asserts the resulting record carries the
// expected values from the source Decision.
func TestProjectAuditRecords_FieldsRespectN6RetentionPolicy(t *testing.T) {
	decisions := []Decision{
		{
			Allow:    true,
			Source:   SourceNetworkProxy,
			Host:     "github.com",
			Port:     443,
			Protocol: ProtocolHTTPSConnect,
		},
	}
	records, _ := projectAuditRecords(decisions, 200)
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	r := records[0]
	if r.Host != "github.com" || r.Port != 443 {
		t.Errorf("Host/Port not retained: %+v", r)
	}
	if r.Protocol != string(ProtocolHTTPSConnect) {
		t.Errorf("Protocol = %q, want %q", r.Protocol, string(ProtocolHTTPSConnect))
	}
	if r.Source != string(SourceNetworkProxy) {
		t.Errorf("Source = %q, want %q", r.Source, string(SourceNetworkProxy))
	}
	if r.Decision != "allow" {
		t.Errorf("Decision = %q, want %q", r.Decision, "allow")
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
