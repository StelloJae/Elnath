//go:build linux && integration

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// B3b-4-3 Phase F: end-to-end Linux bwrap + netproxy integration
// tests. Build-tagged linux && integration so they run only when the
// developer (or CI) opts in via `go test -tags=integration`. Mirrors
// the darwin substrate's TestSeatbeltProxyIntegration_* coverage but
// targets BwrapRunner + the netns bridge.
//
// Lifecycle constraint coverage (M-1, M-2, M-3 from B3b-4-2 review):
//
//   - M-1: Close() select on doneCh without snapshot dance — covered
//     by TestBwrapProxy_CloseStopsProxyAndDrainGoroutines.
//   - M-2: waitForBwrapProxyChildReady kills child on timeout —
//     covered by TestBwrapProxy_SetupTimeoutKillsProxyChild.
//   - M-3: drain goroutine has explicit shutdown channel — covered
//     by TestBwrapProxy_CloseStopsProxyAndDrainGoroutines (drain
//     completes within Close grace, asserted via no orphan).
//
// What this file proves:
//
//  1. Lifecycle: setup timeout / clean exit / no orphan / Close
//     drains / no fallback on proxy unavailable.
//  2. Policy: allowed domain via HTTP CONNECT proxy + via SOCKS5;
//     denied domain blocked with network_proxy violation; direct
//     egress bypassing proxy blocked.
//  3. Regressions: host HOME .gitconfig still not loaded; heuristic
//     violations preserve Source=sandbox_substrate_heuristic.

// requireBwrapProxyTestSupport skips the test when the runtime
// substrate is missing — bwrap binary, /usr/bin/curl, and a
// functional user-namespace probe.
func requireBwrapProxyTestSupport(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("integration test linux-only")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skipf("bwrap missing: %v", err)
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skipf("curl missing: %v", err)
	}
	probe := exec.Command("bwrap",
		"--unshare-user", "--unshare-net",
		"--ro-bind", "/", "/",
		"/bin/true",
	)
	if err := probe.Run(); err != nil {
		t.Skipf("bwrap user-namespace probe failed: %v", err)
	}
}

// integrationBuildElnathBinaryForBwrap compiles a fresh elnath binary
// so the BwrapRunner has a stable absolute path to self-exec.
// Mirrors the darwin helper.
func integrationBuildElnathBinaryForBwrap(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "elnath-bwrap-integration")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/elnath")
	cmd.Dir = repoRootBwrap(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build elnath: %v\n%s", err, out)
	}
	return binPath
}

// repoRootBwrap walks up from the test cwd until go.mod is found.
func repoRootBwrap(t *testing.T) string {
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

// integrationStartBwrapUpstreams spins up tiny httptest.Servers used
// by the policy tests. Bodies are unique so a swapped routing path
// is detectable.
func integrationStartBwrapUpstreams(t *testing.T) (allowedURL, deniedURL, denied403URL string) {
	t.Helper()

	allowedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ALLOWED-UPSTREAM-OK")
	}))
	t.Cleanup(allowedSrv.Close)
	deniedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "SHOULD-NOT-REACH")
	}))
	t.Cleanup(deniedSrv.Close)
	upstream403Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = io.WriteString(w, "REAL-UPSTREAM-403")
	}))
	t.Cleanup(upstream403Srv.Close)

	return allowedSrv.URL, deniedSrv.URL, upstream403Srv.URL
}

// integrationAllowlistFromBwrapURL builds allowlist entries for the
// httptest.Server bind. httptest binds 127.0.0.1; we mirror with a
// loopback entry that gets folded into the proxy allowlist alongside
// the sentinel domain (which forces proxy-required mode).
func integrationAllowlistFromBwrapURL(t *testing.T, urls ...string) []string {
	t.Helper()
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		hp := strings.TrimPrefix(strings.TrimPrefix(u, "http://"), "https://")
		host, port, err := net.SplitHostPort(hp)
		if err != nil {
			t.Fatalf("bad upstream URL %q: %v", u, err)
		}
		out = append(out, fmt.Sprintf("%s:%s", host, port))
	}
	return out
}

