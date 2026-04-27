package main

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/tools"
)

type configuredRunnerFake struct {
	mu       sync.Mutex
	runCalls []tools.BashRunRequest
}

func (f *configuredRunnerFake) Name() string { return "configured-fake" }

func (f *configuredRunnerFake) Probe(context.Context) tools.BashRunnerProbe {
	return tools.BashRunnerProbe{
		Name:               f.Name(),
		Available:          true,
		ExecutionMode:      "test_configured",
		PolicyName:         "test",
		FilesystemEnforced: true,
		NetworkEnforced:    true,
		SandboxEnforced:    true,
		Message:            "configured fake runner",
	}
}

func (f *configuredRunnerFake) Run(_ context.Context, req tools.BashRunRequest) (tools.BashRunResult, error) {
	f.mu.Lock()
	f.runCalls = append(f.runCalls, req)
	f.mu.Unlock()
	return tools.BashRunResult{
		Output:         "BASH RESULT\nstatus: success\nconfigured-runner-marker\n",
		Classification: "success",
	}, nil
}

func (f *configuredRunnerFake) Close(context.Context) error { return nil }

func (f *configuredRunnerFake) calls() []tools.BashRunRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]tools.BashRunRequest, len(f.runCalls))
	copy(out, f.runCalls)
	return out
}

func TestSandboxConfigFromConfig_MapsNetworkPolicy(t *testing.T) {
	cfg := &config.Config{
		Sandbox: config.SandboxConfig{
			Mode:             "seatbelt",
			NetworkAllowlist: []string{"github.com:443", "api.github.com:443"},
			NetworkDenylist:  []string{"169.254.169.254:80"},
		},
	}

	got := sandboxConfigFromConfig(cfg)
	if got.Mode != "seatbelt" {
		t.Fatalf("Mode = %q, want seatbelt", got.Mode)
	}
	if strings.Join(got.NetworkAllowlist, ",") != "github.com:443,api.github.com:443" {
		t.Fatalf("NetworkAllowlist = %v", got.NetworkAllowlist)
	}
	if strings.Join(got.NetworkDenylist, ",") != "169.254.169.254:80" {
		t.Fatalf("NetworkDenylist = %v", got.NetworkDenylist)
	}
}

func TestBuildBashRunnerForConfig_DefaultUsesDirectRunner(t *testing.T) {
	runner, err := buildBashRunnerForConfig(config.DefaultConfig())
	if err != nil {
		t.Fatalf("buildBashRunnerForConfig(default): %v", err)
	}
	defer runner.Close(context.Background())
	if runner.Name() != "direct" {
		t.Fatalf("default config runner = %q, want direct", runner.Name())
	}
}

func TestBuildBashRunnerForConfig_UnsupportedSandboxFailsWithoutDirectFallback(t *testing.T) {
	mode := "seatbelt"
	if runtime.GOOS == "darwin" {
		mode = "bwrap"
	}

	runner, err := buildBashRunnerForConfig(&config.Config{
		Sandbox: config.SandboxConfig{Mode: mode},
	})
	if err == nil {
		t.Fatalf("unsupported sandbox mode %q must fail loudly, got runner=%v", mode, runner)
	}
	if runner != nil {
		t.Fatalf("unsupported sandbox mode %q must not return fallback runner %q", mode, runner.Name())
	}
	if !strings.Contains(err.Error(), mode) {
		t.Fatalf("error %q should name requested mode %q", err.Error(), mode)
	}
}

func TestBuildToolRegistry_BashUsesConfiguredRunner(t *testing.T) {
	fake := &configuredRunnerFake{}
	reg := buildToolRegistry(tools.NewPathGuard(t.TempDir(), nil), nil, fake)

	res, err := reg.Execute(
		tools.WithSessionID(context.Background(), "sess-A"),
		"bash",
		json.RawMessage(`{"command":"echo hi"}`),
	)
	if err != nil {
		t.Fatalf("Execute bash: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected bash success, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "configured-runner-marker") {
		t.Fatalf("bash did not use configured runner; output=%q", res.Output)
	}
	if calls := fake.calls(); len(calls) != 1 || calls[0].Command != "echo hi" {
		t.Fatalf("fake runner calls = %+v", calls)
	}
}

func TestBuildToolRegistry_NilRunnerPanicsInsteadOfFallingBack(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("buildToolRegistry with nil runner must panic instead of silently falling back to DirectRunner")
		}
		if !strings.Contains(strings.ToLower(r.(string)), "runner") {
			t.Fatalf("panic should explain missing runner, got %v", r)
		}
	}()

	_ = buildToolRegistry(tools.NewPathGuard(t.TempDir(), nil), nil, nil)
}

func TestBuildToolRegistry_GitUsesSameConfiguredRunner(t *testing.T) {
	fake := &configuredRunnerFake{}
	reg := buildToolRegistry(tools.NewPathGuard(t.TempDir(), nil), nil, fake)
	ctx := tools.WithSessionID(context.Background(), "sess-A")

	if _, err := reg.Execute(ctx, "bash", json.RawMessage(`{"command":"echo hi"}`)); err != nil {
		t.Fatalf("Execute bash: %v", err)
	}
	res, err := reg.Execute(ctx, "git", json.RawMessage(`{"subcommand":"status"}`))
	if err != nil {
		t.Fatalf("Execute git: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected git success, got: %s", res.Output)
	}

	calls := fake.calls()
	if len(calls) != 2 {
		t.Fatalf("shared runner call count = %d, want 2", len(calls))
	}
	if !strings.HasPrefix(calls[1].Command, "git ") {
		t.Fatalf("second configured runner command = %q, want git command", calls[1].Command)
	}
	if !strings.Contains(calls[1].Command, "'status'") {
		t.Fatalf("git command not shell-quoted as expected: %q", calls[1].Command)
	}
}
