//go:build linux

package tools

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"
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
	// Force the netproxy spawn to fail with a fast, well-defined
	// error. Without this override, os.Executable() returns the Go
	// test binary itself; re-execing the test binary as
	// `tools.test netproxy ...` recursively re-runs every test in
	// this package, holds the parent test binary's stderr fd via
	// inherited descriptors, and tickles the
	// `Test I/O incomplete 1m0s after exiting` runtime hang that
	// broke CI on 2026-04-26. /bin/false exec's, exits with code 1,
	// the readiness preamble never arrives, and the constructor
	// returns a netproxy-side error — which is exactly the failure
	// mode this test wants to assert wording against.
	t.Setenv(netproxyBwrapBinaryOverrideEnv, "/bin/false")

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
	// See TestBwrapFactory_DomainAllowlistAcceptedAfterB3b43 for why
	// this override is mandatory: the default os.Executable() in test
	// context is the test binary itself, which would recursively
	// re-run all tests when invoked as `netproxy ...` and leak
	// inherited stderr fds.
	t.Setenv(netproxyBwrapBinaryOverrideEnv, "/bin/false")

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

// TestBwrapProxy_SpawnProxyChildConfiguresProcessGroup pins the
// hardening that prevents the v41 / B3b-4-3 CI subprocess pipe leak.
//
// Background: when BwrapRunner.spawnProxyChild self-execs the elnath
// binary OR (in test contexts without an override) the test binary
// itself, the child must be placed in its own process group via
// Setpgid=true. Without this, killing the child via cmd.Process.Kill
// only delivers SIGKILL to the immediate child PID. If the child
// happens to have spawned grandchildren (e.g., when os.Executable()
// returns a Go test binary that recursively re-runs all tests),
// those grandchildren survive, inherit the original test binary's
// stderr fd, and hold the pipe open after the test process exits.
// The Go test runtime then prints `Test I/O incomplete 1m0s after
// exiting; exec: WaitDelay expired before I/O complete` and FAILs.
//
// Setpgid=true makes cmd.Process.Kill (and our process-group helpers)
// reach the entire descendant tree via syscall.Kill(-pid, signal),
// matching the substrate-runner cleanup discipline applied to the
// bash invocation itself (configureProcessCleanup at Run time).
func TestBwrapProxy_SpawnProxyChildConfiguresProcessGroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap runner is linux-only")
	}
	// Exercise the helper that places the proxy child in its own
	// process group. configureProxyChildProcessGroup MUST set
	// Setpgid=true on the supplied exec.Cmd. If the helper ever
	// stops setting the bit, the constructor's kill semantics
	// regress and the recursive-self-exec leak that broke CI on
	// 2026-04-26 returns.
	cmd := exec.Command("/bin/true")
	configureProxyChildProcessGroup(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("configureProxyChildProcessGroup must set SysProcAttr")
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Errorf("proxy child must run in its own process group (Setpgid=true) so kill reaches grandchildren")
	}
}

// TestBwrapProxy_RunOverridesClassificationOnPostBashProxyDeath pins
// reviewer M-1: when the supervised proxy child dies in the race
// window between Run's pre-check (proxyChildAlive at line 684) and
// the bash command's exit, the resulting BashRunResult must surface
// Classification="network_proxy_failed" so operators can distinguish
// "command failed because the network proxy died" from "command
// failed because of a real bash exit code".
//
// The test exercises the unit-level helper that performs the post-Run
// classification override (overrideClassificationIfProxyDied). Race
// reproduction at the integration level lives in the integration
// file; this unit test pins the helper's contract so the override
// path stays correct even when the integration test cannot run
// (cross-platform CI, no bwrap available).
func TestBwrapProxy_RunOverridesClassificationOnPostBashProxyDeath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap runner is linux-only")
	}
	// Construct a minimal BashRunResult representing a "bash exited
	// nonzero" state — what the user would see if their command's
	// network calls failed because the proxy died mid-run.
	exit := 1
	original := BashRunResult{
		Output:         "curl: (7) Failed to connect to ...: Connection refused",
		IsError:        true,
		ExitCode:       &exit,
		Classification: "network_failure",
		CWD:            ".",
	}
	overridden := overrideClassificationIfProxyDied(original, true)
	if overridden.Classification != "network_proxy_failed" {
		t.Errorf("Classification = %q, want %q (proxy died → operator-visible cause)",
			overridden.Classification, "network_proxy_failed")
	}
	if !overridden.IsError {
		t.Errorf("IsError must remain true after proxy-death override")
	}
	// Original Output must be preserved as authoritative bash output.
	if overridden.Output != original.Output {
		t.Errorf("Output mutated by classification override: got %q want %q",
			overridden.Output, original.Output)
	}

	// Conversely, when the proxy is alive after Run, classification
	// must NOT be touched.
	preserved := overrideClassificationIfProxyDied(original, false)
	if preserved.Classification != "network_failure" {
		t.Errorf("proxy-alive path must preserve Classification; got %q", preserved.Classification)
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

// ---------------------------------------------------------------
// v42-1b: permitted-connection audit projection (parity with darwin)
// ---------------------------------------------------------------

// TestBwrapRunner_CollectProxyDecisionsProjectsAllowsToAuditRecords
// pins the v42-1b parity contract on linux: a mixed Decision buffer
// must produce both deny-shaped Violations and allow-shaped
// AuditRecords from a single snapshot, and the buffer must clear so
// the next call returns empty projections.
func TestBwrapRunner_CollectProxyDecisionsProjectsAllowsToAuditRecords(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap audit projection is linux-only")
	}
	r := newBwrapRunnerForAuditTest()
	seedBwrapProxyDecisionsForTest(r, []Decision{
		{Allow: true, Source: SourceNetworkProxy, Host: "ok.example", Port: 443, Protocol: ProtocolHTTPSConnect},
		{Allow: false, Source: SourceNetworkProxy, Reason: ReasonNotInAllowlist, Host: "blocked.example", Port: 443, Protocol: ProtocolHTTPSConnect},
		{Allow: true, Source: SourceNetworkProxy, Host: "also-ok.example", Port: 443, Protocol: ProtocolHTTPSConnect},
	})
	violations, audit, drop := collectBwrapProxyDecisionsForTest(r)
	if len(violations) != 1 {
		t.Errorf("len(violations) = %d, want 1", len(violations))
	}
	if violations[0].Host != "blocked.example" {
		t.Errorf("violation[0].Host = %q, want %q", violations[0].Host, "blocked.example")
	}
	if len(audit) != 2 {
		t.Errorf("len(audit) = %d, want 2", len(audit))
	}
	if drop != 0 {
		t.Errorf("drop = %d, want 0", drop)
	}
	violations2, audit2, drop2 := collectBwrapProxyDecisionsForTest(r)
	if len(violations2) != 0 || len(audit2) != 0 || drop2 != 0 {
		t.Errorf("buffer not cleared; got violations=%d audit=%d drop=%d", len(violations2), len(audit2), drop2)
	}
}