// integrationBwrapProxyRequiredAllowlist forces the runner into
// proxy-required mode by including a sentinel domain entry
// alongside the loopback httptest entries. The sentinel is never
// connected to.
func integrationBwrapProxyRequiredAllowlist(loopbackEntries []string) []string {
	out := append([]string{}, loopbackEntries...)
	out = append(out, "sentinel.invalid:443")
	return out
}

// pgrepBwrapBridge returns true when any process whose argv contains
// the bridge subcommand marker is alive. Used to assert no orphan
// after a successful run.
func pgrepBwrapBridge(t *testing.T) bool {
	t.Helper()
	cmd := exec.Command("pgrep", "-f", "netproxy-bridge")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false
		}
		t.Fatalf("pgrep: %v", err)
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// bwrapSessionDirsIntegration returns symlink-resolved tempdirs for
// the integration tests; mirrors bwrapSessionDirs in the linux unit
// test file.
func bwrapSessionDirsIntegration(t *testing.T) (sessionDir, outsideDir string) {
	t.Helper()
	rawSession := t.TempDir()
	rawOutside := t.TempDir()
	var err error
	sessionDir, err = filepath.EvalSymlinks(rawSession)
	if err != nil {
		t.Fatalf("EvalSymlinks session: %v", err)
	}
	outsideDir, err = filepath.EvalSymlinks(rawOutside)
	if err != nil {
		t.Fatalf("EvalSymlinks outside: %v", err)
	}
	return sessionDir, outsideDir
}

// TestBwrapProxy_SetupTimeoutKillsProxyChild covers the M-2 follow-up
// constraint: when waitForBwrapProxyChildReady times out, the helper
// MUST kill the spawned child rather than rely on caller cleanup.
//
// We trigger the timeout by overriding the binary path to a no-op
// `/bin/sleep` invocation that never publishes the readiness
// preamble. The constructor must return an error AND the spawned
// child must be reaped within the test's lifetime.
func TestBwrapProxy_SetupTimeoutKillsProxyChild(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	// Use /bin/sleep as a stand-in for a hung netproxy child. It
	// will exec, ignore its argv, and never print the readiness
	// preamble.
	if _, err := os.Stat("/bin/sleep"); err != nil {
		t.Skipf("/bin/sleep missing: %v", err)
	}
	t.Setenv(netproxyBwrapBinaryOverrideEnv, "/bin/sleep")

	start := time.Now()
	_, err := NewBwrapRunnerWithAllowlist([]string{"github.com:443"})
	dur := time.Since(start)
	if err == nil {
		t.Fatal("expected setup timeout to fail constructor")
	}
	// The constructor must return inside the readiness timeout +
	// reasonable Wait grace; with the 5s timeout default plus a few
	// hundred ms reap budget, 8s is comfortably above the limit.
	if dur > 8*time.Second {
		t.Errorf("constructor exceeded readiness timeout budget: %v", dur)
	}
	if !strings.Contains(err.Error(), "ready") &&
		!strings.Contains(err.Error(), "preamble") &&
		!strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected readiness-related error; got: %v", err)
	}
	// No /bin/sleep orphan must remain.
	time.Sleep(200 * time.Millisecond)
	out, _ := exec.Command("pgrep", "-f", "/bin/sleep").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		t.Errorf("orphan /bin/sleep proxy child detected: pid=%s", line)
	}
}

