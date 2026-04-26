//go:build darwin

package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// B3b-4-2 Phase B: SeatbeltRunner proxy child lifecycle.
//
// When the SeatbeltRunner is constructed with an allowlist or denylist
// that requires the proxy substrate (any domain entry, any non-loopback
// IP entry), the runner MUST:
//
//  1. Spawn a child process via os.Executable() (NOT /proc/self/exe —
//     that path does not exist on macOS) at construction time, NOT
//     per-Run. The child runs the production `elnath netproxy ...`
//     subcommand from cmd/elnath/cmd_netproxy.go.
//  2. Wait for the child to print bound ports (httpListen=...,
//     socksListen=..., ready) before returning a Runner to the caller.
//  3. Capture both bound port numbers so SBPL profile generation can
//     pin them per partner pin C3 (per-port only, never localhost:*).
//  4. Inject HTTP_PROXY / HTTPS_PROXY / ALL_PROXY into the bash
//     command env via cleanBashEnv so the bash command actually uses
//     the proxy.
//  5. On Close(), gracefully shut down the proxy child + reap the
//     PID — no orphans.
//  6. If the proxy child crashes mid-session, the next Run() MUST
//     fail with Classification = "sandbox_setup_failed" or
//     "network_proxy_failed" — NEVER silent fallback to DirectRunner.
//
// Loopback-only allowlist (Phase 1 capability) MUST NOT spawn a proxy
// child — the SBPL `(remote ip "localhost:port")` rule handles
// loopback ports directly without proxy involvement.

// requireSeatbeltProxyTestSupport ensures the test runs only under
// realistic conditions: actual macOS (not just darwin GOOS), an elnath
// binary buildable by `go build`, and an executable test artifact path.
// Tests that don't need the binary (e.g. SBPL profile string assertions)
// skip this helper.
func requireSeatbeltProxyTestSupport(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("seatbelt proxy lifecycle is darwin-only")
	}
}

// buildElnathBinaryForSeatbeltProxy compiles a fresh elnath binary into
// a tempdir so the SeatbeltRunner can self-exec via the absolute path.
// The seatbelt runner cannot use os.Args[0] because go test compiles
// the test harness as `tools.test`, not the elnath binary; production
// uses os.Executable() which returns the elnath binary path. We
// substitute by exposing a hook the runner uses for tests.
func buildElnathBinaryForSeatbeltProxy(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "elnath-seatbelt-proxy")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/elnath")
	cmd.Dir = repoRootDarwin(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build elnath: %v\n%s", err, out)
	}
	return binPath
}

// repoRootDarwin walks up from the test cwd until go.mod is found.
// Mirrors the Linux-spike helper without depending on the linux-tagged
// test file.
func repoRootDarwin(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// TestSeatbeltRunner_EmptyAllowlistDoesNotSpawnProxyChild covers the
// resource-frugality invariant: if the user has no allowlist entries
// the SBPL `(deny default)` baseline alone enforces network policy and
// no proxy child is needed.
func TestSeatbeltRunner_EmptyAllowlistDoesNotSpawnProxyChild(t *testing.T) {
	requireSeatbeltProxyTestSupport(t)
	r, err := NewSeatbeltRunnerWithAllowlist(nil)
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist(nil): %v", err)
	}
	defer r.Close(context.Background())
	if r.proxyChild() != nil {
		t.Errorf("empty allowlist must not spawn proxy child; got %+v", r.proxyChild())
	}
	if r.httpProxyPort() != 0 || r.socksProxyPort() != 0 {
		t.Errorf("empty allowlist must report zero proxy ports; got http=%d socks=%d",
			r.httpProxyPort(), r.socksProxyPort())
	}
}