// TestBwrapRunner_CollectProxyDecisionsConcurrentRunIsolation pins
// the per-Run isolation contract: two goroutines, each populating its
// own runner's buffer with a distinct allow Decision, must each see
// only their own Decision in the resulting AuditRecords.
func TestBwrapRunner_CollectProxyDecisionsConcurrentRunIsolation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap audit projection is linux-only")
	}
	const iterations = 200
	for i := 0; i < iterations; i++ {
		rA := newBwrapRunnerForAuditTest()
		rB := newBwrapRunnerForAuditTest()
		var wg sync.WaitGroup
		var auditA, auditB []SandboxAuditRecord
		wg.Add(2)
		go func() {
			defer wg.Done()
			seedBwrapProxyDecisionsForTest(rA, []Decision{{
				Allow: true, Source: SourceNetworkProxy, Host: "alpha.example", Port: 443, Protocol: ProtocolHTTPSConnect,
			}})
			_, auditA, _ = collectBwrapProxyDecisionsForTest(rA)
		}()
		go func() {
			defer wg.Done()
			seedBwrapProxyDecisionsForTest(rB, []Decision{{
				Allow: true, Source: SourceNetworkProxy, Host: "beta.example", Port: 443, Protocol: ProtocolHTTPSConnect,
			}})
			_, auditB, _ = collectBwrapProxyDecisionsForTest(rB)
		}()
		wg.Wait()
		if len(auditA) != 1 || auditA[0].Host != "alpha.example" {
			t.Fatalf("iter %d: rA audit cross-attributed: %+v", i, auditA)
		}
		if len(auditB) != 1 || auditB[0].Host != "beta.example" {
			t.Fatalf("iter %d: rB audit cross-attributed: %+v", i, auditB)
		}
	}
}

// TestBwrapRunner_AuditProjectionMatchesPlatformAgnosticHelper pins
// cross-platform parity: the linux substrate's collectProxyDecisions
// MUST produce identical AuditRecords to the platform-agnostic
// projectAuditRecords helper given the same Decision input. Together
// with the darwin equivalent
// (TestSeatbeltRunner_AuditProjectionMatchesPlatformAgnosticHelper)
// this covers the macOS+Linux parity assertion.
func TestBwrapRunner_AuditProjectionMatchesPlatformAgnosticHelper(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap audit projection is linux-only")
	}
	decisions := []Decision{
		{Allow: true, Source: SourceNetworkProxy, Host: "github.com", Port: 443, Protocol: ProtocolHTTPSConnect},
		{Allow: true, Source: SourceNetworkProxy, Host: "api.example.com", Port: 443, Protocol: ProtocolHTTPSConnect},
		{Allow: false, Source: SourceNetworkProxy, Reason: ReasonNotInAllowlist, Host: "blocked.example", Port: 443, Protocol: ProtocolHTTPSConnect},
	}
	want, wantDrop := projectAuditRecords(decisions, auditRecordRetentionDefault)

	r := newBwrapRunnerForAuditTest()
	seedBwrapProxyDecisionsForTest(r, decisions)
	_, got, gotDrop := collectBwrapProxyDecisionsForTest(r)
	if gotDrop != wantDrop {
		t.Errorf("drop count drift: substrate=%d helper=%d", gotDrop, wantDrop)
	}
	if len(got) != len(want) {
		t.Fatalf("audit length drift: substrate=%d helper=%d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("audit[%d] drift: substrate=%+v helper=%+v", i, got[i], want[i])
		}
	}
}
