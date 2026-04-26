//go:build linux

package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// B3b-4-3 Linux bwrap proxy wiring — UNIT tests for the factory +
// BwrapRunner construction surface. These tests do not require bwrap
// or a built elnath binary; they exercise the construction-time
// rejection path and the cross-platform shape of the runner type.
//
// Integration tests live in bash_runner_bwrap_proxy_integration_linux_test.go
// behind the `linux && integration` tag.

// TestBwrapFactory_DomainAllowlistAcceptedAfterB3b43 pins the
// post-B3b-4-3 happy path: a domain allowlist on bwrap returns a
// BwrapRunner with the allowlist captured. The factory used to reject
// every non-empty bwrap allowlist (B3b-4-1); B3b-4-3 wires the
// netproxy substrate so the same config now constructs a working
// runner.
//
// On hosts without bwrap available the runner construction will fail
// at the netproxy spawn (no elnath binary to self-exec); the test
// only asserts that the factory does NOT pre-reject with the legacy
// "B3b-4-3 lane not available" wording. Integration test coverage of
// the spawn is in the integration file.
func TestBwrapFactory_DomainAllowlistAcceptedAfterB3b43(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap factory is linux-only")
	}
	_, err := NewBashRunnerForConfig(SandboxConfig{
		Mode:             "bwrap",
		NetworkAllowlist: []string{"github.com:443"},
	})
	if err == nil {
		// Construction may have succeeded if the test machine has
		// bwrap + a buildable elnath binary on PATH; that is the
		// happy path and we accept it without further assertion
		// because integration tests own the substrate behavior.
		return
	}
	// When construction fails, the failure MUST NOT be the legacy
	// pre-B3b-4-3 wording. Allow netproxy-related setup errors (which
	// are the actual B3b-4-3 substrate failure modes).
	msg := err.Error()
	for _, forbidden := range []string{
		"Bwrap proxy wiring is not available in this lane yet",
		"B3b-4-3 Linux). ",
	} {
		if strings.Contains(msg, forbidden) {
			t.Errorf("factory still emits pre-B3b-4-3 rejection wording %q; got: %v", forbidden, err)
		}
	}
}

// TestBwrapFactory_NonLoopbackIPAllowlistAcceptedAfterB3b43 asserts
// the same contract for explicit IP entries. Pre-B3b-4-3 the factory
// rejected even single non-loopback IP entries; post-B3b-4-3 they
// flow through the netproxy substrate.
func TestBwrapFactory_NonLoopbackIPAllowlistAcceptedAfterB3b43(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap factory is linux-only")
	}
	_, err := NewBashRunnerForConfig(SandboxConfig{
		Mode:             "bwrap",
		NetworkAllowlist: []string{"10.0.0.5:5432"},
	})
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "Bwrap proxy wiring is not available") {
		t.Errorf("factory still emits pre-B3b-4-3 rejection wording; got: %v", err)
	}
}

// TestBwrapFactory_LoopbackOnlyAllowlistRejectedAfterB3b43 pins the
// post-B3b-4-3 contract for loopback entries on bwrap: still
// rejected, because bwrap has no SBPL-equivalent loopback rule and
// the netns blocks all egress (including loopback to a host
// listener).
func TestBwrapFactory_LoopbackOnlyAllowlistRejectedAfterB3b43(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap factory is linux-only")
	}
	_, err := NewBashRunnerForConfig(SandboxConfig{
		Mode:             "bwrap",
		NetworkAllowlist: []string{"127.0.0.1:8080"},
	})
	if err == nil {
		t.Fatal("loopback-only allowlist on bwrap MUST be rejected")
	}
	for _, want := range []string{"loopback", "127.0.0.1:8080", "restart"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("loopback rejection missing %q; got: %v", want, err)
		}
	}
}

// TestBwrapRunner_DefaultDenyConstructorDoesNotSpawnProxyChild
// preserves the resource-frugality invariant: empty allowlist means
// no proxy is needed and no child is spawned. The bwrap default-deny
// from --unshare-net handles policy.
func TestBwrapRunner_DefaultDenyConstructorDoesNotSpawnProxyChild(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap runner is linux-only")
	}
	r, err := NewBwrapRunnerWithAllowlist(nil)
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist(nil): %v", err)
	}
	defer r.Close(context.Background())
	if r.proxyChild() != nil {
		t.Errorf("default-deny constructor must not spawn proxy child")
	}
	if r.proxyActive() {
		t.Errorf("default-deny runner must report proxyActive()=false")
	}
}

// TestBwrapRunner_DefaultDenyEnvDoesNotInjectProxyVars asserts the
// env injection invariant: a runner without an active proxy MUST NOT
// emit HTTP_PROXY / HTTPS_PROXY / ALL_PROXY into the bash env.
// DirectRunner-equivalent behavior is preserved.
func TestBwrapRunner_DefaultDenyEnvDoesNotInjectProxyVars(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap runner is linux-only")
	}
	r, err := NewBwrapRunnerWithAllowlist(nil)
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist(nil): %v", err)
	}
	defer r.Close(context.Background())
	env := r.bwrapBashEnvForRun([]string{"PATH=/usr/bin"}, "/tmp/sess", "/tmp/sess")
	for _, kv := range env {
		if strings.HasPrefix(kv, "HTTP_PROXY=") ||
			strings.HasPrefix(kv, "HTTPS_PROXY=") ||
			strings.HasPrefix(kv, "ALL_PROXY=") {
			t.Errorf("default-deny runner must NOT inject proxy env; got %q", kv)
		}
	}
}