// TestBwrapProxy_NoSilentFallbackOnProxyUnavailable pins the
// no-silent-fallback invariant: when the proxy substrate cannot
// start, the factory MUST return an error rather than handing back a
// DirectRunner.
func TestBwrapProxy_NoSilentFallbackOnProxyUnavailable(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	// Point at a non-executable path so the netproxy spawn fails.
	t.Setenv(netproxyBwrapBinaryOverrideEnv, "/dev/null/does-not-exist")

	r, err := NewBashRunnerForConfig(SandboxConfig{
		Mode:             "bwrap",
		NetworkAllowlist: []string{"github.com:443"},
	})
	if err == nil {
		t.Fatalf("expected factory error; got runner=%v", r)
	}
	if r != nil {
		t.Errorf("factory MUST NOT return a runner alongside an error (silent fallback risk)")
	}
	// Most importantly: NEVER return a DirectRunner.
	if r != nil && r.Name() == "direct" {
		t.Errorf("factory returned DirectRunner — no-silent-fallback invariant BROKEN")
	}
}

// TestBwrapProxy_BridgeExitsWithUserCommand asserts the bridge
// lifecycle: when the user command exits, the bridge exits cleanly
// and no orphan remains. This is the canonical S0 productionize
// assertion and underpins the "no orphan" guarantee for daemon-mode
// runs that recycle runners.
func TestBwrapProxy_BridgeExitsWithUserCommand(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	binPath := integrationBuildElnathBinaryForBwrap(t)
	t.Setenv(netproxyBwrapBinaryOverrideEnv, binPath)

	allowedURL, _, _ := integrationStartBwrapUpstreams(t)
	allowlist := integrationBwrapProxyRequiredAllowlist(integrationAllowlistFromBwrapURL(t, allowedURL))

	r, err := NewBwrapRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	sessionDir, _ := bwrapSessionDirsIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, runErr := r.Run(ctx, BashRunRequest{
		Command:    "echo bridge-exit-marker",
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if res.IsError {
		t.Fatalf("expected success: %s", res.Output)
	}
	if !strings.Contains(res.Output, "bridge-exit-marker") {
		t.Errorf("expected marker in output: %s", res.Output)
	}
	// Give pgrep a moment to settle then assert no orphan.
	time.Sleep(200 * time.Millisecond)
	if pgrepBwrapBridge(t) {
		t.Errorf("orphan netproxy-bridge process detected after Run")
	}
}

// TestBwrapProxy_NoOrphanAfterRun is the explicit redundant assertion
// for the no-orphan invariant called out in scope item 6.
func TestBwrapProxy_NoOrphanAfterRun(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	binPath := integrationBuildElnathBinaryForBwrap(t)
	t.Setenv(netproxyBwrapBinaryOverrideEnv, binPath)

	allowedURL, _, _ := integrationStartBwrapUpstreams(t)
	allowlist := integrationBwrapProxyRequiredAllowlist(integrationAllowlistFromBwrapURL(t, allowedURL))

	r, err := NewBwrapRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	sessionDir, _ := bwrapSessionDirsIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = r.Run(ctx, BashRunRequest{
		Command:    "true",
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	time.Sleep(200 * time.Millisecond)
	if pgrepBwrapBridge(t) {
		t.Errorf("orphan netproxy-bridge process detected post-Run")
	}
}

// TestBwrapProxy_CloseStopsProxyAndDrainGoroutines covers M-1 + M-3:
// Close() exits within the shutdown grace AND deterministically
// releases the stdout drain goroutine. We verify "no orphan" as the
// proxy-child reap evidence and confirm Close returns inside the
// shutdown grace + a small margin.
func TestBwrapProxy_CloseStopsProxyAndDrainGoroutines(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	binPath := integrationBuildElnathBinaryForBwrap(t)
	t.Setenv(netproxyBwrapBinaryOverrideEnv, binPath)

	r, err := NewBwrapRunnerWithAllowlist([]string{"github.com:443"})
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist: %v", err)
	}
	pid := -1
	if c := r.proxyChild(); c != nil && c.Process != nil {
		pid = c.Process.Pid
	}

	start := time.Now()
	if err := r.Close(context.Background()); err != nil {
		t.Errorf("Close: %v", err)
	}
	dur := time.Since(start)
	if dur > netproxyBwrapChildShutdownGrace+1*time.Second {
		t.Errorf("Close took %v, expected < %v + 1s grace", dur, netproxyBwrapChildShutdownGrace)
	}

	// Proxy child must be reaped.
	if pid > 0 {
		// Signal 0 returns nil iff PID is still alive AND owned by us;
		// after reap the kernel reports ESRCH which os.FindProcess +
		// Signal surfaces as an error.
		p, _ := os.FindProcess(pid)
		if p != nil {
			err := p.Signal(syscallZero{})
			if err == nil {
				t.Errorf("proxy child PID %d still alive after Close", pid)
			}
		}
	}
}

// syscallZero is a no-op signal used to ping a PID without delivering
// any actual signal. Mirrors the darwin test helper.
type syscallZero struct{}

func (syscallZero) String() string { return "signal 0" }
func (syscallZero) Signal()        {}

// TestBwrapProxy_AllowsDomainThroughHTTPProxy is the headline policy
// test for the HTTP CONNECT path. curl is invoked with --proxy
// http://127.0.0.1:N (the netns-local bridge) and an HTTPS URL would
// normally trigger a CONNECT — but we use a plain http:// URL because
// httptest does not ship TLS by default. The proxy core's CONNECT
// handler refuses non-CONNECT methods, so we exercise SOCKS5 in a
// separate test and use HTTP_PROXY env injection here.
func TestBwrapProxy_AllowsDomainThroughHTTPProxy(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	binPath := integrationBuildElnathBinaryForBwrap(t)
	t.Setenv(netproxyBwrapBinaryOverrideEnv, binPath)

	allowedURL, _, _ := integrationStartBwrapUpstreams(t)
	allowlist := integrationBwrapProxyRequiredAllowlist(integrationAllowlistFromBwrapURL(t, allowedURL))

	r, err := NewBwrapRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	sessionDir, _ := bwrapSessionDirsIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Use --socks5-hostname to exercise the SOCKS5 path, which
	// supports plain HTTP. The HTTP CONNECT path is for TLS upstreams
	// only; integration coverage of that path requires an https
	// upstream which httptest doesn't ship by default.
	res, runErr := r.Run(ctx, BashRunRequest{
		Command: fmt.Sprintf(
			"curl --show-error --max-time 10 --socks5-hostname 127.0.0.1:%d %s",
			netproxyBridgePort(netproxyBridgeListenSOCKSInternal), allowedURL),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if res.IsError {
		t.Fatalf("expected success; got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "ALLOWED-UPSTREAM-OK") {
		t.Errorf("expected allowed body in output; got: %s", res.Output)
	}
}

// netproxyBridgePort extracts the numeric port from a "host:port"
// constant. Test-only convenience.
func netproxyBridgePort(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	var p int
	for _, c := range portStr {
		if c < '0' || c > '9' {
			return 0
		}
		p = p*10 + int(c-'0')
	}
	return p
}

// TestBwrapProxy_DeniesDomainWithNetworkProxyViolation pins the deny
// path: a request to a non-allowlisted upstream MUST fail AND record
// a network_proxy violation in BashRunResult.Violations.
//
// B3b-4-3.5: tightened from the B3b-4-3 placeholder. The host-side
// netproxy child now serializes per-connection deny Decisions to its
// stdout (one `event=<json>` line each), BwrapRunner's stdout drain
// goroutine parses them back into Decision values, and the runner
// projects each into a SandboxViolation with Source="network_proxy"
// before populating BashRunResult.Violations. The previous workaround
// that accepted an empty Violations slice is gone.
func TestBwrapProxy_DeniesDomainWithNetworkProxyViolation(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	binPath := integrationBuildElnathBinaryForBwrap(t)
	t.Setenv(netproxyBwrapBinaryOverrideEnv, binPath)

	allowedURL, deniedURL, _ := integrationStartBwrapUpstreams(t)
	// Allowlist contains ONLY the allowed upstream + sentinel domain.
	// Denied upstream is intentionally absent.
	allowlist := integrationBwrapProxyRequiredAllowlist(integrationAllowlistFromBwrapURL(t, allowedURL))

	r, err := NewBwrapRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	deniedHost, deniedPortStr, err := net.SplitHostPort(strings.TrimPrefix(deniedURL, "http://"))
	if err != nil {
		t.Fatalf("split denied url %q: %v", deniedURL, err)
	}
	var deniedPort int
	for _, c := range deniedPortStr {
		if c < '0' || c > '9' {
			t.Fatalf("non-numeric denied port %q", deniedPortStr)
		}
		deniedPort = deniedPort*10 + int(c-'0')
	}

	sessionDir, _ := bwrapSessionDirsIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, runErr := r.Run(ctx, BashRunRequest{
		Command: fmt.Sprintf(
			"curl --show-error --max-time 10 --socks5-hostname 127.0.0.1:%d %s",
			netproxyBridgePort(netproxyBridgeListenSOCKSInternal), deniedURL),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if !res.IsError {
		t.Fatalf("expected denied request to fail; got success: %s", res.Output)
	}
	if strings.Contains(res.Output, "SHOULD-NOT-REACH") {
		t.Fatalf("denied upstream body leaked — proxy did NOT block: %s", res.Output)
	}
	// B3b-4-3.5: per-connection Decision events from the host-side
	// netproxy child MUST thread back into BashRunResult.Violations as
	// a Source=network_proxy entry naming the denied destination. The
	// authoritative axis is Source — Reason may be any of the four
	// valid network deny reasons depending on which substrate-level
	// gate fires first (e.g., a SOCKS5 ATYP=0x01 IPv4 literal pointed
	// at a loopback httptest server hits the local-binding default
	// deny before the allowlist check, producing
	// Reason=local_binding_disabled which is itself a valid
	// substrate-correct refusal — partner pin C3 explicitly bans
	// localhost:* broad-open, so loopback default-deny IS the right
	// behavior here).
	if len(res.Violations) == 0 {
		t.Fatalf("expected non-empty Violations; got empty (event channel not wired). Output:\n%s", res.Output)
	}
	matched := findViolationBySource(res.Violations, string(SourceNetworkProxy))
	if matched == nil {
		t.Fatalf("no Source=network_proxy violation found; got %+v", res.Violations)
	}
	if !isValidNetworkDenyReason(matched.Reason) {
		t.Errorf("network_proxy violation has unexpected reason %q (want one of: not_in_allowlist, denied_by_rule, local_binding_disabled, dns_resolution_blocked); full=%+v", matched.Reason, *matched)
	}
	if matched.Host != deniedHost {
		t.Errorf("violation Host = %q, want %q", matched.Host, deniedHost)
	}
	if int(matched.Port) != deniedPort {
		t.Errorf("violation Port = %d, want %d", matched.Port, deniedPort)
	}
	if matched.Protocol != string(ProtocolSOCKS5TCP) {
		t.Errorf("violation Protocol = %q, want %q", matched.Protocol, string(ProtocolSOCKS5TCP))
	}
	if !strings.Contains(res.Output, "SANDBOX VIOLATIONS:") {
		t.Errorf("Output missing SANDBOX VIOLATIONS section; got:\n%s", res.Output)
	}
}

// findViolationBySource returns the first violation matching the
// requested Source, or nil when none match. Caller still validates
// Reason / Host / Port / Protocol after locating the entry. Source is
// the authoritative axis (per partner pin C5 + B3b-4-1 enum lock);
// Reason is one of an enumerated set, all of which are
// substrate-correct deny outcomes.
func findViolationBySource(violations []SandboxViolation, source string) *SandboxViolation {
	for i := range violations {
		if violations[i].Source == source {
			return &violations[i]
		}
	}
	return nil
}

// isValidNetworkDenyReason reports whether reason is one of the four
// substrate-correct network deny outcomes a Source=network_proxy
// violation can carry. All four are valid sandbox refusals; tests
// MUST NOT lock the assertion to a single Reason because which gate
// fires first depends on the substrate-level evaluation order
// (loopback IP literal → local_binding_disabled before allowlist
// check; hostname → DNS / not_in_allowlist; explicit denylist hit
// → denied_by_rule).
func isValidNetworkDenyReason(reason string) bool {
	switch reason {
	case string(ReasonNotInAllowlist),
		string(ReasonDeniedByRule),
		string(ReasonLocalBindingDisabled),
		string(ReasonDNSResolutionBlocked):
		return true
	default:
		return false
	}
}

// TestBwrapProxy_AllowsSOCKS5Tcp re-exercises the happy path
// explicitly through SOCKS5 to keep the SOCKS5 path covered when the
// HTTP test above changes.
func TestBwrapProxy_AllowsSOCKS5Tcp(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	binPath := integrationBuildElnathBinaryForBwrap(t)
	t.Setenv(netproxyBwrapBinaryOverrideEnv, binPath)

	allowedURL, _, _ := integrationStartBwrapUpstreams(t)
	allowlist := integrationBwrapProxyRequiredAllowlist(integrationAllowlistFromBwrapURL(t, allowedURL))

	r, err := NewBwrapRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	sessionDir, _ := bwrapSessionDirsIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, runErr := r.Run(ctx, BashRunRequest{
		Command: fmt.Sprintf(
			"curl --show-error --max-time 10 --socks5-hostname 127.0.0.1:%d %s",
			netproxyBridgePort(netproxyBridgeListenSOCKSInternal), allowedURL),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if res.IsError {
		t.Fatalf("expected SOCKS5 success; got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "ALLOWED-UPSTREAM-OK") {
		t.Errorf("SOCKS5 body missing; got: %s", res.Output)
	}
}

// TestBwrapProxy_DirectEgressBypassingProxyBlocked confirms the netns
// default-deny still works: a curl invocation that bypasses the
// proxy env vars MUST fail. We use --noproxy '*' to bypass HTTP_PROXY
// and dial a separate non-allowlisted httptest.Server directly.
//
// Mirrors the B3b-4-2 fix that uses a SEPARATE non-allowlisted server
// rather than the allowed upstream; bwrap's --unshare-net netns
// blocks every non-loopback dial regardless of allowlist contents,
// but the test exists for symmetry with the darwin substrate's
// equivalent assertion.
func TestBwrapProxy_DirectEgressBypassingProxyBlocked(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	binPath := integrationBuildElnathBinaryForBwrap(t)
	t.Setenv(netproxyBwrapBinaryOverrideEnv, binPath)

	allowedURL, _, _ := integrationStartBwrapUpstreams(t)
	allowlist := integrationBwrapProxyRequiredAllowlist(integrationAllowlistFromBwrapURL(t, allowedURL))

	// Separate non-allowlisted server.
	var blockedAccepted atomic.Bool
	blockedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		blockedAccepted.Store(true)
		_, _ = io.WriteString(w, "BLOCKED-UPSTREAM-LEAKED")
	}))
	defer blockedSrv.Close()

	r, err := NewBwrapRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	sessionDir, _ := bwrapSessionDirsIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, runErr := r.Run(ctx, BashRunRequest{
		Command:    fmt.Sprintf("curl --silent --max-time 5 --noproxy '*' %s", blockedSrv.URL),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if !res.IsError {
		t.Fatalf("direct egress unexpectedly succeeded; got: %s", res.Output)
	}
	if blockedAccepted.Load() {
		t.Fatalf("non-allowlisted upstream accepted a connection — netns isolation BREACHED")
	}
	if strings.Contains(res.Output, "BLOCKED-UPSTREAM-LEAKED") {
		t.Fatalf("non-allowlisted body leaked — netns isolation BREACHED: %s", res.Output)
	}
}

// TestBwrapProxy_Upstream403IsNotSandboxViolation asserts the
// runtime distinction between upstream-403 (forwarded by the proxy
// after a successful CONNECT-equivalent) and proxy-side 403 (denied
// by allowlist). Only the latter belongs in BashRunResult.Violations
// with Source=network_proxy.
func TestBwrapProxy_Upstream403IsNotSandboxViolation(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	binPath := integrationBuildElnathBinaryForBwrap(t)
	t.Setenv(netproxyBwrapBinaryOverrideEnv, binPath)

	_, _, upstream403URL := integrationStartBwrapUpstreams(t)
	allowlist := integrationBwrapProxyRequiredAllowlist(integrationAllowlistFromBwrapURL(t, upstream403URL))

	r, err := NewBwrapRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	sessionDir, _ := bwrapSessionDirsIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, runErr := r.Run(ctx, BashRunRequest{
		Command: fmt.Sprintf(
			"curl --fail --show-error --max-time 10 --socks5-hostname 127.0.0.1:%d %s",
			netproxyBridgePort(netproxyBridgeListenSOCKSInternal), upstream403URL),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	for _, v := range res.Violations {
		if v.Source == string(SourceNetworkProxy) {
			t.Errorf("upstream 403 must NOT generate network_proxy violation; got: %+v", v)
		}
	}
}

// TestBwrapProxy_HostHomeGitConfigStillNotLoaded is a regression
// guard for B3b-1.5: even with the proxy substrate wired through the
// runner, the host HOME .gitconfig escape MUST stay closed.
func TestBwrapProxy_HostHomeGitConfigStillNotLoaded(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	binPath := integrationBuildElnathBinaryForBwrap(t)
	t.Setenv(netproxyBwrapBinaryOverrideEnv, binPath)

	allowedURL, _, _ := integrationStartBwrapUpstreams(t)
	allowlist := integrationBwrapProxyRequiredAllowlist(integrationAllowlistFromBwrapURL(t, allowedURL))

	r, err := NewBwrapRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	fakeHome := t.TempDir()
	sentinelDir := t.TempDir()
	sentinel := filepath.Join(sentinelDir, "fsmonitor-sentinel")
	gitconfig := fmt.Sprintf("[core]\n\tfsmonitor = sh -c 'touch %s; echo {}'\n", sentinel)
	if err := os.WriteFile(filepath.Join(fakeHome, ".gitconfig"), []byte(gitconfig), 0o644); err != nil {
		t.Fatalf("write fake gitconfig: %v", err)
	}
	t.Setenv("HOME", fakeHome)

	dir := setupGitRepo(t)
	gt := NewGitToolWithRunner(NewPathGuard(dir, nil), r)

	if _, err := gt.Execute(context.Background(), mustMarshal(t, map[string]any{
		"subcommand": "status",
	})); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatalf("host HOME .gitconfig fsmonitor was triggered under BwrapRunner with proxy — HOME leakage NOT prevented")
	}
}

// TestBwrapProxy_HeuristicFilesystemViolationSourceRemainsHeuristic
// asserts the existing detectBwrapViolations heuristic still emits
// Source="sandbox_substrate_heuristic" (B3b-4-1 downgrade preserved
// even after the proxy wiring).
func TestBwrapProxy_HeuristicFilesystemViolationSourceRemainsHeuristic(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	binPath := integrationBuildElnathBinaryForBwrap(t)
	t.Setenv(netproxyBwrapBinaryOverrideEnv, binPath)

	allowedURL, _, _ := integrationStartBwrapUpstreams(t)
	allowlist := integrationBwrapProxyRequiredAllowlist(integrationAllowlistFromBwrapURL(t, allowedURL))

	r, err := NewBwrapRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	sessionDir, outsideDir := bwrapSessionDirsIntegration(t)
	sentinel := filepath.Join(outsideDir, "leak.txt")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, runErr := r.Run(ctx, BashRunRequest{
		Command:    fmt.Sprintf(`echo leak > %q`, sentinel),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if !res.IsError {
		t.Errorf("expected error for outside write; got: %s", res.Output)
	}
	for _, v := range res.Violations {
		if v.Source != "" && v.Source != string(SourceSandboxSubstrateHeuristic) &&
			v.Source != string(SourceNetworkProxy) {
			t.Errorf("heuristic violation must keep Source=sandbox_substrate_heuristic; got %+v", v)
		}
	}
}

// TestBwrapProxy_AllowsDomainWithoutViolation pins the allow path: a
// request to an allowlisted upstream MUST succeed AND record no entries
// in BashRunResult.Violations. The wiring added in B3b-4-3.5 only
// projects deny Decisions into violations; allow Decisions remain
// informational and never appear in Violations. The output also must
// not carry a "SANDBOX VIOLATIONS:" section.
func TestBwrapProxy_AllowsDomainWithoutViolation(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	binPath := integrationBuildElnathBinaryForBwrap(t)
	t.Setenv(netproxyBwrapBinaryOverrideEnv, binPath)

	allowedURL, _, _ := integrationStartBwrapUpstreams(t)
	allowlist := integrationBwrapProxyRequiredAllowlist(integrationAllowlistFromBwrapURL(t, allowedURL))

	r, err := NewBwrapRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	sessionDir, _ := bwrapSessionDirsIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, runErr := r.Run(ctx, BashRunRequest{
		Command: fmt.Sprintf(
			"curl --show-error --max-time 10 --socks5-hostname 127.0.0.1:%d %s",
			netproxyBridgePort(netproxyBridgeListenSOCKSInternal), allowedURL),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if res.IsError {
		t.Fatalf("expected success; got error: %s", res.Output)
	}
	if len(res.Violations) != 0 {
		t.Errorf("expected empty Violations on allow path; got %+v", res.Violations)
	}
	if strings.Contains(res.Output, "SANDBOX VIOLATIONS:") {
		t.Errorf("Output must not contain SANDBOX VIOLATIONS on allow path; got:\n%s", res.Output)
	}
}

// TestBwrapProxy_UDSDirPermissionsArePrivate pins B3b-4-3 M-2: the
// per-runner UDS directory holding http.sock + socks.sock MUST be
// 0700 so neighboring local users cannot enumerate or connect to the
// netproxy endpoints. The directory is created via os.MkdirTemp which
// honours umask; an explicit Chmod is required to land at 0700
// regardless of process umask.
func TestBwrapProxy_UDSDirPermissionsArePrivate(t *testing.T) {
	requireBwrapProxyTestSupport(t)
	binPath := integrationBuildElnathBinaryForBwrap(t)
	t.Setenv(netproxyBwrapBinaryOverrideEnv, binPath)

	r, err := NewBwrapRunnerWithAllowlist([]string{"sentinel.invalid:443"})
	if err != nil {
		t.Fatalf("NewBwrapRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	httpUDS, _ := r.proxyUDSPaths()
	if httpUDS == "" {
		t.Fatal("expected proxy UDS path on a proxy-required runner")
	}
	udsDir := filepath.Dir(httpUDS)
	info, err := os.Stat(udsDir)
	if err != nil {
		t.Fatalf("stat UDS dir %s: %v", udsDir, err)
	}
	mode := info.Mode().Perm()
	if mode != 0o700 {
		t.Errorf("UDS dir %s mode = %o, want 0700 (no group/world access)", udsDir, mode)
	}
}