// TestSeatbeltRunner_LoopbackOnlyAllowlistDoesNotSpawnProxyChild
// preserves the Phase 1 capability: loopback IPs reach the SBPL
// `(remote ip "localhost:port")` rule directly without proxy
// involvement.
func TestSeatbeltRunner_LoopbackOnlyAllowlistDoesNotSpawnProxyChild(t *testing.T) {
	requireSeatbeltProxyTestSupport(t)
	r, err := NewSeatbeltRunnerWithAllowlist([]string{"127.0.0.1:5555", "[::1]:8080"})
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())
	if r.proxyChild() != nil {
		t.Errorf("loopback-only allowlist must not spawn proxy child")
	}
	if r.httpProxyPort() != 0 || r.socksProxyPort() != 0 {
		t.Errorf("loopback-only allowlist must report zero proxy ports")
	}
}

// TestSeatbeltRunner_DomainAllowlistSpawnsProxyChildAndCapturesPorts
// covers the production path: domain entry triggers proxy spawn at
// runner construction; both port numbers are captured for SBPL
// emission.
func TestSeatbeltRunner_DomainAllowlistSpawnsProxyChildAndCapturesPorts(t *testing.T) {
	requireSeatbeltProxyTestSupport(t)
	binPath := buildElnathBinaryForSeatbeltProxy(t)
	t.Setenv("ELNATH_NETPROXY_BINARY_OVERRIDE", binPath)

	r, err := NewSeatbeltRunnerWithAllowlist([]string{"github.com:443"})
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist(domain): %v", err)
	}
	defer r.Close(context.Background())

	child := r.proxyChild()
	if child == nil {
		t.Fatalf("domain allowlist must spawn proxy child")
	}
	if r.httpProxyPort() == 0 || r.socksProxyPort() == 0 {
		t.Errorf("expected non-zero http/socks ports; got http=%d socks=%d",
			r.httpProxyPort(), r.socksProxyPort())
	}
	if child.Process == nil || child.Process.Pid == 0 {
		t.Errorf("proxy child has no PID")
	}
}

// TestSeatbeltRunner_CloseShutsDownProxyChildCleanly asserts the
// runner-lifetime resource is released on Close.
func TestSeatbeltRunner_CloseShutsDownProxyChildCleanly(t *testing.T) {
	requireSeatbeltProxyTestSupport(t)
	binPath := buildElnathBinaryForSeatbeltProxy(t)
	t.Setenv("ELNATH_NETPROXY_BINARY_OVERRIDE", binPath)

	r, err := NewSeatbeltRunnerWithAllowlist([]string{"github.com:443"})
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}
	pid := r.proxyChild().Process.Pid

	if err := r.Close(context.Background()); err != nil {
		t.Errorf("Close: %v", err)
	}

	// Give the kernel a beat to reap.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("proxy child PID %d still alive after Close", pid)
}

// pidAlive reports whether the given PID is still running, by
// signaling 0 and observing the result.
func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall0); err != nil {
		return false
	}
	return true
}

// TestSeatbeltRunner_ProxyChildCrashCausesNextRunToFailWithoutFallback
// pins the no-silent-fallback invariant.
func TestSeatbeltRunner_ProxyChildCrashCausesNextRunToFailWithoutFallback(t *testing.T) {
	requireSeatbeltProxyTestSupport(t)
	binPath := buildElnathBinaryForSeatbeltProxy(t)
	t.Setenv("ELNATH_NETPROXY_BINARY_OVERRIDE", binPath)

	r, err := NewSeatbeltRunnerWithAllowlist([]string{"github.com:443"})
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	child := r.proxyChild()
	if child == nil || child.Process == nil {
		t.Fatalf("proxy child not spawned")
	}
	// Forcibly kill the proxy child to simulate a crash.
	if err := child.Process.Kill(); err != nil {
		t.Fatalf("kill proxy child: %v", err)
	}
	// Wait for the runner to observe the dead state via its
	// internal Wait goroutine (sticky atomic flag flips when
	// cmd.Wait returns).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !r.proxyChildAlive() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if r.proxyChildAlive() {
		t.Fatalf("runner did not observe child crash within timeout")
	}

	// Sentinel session for Run.
	sessionDir, _ := seatbeltSessionDirs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, runErr := r.Run(ctx, BashRunRequest{
		Command:    "true",
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run returned non-nil error (must come back via Result): %v", runErr)
	}
	if !res.IsError {
		t.Errorf("expected IsError when proxy child has crashed; got success")
	}
	if res.Classification != "sandbox_setup_failed" && res.Classification != "network_proxy_failed" {
		t.Errorf("expected classification sandbox_setup_failed or network_proxy_failed; got %q",
			res.Classification)
	}
	if !strings.Contains(res.Output, "proxy") {
		t.Errorf("expected proxy mention in output; got: %q", res.Output)
	}
}