// TestBwrapBuildArgs_DefaultDenyMatchesLegacy asserts the legacy
// default-deny argv shape is preserved when the runner has no proxy.
// Regression guard for B3b-3 capability survival under B3b-4-3.
func TestBwrapBuildArgs_DefaultDenyMatchesLegacy(t *testing.T) {
	req := BashRunRequest{
		SessionDir: "/tmp/sess",
		WorkDir:    "/tmp/sess",
		Command:    "echo hi",
	}
	args := buildBwrapArgs(req, "/bin/bash")
	// The wrapper command MUST still be /bin/bash -c <cmd> with no
	// bridge subcommand smuggled in.
	wantTail := []string{"--", "/bin/bash", "-c", "echo hi"}
	if len(args) < len(wantTail) {
		t.Fatalf("args too short: %v", args)
	}
	got := args[len(args)-len(wantTail):]
	for i, w := range wantTail {
		if got[i] != w {
			t.Errorf("args[%d] = %q, want %q", i, got[i], w)
		}
	}
	for _, a := range args {
		if strings.Contains(a, "netproxy-bridge") {
			t.Errorf("default-deny argv must not contain bridge subcommand; got %q", a)
		}
	}
}

// TestBwrapBuildArgsWithBridge_ShapeForProxyRequiredRun pins the argv
// shape for a proxy-required Run: the wrapper is the elnath binary
// invoking netproxy-bridge with both UDS endpoints, fixed netns-local
// listen ports, and the user command threaded through --user-cmd.
//
// This test covers the wrapper command quoting safety contract: the
// user command is a SINGLE argv element, not concatenated with extra
// quoting layers.
func TestBwrapBuildArgsWithBridge_ShapeForProxyRequiredRun(t *testing.T) {
	req := BashRunRequest{
		SessionDir: "/tmp/sess",
		WorkDir:    "/tmp/sess",
		Command:    "echo 'hello $USER; ls $(pwd)'",
	}
	args := buildBwrapArgsWithBridge(req, "/opt/elnath/elnath", "/tmp/uds-XXX",
		"/tmp/uds-XXX/http.sock", "/tmp/uds-XXX/socks.sock")

	mustContainPair := func(flag, value string) {
		t.Helper()
		for i := 0; i+1 < len(args); i++ {
			if args[i] == flag && args[i+1] == value {
				return
			}
		}
		t.Errorf("argv missing pair %s %q; got: %v", flag, value, args)
	}
	mustContainPair("--bind", "/tmp/uds-XXX")
	mustContainPair("--ro-bind", "/opt/elnath/elnath")
	mustContainPair("--uds-http", "/tmp/uds-XXX/http.sock")
	mustContainPair("--uds-socks", "/tmp/uds-XXX/socks.sock")
	mustContainPair("--listen-http", netproxyBridgeListenHTTPInternal)
	mustContainPair("--listen-socks", netproxyBridgeListenSOCKSInternal)
	mustContainPair("--user-cmd", req.Command)

	// The user-cmd value is a single argv element. Find it and
	// confirm.
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--user-cmd" {
			if args[i+1] != req.Command {
				t.Errorf("--user-cmd value = %q, want %q (single argv element, no extra quoting)",
					args[i+1], req.Command)
			}
		}
	}
}

// TestBwrapBuildArgsWithBridge_QuotingSafetyForCraftedCommands sweeps
// command strings that contain shell metacharacters, single quotes,
// semicolons, command substitution, and newlines. The wrapper passes
// the user command as a single argv element with no extra quoting
// layers, so the bridge sees the EXACT bytes the agent emitted.
//
// This is the partner-pinned wrapper command quoting safety contract.
func TestBwrapBuildArgsWithBridge_QuotingSafetyForCraftedCommands(t *testing.T) {
	cases := []string{
		"echo 'single quotes here'",
		"echo a; echo b",
		"echo $(date)",
		"echo line1\necho line2",
		`echo "double quotes" 'and singles'`,
		"echo backticks `date`",
	}
	for _, cmd := range cases {
		req := BashRunRequest{
			SessionDir: "/tmp/sess",
			WorkDir:    "/tmp/sess",
			Command:    cmd,
		}
		args := buildBwrapArgsWithBridge(req, "/opt/elnath/elnath",
			"/tmp/uds", "/tmp/uds/http.sock", "/tmp/uds/socks.sock")
		var found bool
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "--user-cmd" {
				if args[i+1] != cmd {
					t.Errorf("crafted command %q rendered as %q (must be byte-identical)",
						cmd, args[i+1])
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("crafted command %q missing --user-cmd in argv: %v", cmd, args)
		}
	}
}