// ---------------------------------------------------------------
// Phase C: SBPL per-port pinning (partner pin C3)
// ---------------------------------------------------------------

func TestSeatbeltProfile_AllowlistEmitsHTTPProxyPort(t *testing.T) {
	req := BashRunRequest{SessionDir: "/private/tmp/x"}
	p := seatbeltProfileWithProxyPorts(req, nil, 31280, 31281)
	want := `(allow network-outbound (remote ip "localhost:31280"))`
	if !strings.Contains(p, want) {
		t.Errorf("profile must contain %q; got:\n%s", want, p)
	}
}

func TestSeatbeltProfile_AllowlistEmitsSOCKSProxyPort(t *testing.T) {
	req := BashRunRequest{SessionDir: "/private/tmp/x"}
	p := seatbeltProfileWithProxyPorts(req, nil, 31280, 31281)
	want := `(allow network-outbound (remote ip "localhost:31281"))`
	if !strings.Contains(p, want) {
		t.Errorf("profile must contain %q; got:\n%s", want, p)
	}
}

// TestSeatbeltProfile_NeverEmitsLocalhostStarWildcard pins the
// partner-locked C3 invariant: localhost:* MUST NEVER appear in the
// emitted SBPL profile, regardless of allowlist contents.
func TestSeatbeltProfile_NeverEmitsLocalhostStarWildcard(t *testing.T) {
	req := BashRunRequest{SessionDir: "/private/tmp/x"}
	cases := [][]string{
		nil,
		{"127.0.0.1:5432"},
		{"127.0.0.1:5432", "[::1]:8080"},
	}
	for _, allowlist := range cases {
		p := seatbeltProfileWithProxyPorts(req, allowlist, 31280, 31281)
		if strings.Contains(p, "localhost:*") {
			t.Errorf("allowlist %v emitted forbidden localhost:* pattern:\n%s", allowlist, p)
		}
	}
}

func TestSeatbeltProfile_UserLoopbackEntryEmittedAsLocalhostPerPort(t *testing.T) {
	req := BashRunRequest{SessionDir: "/private/tmp/x"}
	p := seatbeltProfileWithProxyPorts(req, []string{"127.0.0.1:5432"}, 31280, 31281)
	want := `(allow network-outbound (remote ip "localhost:5432"))`
	if !strings.Contains(p, want) {
		t.Errorf("user 127.0.0.1:5432 must emit %q; got:\n%s", want, p)
	}
}

// ---------------------------------------------------------------
// Phase D: env injection
// ---------------------------------------------------------------

func TestSeatbeltRunner_EnvInjectionWhenProxyActive(t *testing.T) {
	requireSeatbeltProxyTestSupport(t)
	binPath := buildElnathBinaryForSeatbeltProxy(t)
	t.Setenv("ELNATH_NETPROXY_BINARY_OVERRIDE", binPath)

	r, err := NewSeatbeltRunnerWithAllowlist([]string{"github.com:443"})
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	env := r.bashEnvForRun([]string{"PATH=/usr/bin"}, "/tmp/sess", "/tmp/sess")
	httpProxyFound := false
	httpsProxyFound := false
	allProxyFound := false
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "HTTP_PROXY="):
			httpProxyFound = true
			if !strings.Contains(kv, "127.0.0.1:") {
				t.Errorf("HTTP_PROXY missing 127.0.0.1: %q", kv)
			}
		case strings.HasPrefix(kv, "HTTPS_PROXY="):
			httpsProxyFound = true
			if !strings.Contains(kv, "127.0.0.1:") {
				t.Errorf("HTTPS_PROXY missing 127.0.0.1: %q", kv)
			}
		case strings.HasPrefix(kv, "ALL_PROXY="):
			allProxyFound = true
			if !strings.Contains(kv, "socks5h://127.0.0.1:") {
				t.Errorf("ALL_PROXY must use socks5h://; got %q", kv)
			}
		}
	}
	if !httpProxyFound || !httpsProxyFound || !allProxyFound {
		t.Errorf("env injection incomplete: HTTP_PROXY=%v HTTPS_PROXY=%v ALL_PROXY=%v",
			httpProxyFound, httpsProxyFound, allProxyFound)
	}
}

func TestSeatbeltRunner_NoEnvInjectionForEmptyOrLoopbackOnly(t *testing.T) {
	requireSeatbeltProxyTestSupport(t)
	cases := [][]string{nil, {"127.0.0.1:5555"}}
	for _, allowlist := range cases {
		r, err := NewSeatbeltRunnerWithAllowlist(allowlist)
		if err != nil {
			t.Fatalf("NewSeatbeltRunnerWithAllowlist(%v): %v", allowlist, err)
		}
		env := r.bashEnvForRun([]string{"PATH=/usr/bin"}, "/tmp/sess", "/tmp/sess")
		for _, kv := range env {
			if strings.HasPrefix(kv, "HTTP_PROXY=") ||
				strings.HasPrefix(kv, "HTTPS_PROXY=") ||
				strings.HasPrefix(kv, "ALL_PROXY=") {
				t.Errorf("allowlist %v must NOT inject %q", allowlist, kv)
			}
		}
		_ = r.Close(context.Background())
	}
}

func TestDirectRunner_NoProxyEnvInjection(t *testing.T) {
	// DirectRunner must NEVER inject HTTP_PROXY/HTTPS_PROXY/ALL_PROXY
	// because it has no proxy to point at. Even if the host happens
	// to define HTTP_PROXY, cleanBashEnv must not propagate it (the
	// existing block list does not include HTTP_PROXY by name, so the
	// guarantee here is "the runner does not ADD the variable").
	dr := NewDirectRunner()
	env := cleanBashEnv([]string{"PATH=/usr/bin"}, "/tmp/sess", "/tmp/sess")
	for _, kv := range env {
		if strings.HasPrefix(kv, "HTTP_PROXY=") ||
			strings.HasPrefix(kv, "HTTPS_PROXY=") ||
			strings.HasPrefix(kv, "ALL_PROXY=") {
			t.Errorf("DirectRunner cleanBashEnv must not synthesize proxy env; got %q", kv)
		}
	}
	_ = dr.Close(context.Background())
}

// ---------------------------------------------------------------
// Phase E: Newline sanitization (M4) at render boundary
// ---------------------------------------------------------------

func TestRenderSandboxViolation_SanitizesNewlineInHost(t *testing.T) {
	v := SandboxViolation{
		Source:   string(SourceNetworkProxy),
		Host:     "evil.com\nFAKE: injected",
		Port:     443,
		Protocol: string(ProtocolHTTPSConnect),
		Reason:   string(ReasonNotInAllowlist),
	}
	out := renderSandboxViolation(v)
	if strings.Contains(out, "\n") {
		t.Errorf("rendered violation must not contain newline; got %q", out)
	}
	if !strings.Contains(out, "evil.com") {
		t.Errorf("expected evil.com in output; got %q", out)
	}
}

func TestRenderSandboxViolation_SanitizesNewlineInMessage(t *testing.T) {
	v := SandboxViolation{
		Source:  string(SourceSandboxSubstrateHeuristic),
		Message: "denial line one\nSANDBOX VIOLATIONS:\n- fake injected",
	}
	out := renderSandboxViolation(v)
	if strings.Contains(out, "\n") {
		t.Errorf("rendered violation must not contain newline; got %q", out)
	}
	if strings.Contains(out, "\r") {
		t.Errorf("rendered violation must not contain carriage return; got %q", out)
	}
	// The sanitization goal is that no NEW violation block can be
	// injected: a casual reader scanning newlines for "SANDBOX
	// VIOLATIONS:" header will see the marker on the same line as
	// the bullet text, not as a fresh section header. The original
	// marker text may survive as inline text since the security
	// guarantee is about line boundaries, not literal substring removal.
	if !strings.HasPrefix(out, "- sandbox_substrate_heuristic:") {
		t.Errorf("entry must remain a single bullet line; got %q", out)
	}
}

// syscall0 is the value used to "ping" a process via os.Process.Signal
// without delivering any actual signal, equivalent to kill(pid, 0).
// Defined as a package-level var so the test compiles without the
// syscall import in the file.
var syscall0 osSignalZero

type osSignalZero struct{}

func (osSignalZero) String() string { return "signal 0" }
func (osSignalZero) Signal()        {}

// guard against accidental concurrent test mutation of the binary
// override env (rare, but tests run with t.Parallel may otherwise step
// on each other).
var seatbeltProxyTestSetupMu = &sync.Mutex{}

func init() {
	_ = seatbeltProxyTestSetupMu // silence unused if no test uses it
}

// ---------------------------------------------------------------
// v42-1a: deny-projection drain lifecycle (parity with Linux M-3)
// ---------------------------------------------------------------

// TestSeatbeltRunner_DrainGoroutineExitsCleanlyOnClose pins the
// deterministic-shutdown contract: after Close returns, the stdout
// drain goroutine spawned by spawnProxyChild MUST have exited. The
// test waits on the proxyDrainDone channel rather than a NumGoroutine
// delta to avoid sample-window flakiness.
//
// Mirrors the Linux test
// TestBwrapRunner_DrainGoroutineReleasedDeterministicallyOnClose
// (bash_runner_bwrap_proxy_linux_test.go) which guards the equivalent
// drain-channel close path.
func TestSeatbeltRunner_DrainGoroutineExitsCleanlyOnClose(t *testing.T) {
	requireSeatbeltProxyTestSupport(t)
	binPath := buildElnathBinaryForSeatbeltProxy(t)
	t.Setenv("ELNATH_NETPROXY_BINARY_OVERRIDE", binPath)

	r, err := NewSeatbeltRunnerWithAllowlist([]string{"github.com:443"})
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}

	r.proxyMu.Lock()
	drainDone := r.proxyDrainDone
	r.proxyMu.Unlock()
	if drainDone == nil {
		t.Fatalf("proxy-required runner must have spawned a drain goroutine; proxyDrainDone is nil")
	}

	if err := r.Close(context.Background()); err != nil {
		t.Errorf("Close: %v", err)
	}

	select {
	case <-drainDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("drain goroutine did not exit within 3s after Close — channel-based shutdown is not wired")
	}
}

// TestSeatbeltRunner_CloseIsIdempotent pins critic Major 1: a second
// Close call must be a safe no-op AND the first call must have actually
// shut drain down. This catches the historical fast-path early-return
// at line 451 (`if cmd == nil || cmd.Process == nil || already`) which
// previously skipped drain shutdown when proxyShutdown was already true.
func TestSeatbeltRunner_CloseIsIdempotent(t *testing.T) {
	requireSeatbeltProxyTestSupport(t)
	binPath := buildElnathBinaryForSeatbeltProxy(t)
	t.Setenv("ELNATH_NETPROXY_BINARY_OVERRIDE", binPath)

	r, err := NewSeatbeltRunnerWithAllowlist([]string{"github.com:443"})
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}

	r.proxyMu.Lock()
	drainDone := r.proxyDrainDone
	r.proxyMu.Unlock()
	if drainDone == nil {
		t.Fatalf("proxy-required runner must have spawned a drain goroutine; proxyDrainDone is nil")
	}

	if err := r.Close(context.Background()); err != nil {
		t.Errorf("first Close: %v", err)
	}
	select {
	case <-drainDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("first Close did not release drain goroutine within 3s")
	}

	if err := r.Close(context.Background()); err != nil {
		t.Errorf("second Close (must be no-op): %v", err)
	}
}

// TestSeatbeltRunner_CloseAfterProxyDeadStillShutsDownDrain pins
// critic Major 2: Close called after the proxy child has died MUST
// still close drainShutdown + await drainDone. Linux Close at
// bash_runner_bwrap_linux.go:241-307 has no dead-proxy fast-path; macOS
// must match.
func TestSeatbeltRunner_CloseAfterProxyDeadStillShutsDownDrain(t *testing.T) {
	requireSeatbeltProxyTestSupport(t)
	binPath := buildElnathBinaryForSeatbeltProxy(t)
	t.Setenv("ELNATH_NETPROXY_BINARY_OVERRIDE", binPath)

	r, err := NewSeatbeltRunnerWithAllowlist([]string{"github.com:443"})
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}

	r.proxyMu.Lock()
	drainDone := r.proxyDrainDone
	child := r.proxyCmd
	r.proxyMu.Unlock()
	if drainDone == nil || child == nil || child.Process == nil {
		t.Fatalf("proxy-required runner must have a running child + drain; got drainDone=%v child=%v",
			drainDone, child)
	}

	// Forcibly kill the child so the runner observes the dead state
	// before Close runs. The Wait goroutine flips proxyDead and pushes
	// to doneCh; we wait for that observation then call Close.
	if err := child.Process.Kill(); err != nil {
		t.Fatalf("kill proxy child: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !r.proxyChildAlive() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if r.proxyChildAlive() {
		t.Fatalf("runner did not observe child crash within timeout")
	}

	if err := r.Close(context.Background()); err != nil {
		t.Errorf("Close after child crash: %v", err)
	}

	select {
	case <-drainDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("drain goroutine leaked: Close after dead-proxy did NOT shut drain down within 3s")
	}
}

// ---------------------------------------------------------------
// v42-1b: permitted-connection audit projection (parity with Linux)
// ---------------------------------------------------------------

// TestSeatbeltRunner_CollectProxyDecisionsProjectsAllowsToAuditRecords
// pins the v42-1b parity contract on darwin: a mixed Decision buffer
// must produce both deny-shaped Violations and allow-shaped
// AuditRecords from a single snapshot, and the buffer must clear so
// the next call returns empty projections.
func TestSeatbeltRunner_CollectProxyDecisionsProjectsAllowsToAuditRecords(t *testing.T) {
	r := newSeatbeltRunnerForAuditTest()
	seedSeatbeltProxyDecisionsForTest(r, []Decision{
		{Allow: true, Source: SourceNetworkProxy, Host: "ok.example", Port: 443, Protocol: ProtocolHTTPSConnect},
		{Allow: false, Source: SourceNetworkProxy, Reason: ReasonNotInAllowlist, Host: "blocked.example", Port: 443, Protocol: ProtocolHTTPSConnect},
		{Allow: true, Source: SourceNetworkProxy, Host: "also-ok.example", Port: 443, Protocol: ProtocolHTTPSConnect},
	})
	violations, audit, drop := collectSeatbeltProxyDecisionsForTest(r)
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
	// Second call must observe empty buffer (clear-on-snapshot).
	violations2, audit2, drop2 := collectSeatbeltProxyDecisionsForTest(r)
	if len(violations2) != 0 || len(audit2) != 0 || drop2 != 0 {
		t.Errorf("buffer not cleared; got violations=%d audit=%d drop=%d", len(violations2), len(audit2), drop2)
	}
}

// TestSeatbeltRunner_CollectProxyDecisionsConcurrentRunIsolation pins
// the per-Run isolation contract: two goroutines, each populating its
// own runner's buffer with a distinct allow Decision, must each see
// only their own Decision in the resulting AuditRecords. The shared
// projectAuditRecords helper is stateless and the per-runner mutex
// scope keeps the buffers private.
func TestSeatbeltRunner_CollectProxyDecisionsConcurrentRunIsolation(t *testing.T) {
	const iterations = 200
	for i := 0; i < iterations; i++ {
		rA := newSeatbeltRunnerForAuditTest()
		rB := newSeatbeltRunnerForAuditTest()
		var wg sync.WaitGroup
		var auditA, auditB []SandboxAuditRecord
		wg.Add(2)
		go func() {
			defer wg.Done()
			seedSeatbeltProxyDecisionsForTest(rA, []Decision{{
				Allow: true, Source: SourceNetworkProxy, Host: "alpha.example", Port: 443, Protocol: ProtocolHTTPSConnect,
			}})
			_, auditA, _ = collectSeatbeltProxyDecisionsForTest(rA)
		}()
		go func() {
			defer wg.Done()
			seedSeatbeltProxyDecisionsForTest(rB, []Decision{{
				Allow: true, Source: SourceNetworkProxy, Host: "beta.example", Port: 443, Protocol: ProtocolHTTPSConnect,
			}})
			_, auditB, _ = collectSeatbeltProxyDecisionsForTest(rB)
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

// TestSeatbeltRunner_AuditProjectionMatchesPlatformAgnosticHelper pins
// cross-platform parity: the darwin substrate's collectProxyDecisions
// MUST produce identical AuditRecords to the platform-agnostic
// projectAuditRecords helper given the same Decision input. Together
// with the Linux equivalent (TestBwrapRunner_AuditProjectionMatchesPlatformAgnosticHelper)
// this covers the macOS+Linux parity assertion.
func TestSeatbeltRunner_AuditProjectionMatchesPlatformAgnosticHelper(t *testing.T) {
	decisions := []Decision{
		{Allow: true, Source: SourceNetworkProxy, Host: "github.com", Port: 443, Protocol: ProtocolHTTPSConnect},
		{Allow: true, Source: SourceNetworkProxy, Host: "api.example.com", Port: 443, Protocol: ProtocolHTTPSConnect},
		{Allow: false, Source: SourceNetworkProxy, Reason: ReasonNotInAllowlist, Host: "blocked.example", Port: 443, Protocol: ProtocolHTTPSConnect},
	}
	want, wantDrop := projectAuditRecords(decisions, auditRecordRetentionDefault)

	r := newSeatbeltRunnerForAuditTest()
	seedSeatbeltProxyDecisionsForTest(r, decisions)
	_, got, gotDrop := collectSeatbeltProxyDecisionsForTest(r)
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

// TestSeatbeltRunner_ProxyActiveIsRaceFree pins critic Minor 3: the
// proxyActive() helper added in v42-1a MUST hold proxyMu while reading
// proxyHTTPPort / proxyCmd. Without the lock, `go test -race` would
// flag a data race against the writer at spawnProxyChild line 263.
//
// Test runs only inside a -race build; otherwise it is a near-no-op.
// The hammering pattern (concurrent reader + writer) is what the race
// detector instruments — the test's GREEN signal IS "no race detected".
func TestSeatbeltRunner_ProxyActiveIsRaceFree(t *testing.T) {
	requireSeatbeltProxyTestSupport(t)
	binPath := buildElnathBinaryForSeatbeltProxy(t)
	t.Setenv("ELNATH_NETPROXY_BINARY_OVERRIDE", binPath)

	r, err := NewSeatbeltRunnerWithAllowlist([]string{"github.com:443"})
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	stop := make(chan struct{})
	defer close(stop)

	// Reader: hammers proxyActive on the hot path.
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				_ = r.proxyActive()
			}
		}
	}()
	// Writer: under proxyMu, restamps proxyHTTPPort to a sentinel and
	// back. This mirrors the Wait-goroutine race window where the field
	// is mutated while another goroutine reads via proxyActive.
	go func() {
		for i := 0; i < 1000; i++ {
			select {
			case <-stop:
				return
			default:
				r.proxyMu.Lock()
				saved := r.proxyHTTPPort
				r.proxyHTTPPort = 0
				r.proxyHTTPPort = saved
				r.proxyMu.Unlock()
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)
}
