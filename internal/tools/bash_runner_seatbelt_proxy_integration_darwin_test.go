//go:build darwin && integration

package tools

import (
	"context"
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

// B3b-4-2 Phase F: end-to-end macOS Seatbelt + netproxy integration
// tests. Build-tagged darwin && integration so they run only when the
// developer (or future CI) opts in via `go test -tags=integration`.
//
// What this file proves:
//
//  1. The SeatbeltRunner self-execs the elnath binary as the netproxy
//     child, captures bound ports, and renders a working SBPL profile.
//  2. A bash command run through the runner sees HTTP_PROXY env vars
//     pointing at the bound proxy ports.
//  3. An HTTP request to an allowlisted upstream succeeds via the
//     proxy.
//  4. An HTTP request to a denied upstream fails AND records a
//     network_proxy violation in BashRunResult.Violations.
//  5. Direct egress (bypassing the proxy) is blocked by the SBPL
//     default-deny baseline.
//  6. Upstream HTTP 403 from a real upstream is forwarded as IsError
//     but does NOT register as a sandbox violation.
//  7. Newline-bearing crafted Host renders safely (defense in depth
//     beyond the unit test's render-boundary scrubbing).
//
// These tests require an actual /usr/bin/sandbox-exec binary and a
// real darwin host. They build a fresh elnath binary into a tempdir
// each test so the runner has a stable absolute path to self-exec.

// integrationRequireDarwinSeatbelt skips when the runtime substrate
// is missing — the file is already build-tagged darwin so the
// runtime check guards against a darwin host without sandbox-exec.
func integrationRequireDarwinSeatbelt(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("integration test darwin-only")
	}
	if _, err := os.Stat(seatbeltBinary); err != nil {
		t.Skipf("sandbox-exec missing at %s: %v", seatbeltBinary, err)
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skipf("curl missing: %v", err)
	}
}

// integrationBuildElnathBinary compiles a fresh elnath binary so the
// SeatbeltRunner has a stable absolute path to self-exec. Production
// uses os.Executable() which returns the elnath binary; tests cannot
// because go test compiles the test harness as `tools.test`, not the
// elnath binary.
func integrationBuildElnathBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "elnath-integration")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/elnath")
	cmd.Dir = repoRootDarwin(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build elnath: %v\n%s", err, out)
	}
	return binPath
}

// integrationStartUpstreams spins up two tiny httptest.Servers: one
// "allowed" upstream that the test allowlist will permit, and one
// "denied" upstream that the test will not. Both run on 127.0.0.1
// loopback ephemeral ports. Bodies are unique so a swapped routing
// path is detectable.
func integrationStartUpstreams(t *testing.T) (allowedURL, deniedURL, denied403URL string) {
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

// integrationAllowlistFromURL builds allowlist entries matching the
// httptest.Server's bind. httptest binds 127.0.0.1 + ephemeral port,
// so the entry is `127.0.0.1:port`. The runner's
// splitAllowlistByProxyNeed treats this as loopback-only (no proxy
// needed) — to force proxy-required mode, callers add a sentinel
// domain entry alongside.
func integrationAllowlistFromURL(t *testing.T, urls ...string) []string {
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

// integrationProxyRequiredAllowlist forces the runner into proxy-
// required mode by adding a sentinel domain entry alongside the
// httptest 127.0.0.1 entries. The proxy-required mode is what
// triggers child-spawn + SBPL per-port pinning + env injection. The
// sentinel domain is never connected to (the test only curls
// 127.0.0.1 URLs) so it does not affect the assertion.
func integrationProxyRequiredAllowlist(loopbackEntries []string) []string {
	out := append([]string{}, loopbackEntries...)
	out = append(out, "sentinel.invalid:443")
	return out
}

// TestSeatbeltProxyIntegration_AllowedDomainHTTPRequestSucceeds is
// the headline integration test: an HTTP request to an allowlisted
// upstream succeeds via the proxy, with a working sandbox-exec wrap.
//
// curl is invoked with --socks5-hostname to force the SOCKS5 proxy
// path. HTTP_PROXY honored by curl issues a forward-proxy GET which
// the netproxy core (CONNECT-only) returns 405 for; the test
// deliberately uses SOCKS5 for HTTP so the assertion is a clean
// allow-vs-deny signal independent of HTTP forward-proxy semantics
// (which Codex and our core both decline by design).
func TestSeatbeltProxyIntegration_AllowedDomainHTTPRequestSucceeds(t *testing.T) {
	integrationRequireDarwinSeatbelt(t)
	binPath := integrationBuildElnathBinary(t)
	t.Setenv(netproxyBinaryOverrideEnv, binPath)

	allowedURL, _, _ := integrationStartUpstreams(t)
	allowlist := integrationProxyRequiredAllowlist(integrationAllowlistFromURL(t, allowedURL))

	r, err := NewSeatbeltRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist(%v): %v", allowlist, err)
	}
	defer r.Close(context.Background())

	if r.httpProxyPort() == 0 {
		t.Fatalf("proxy http port not bound; runner did not enter proxy-required mode")
	}
	socksPort := r.socksProxyPort()

	sessionDir, _ := seatbeltSessionDirs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, runErr := r.Run(ctx, BashRunRequest{
		Command: fmt.Sprintf(
			"curl --show-error --max-time 10 --socks5-hostname 127.0.0.1:%d %s",
			socksPort, allowedURL),
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

// TestSeatbeltProxyIntegration_DeniedDomainHTTPRequestFailsWithViolation
// asserts the deny path: not allowlisted → curl fails. The proxy
// rejects the SOCKS5 CONNECT with reply code 0x02 (connection not
// allowed by ruleset).
//
// Note on Violations: B3b-4-2 wires only env injection + sandbox
// profile, NOT a structured channel from the proxy back to the
// runner — Decisions stay inside the proxy child process. v42-1a
// adds the deny-projection wiring; the dedicated assertions live in
// TestSeatbeltProxyIntegration_DeniedDomainPopulatesNetworkProxyViolation
// below. This test stays scoped to the body-leak and IsError checks
// so the v42-1a-era test name stays unchanged.
func TestSeatbeltProxyIntegration_DeniedDomainHTTPRequestFailsWithViolation(t *testing.T) {
	integrationRequireDarwinSeatbelt(t)
	binPath := integrationBuildElnathBinary(t)
	t.Setenv(netproxyBinaryOverrideEnv, binPath)

	allowedURL, deniedURL, _ := integrationStartUpstreams(t)
	allowlist := integrationAllowlistFromURL(t, allowedURL)

	r, err := NewSeatbeltRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())
	socksPort := r.socksProxyPort()

	sessionDir, _ := seatbeltSessionDirs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, runErr := r.Run(ctx, BashRunRequest{
		Command: fmt.Sprintf(
			"curl --show-error --max-time 10 --socks5-hostname 127.0.0.1:%d %s",
			socksPort, deniedURL),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	// curl exits non-zero when the SOCKS5 CONNECT is denied.
	if !res.IsError {
		t.Fatalf("expected denied request to fail; got success: %s", res.Output)
	}
	if strings.Contains(res.Output, "SHOULD-NOT-REACH") {
		t.Fatalf("denied upstream body leaked into output — proxy did NOT block: %s", res.Output)
	}
}

// TestSeatbeltProxyIntegration_DeniedDomainPopulatesNetworkProxyViolation
// pins the v42-1a deny-projection contract: when the proxy rejects a
// SOCKS5 CONNECT to a non-allowlisted host, the per-connection Decision
// event MUST thread back into BashRunResult.Violations as a
// Source=network_proxy entry naming the denied destination.
//
// Mirrors the Linux test
// TestBwrapProxy_DeniesDomainWithNetworkProxyViolation in
// bash_runner_bwrap_proxy_integration_linux_test.go:466.
//
// Authoritative axis is Source — Reason may be any of the four valid
// network deny reasons depending on which substrate-level gate fires
// first (loopback IP literal hits local_binding_disabled before the
// allowlist check, hostname hits not_in_allowlist or
// dns_resolution_blocked, denylist hit produces denied_by_rule).
// Partner pin C3 explicitly bans localhost:* broad-open; loopback
// default-deny IS substrate-correct here.
func TestSeatbeltProxyIntegration_DeniedDomainPopulatesNetworkProxyViolation(t *testing.T) {
	integrationRequireDarwinSeatbelt(t)
	binPath := integrationBuildElnathBinary(t)
	t.Setenv(netproxyBinaryOverrideEnv, binPath)

	allowedURL, deniedURL, _ := integrationStartUpstreams(t)
	allowlist := integrationProxyRequiredAllowlist(integrationAllowlistFromURL(t, allowedURL))

	r, err := NewSeatbeltRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())
	socksPort := r.socksProxyPort()

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

	sessionDir, _ := seatbeltSessionDirs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, runErr := r.Run(ctx, BashRunRequest{
		Command: fmt.Sprintf(
			"curl --show-error --max-time 10 --socks5-hostname 127.0.0.1:%d %s",
			socksPort, deniedURL),
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
	if len(res.Violations) == 0 {
		t.Fatalf("expected non-empty Violations; got empty (deny projection not wired). Output:\n%s", res.Output)
	}
	matched := findSeatbeltViolationBySource(res.Violations, string(SourceNetworkProxy))
	if matched == nil {
		t.Fatalf("no Source=network_proxy violation found; got %+v", res.Violations)
	}
	if !isValidSeatbeltNetworkDenyReason(matched.Reason) {
		t.Errorf("network_proxy violation has unexpected reason %q (want one of: not_in_allowlist, denied_by_rule, local_binding_disabled, dns_resolution_blocked); full=%+v",
			matched.Reason, *matched)
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

// findSeatbeltViolationBySource returns the first violation matching
// the requested Source, or nil when none match. Source is the
// authoritative axis (per partner pin C5 + B3b-4-1 enum lock); Reason
// is one of an enumerated set, all of which are substrate-correct deny
// outcomes.
//
// Suffix differentiates this helper from the bwrap test counterpart
// findViolationBySource (linux-tagged file) so the two integration
// suites can co-exist in the same package without symbol collision.
func findSeatbeltViolationBySource(violations []SandboxViolation, source string) *SandboxViolation {
	for i := range violations {
		if violations[i].Source == source {
			return &violations[i]
		}
	}
	return nil
}

// isValidSeatbeltNetworkDenyReason reports whether reason is one of
// the four substrate-correct network deny outcomes a
// Source=network_proxy violation can carry. Tests MUST NOT lock the
// assertion to a single Reason because which gate fires first depends
// on the substrate-level evaluation order.
func isValidSeatbeltNetworkDenyReason(reason string) bool {
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

// TestSeatbeltProxyIntegration_DirectEgressToNonAllowlistedPortBlocked
// confirms SBPL default-deny by attempting direct egress to a
// loopback port that is NOT in the allowlist, with HTTP_PROXY env
// vars bypassed via `curl --noproxy '*'`. The allowed upstream's
// port IS allowlisted (explicit per-port entry — see
// `TestSeatbeltProxyIntegration_ExplicitLocalPortAllowsDirectEgress`
// for that semantic), so the test routes the bypass attempt to a
// SEPARATE loopback server that the allowlist intentionally omits.
//
// Why the test was rewritten in B3b-4-2: Seatbelt SBPL grammar is
// `(allow network-outbound (remote ip "host:port"))` per entry. It
// cannot distinguish "this localhost:port is the proxy" from "this
// localhost:port is a user-authorized local service". Therefore an
// explicit `127.0.0.1:N` allowlist entry intentionally permits
// direct egress to that exact port. Default-deny enforcement is
// observed against ports the user did NOT allowlist.
func TestSeatbeltProxyIntegration_DirectEgressToNonAllowlistedPortBlocked(t *testing.T) {
	integrationRequireDarwinSeatbelt(t)
	binPath := integrationBuildElnathBinary(t)
	t.Setenv(netproxyBinaryOverrideEnv, binPath)

	allowedURL, _, _ := integrationStartUpstreams(t)
	allowlist := integrationProxyRequiredAllowlist(integrationAllowlistFromURL(t, allowedURL))

	// Separate server on a port that is NOT added to the allowlist.
	// SBPL default-deny must block direct egress to it.
	var blockedAccepted atomic.Bool
	blockedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		blockedAccepted.Store(true)
		_, _ = io.WriteString(w, "BLOCKED-UPSTREAM-LEAKED")
	}))
	defer blockedSrv.Close()

	r, err := NewSeatbeltRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	sessionDir, _ := seatbeltSessionDirs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// curl --noproxy '*' bypasses HTTP_PROXY/ALL_PROXY env vars and
	// dials the destination directly. The destination is NOT in the
	// allowlist, so SBPL must refuse.
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
		t.Fatalf("direct egress to non-allowlisted port unexpectedly succeeded; got: %s", res.Output)
	}
	if blockedAccepted.Load() {
		t.Fatalf("non-allowlisted upstream accepted a connection — SBPL default-deny BREACHED")
	}
	if strings.Contains(res.Output, "BLOCKED-UPSTREAM-LEAKED") {
		t.Fatalf("non-allowlisted upstream body leaked into output — SBPL default-deny BREACHED: %s", res.Output)
	}
}

// TestSeatbeltProxyIntegration_ExplicitLocalPortAllowsDirectEgress
// pins the partner-pin C3 semantics: an explicit per-port loopback
// allowlist entry IS user opt-in to direct egress on that port.
// Seatbelt SBPL cannot distinguish proxy port from user-authorized
// local-service port, and the locked design intentionally permits
// the user-named port. The broad `localhost:*` footgun remains
// forbidden (covered by `TestSeatbeltProfile_NeverEmitsLocalhostStarWildcard`).
func TestSeatbeltProxyIntegration_ExplicitLocalPortAllowsDirectEgress(t *testing.T) {
	integrationRequireDarwinSeatbelt(t)
	binPath := integrationBuildElnathBinary(t)
	t.Setenv(netproxyBinaryOverrideEnv, binPath)

	allowedURL, _, _ := integrationStartUpstreams(t)
	allowlist := integrationProxyRequiredAllowlist(integrationAllowlistFromURL(t, allowedURL))

	r, err := NewSeatbeltRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())

	sessionDir, _ := seatbeltSessionDirs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// allowedURL's port IS in the allowlist as an explicit per-port
	// entry. --noproxy '*' bypasses HTTP_PROXY; SBPL must still allow
	// because the destination port is user-allowlisted.
	res, runErr := r.Run(ctx, BashRunRequest{
		Command:    fmt.Sprintf("curl --silent --max-time 5 --noproxy '*' %s", allowedURL),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if res.IsError {
		t.Fatalf("explicit per-port direct egress unexpectedly blocked; got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "ALLOWED-UPSTREAM-OK") {
		t.Fatalf("expected upstream body in output; got: %s", res.Output)
	}
}

// TestSeatbeltProxyIntegration_UpstreamHTTP403IsNotASandboxViolation
// asserts the runtime distinction between an upstream-side 403
// (forwarded by the proxy after a successful CONNECT) and a
// proxy-side 403 (denied by allowlist). Only the latter populates
// BashRunResult.Violations.
func TestSeatbeltProxyIntegration_UpstreamHTTP403IsNotASandboxViolation(t *testing.T) {
	integrationRequireDarwinSeatbelt(t)
	binPath := integrationBuildElnathBinary(t)
	t.Setenv(netproxyBinaryOverrideEnv, binPath)

	_, _, upstream403URL := integrationStartUpstreams(t)
	allowlist := integrationAllowlistFromURL(t, upstream403URL)

	r, err := NewSeatbeltRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}
	defer r.Close(context.Background())
	socksPort := r.socksProxyPort()

	sessionDir, _ := seatbeltSessionDirs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// curl --fail surfaces the upstream 403 as a non-zero exit code.
	res, runErr := r.Run(ctx, BashRunRequest{
		Command: fmt.Sprintf(
			"curl --fail --show-error --max-time 10 --socks5-hostname 127.0.0.1:%d %s",
			socksPort, upstream403URL),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	// The CONNECT was permitted by the proxy (upstream is
	// allowlisted) — the proxy does not record a deny decision for
	// the upstream's own 403. SandboxViolations from the substrate
	// stderr heuristic may or may not fire depending on macOS
	// version; we assert NO network_proxy entry appears.
	for _, v := range res.Violations {
		if v.Source == string(SourceNetworkProxy) {
			t.Errorf("upstream 403 must NOT generate network_proxy violation; got: %+v", v)
		}
	}
}

// TestSeatbeltProxyIntegration_NewlineCraftedHostRendersSafely is the
// E2E version of the unit test in
// bash_runner_seatbelt_proxy_darwin_test.go. It constructs a
// SandboxViolation slice manually (since we don't have a wire-level
// way to inject newlines into a real SOCKS5 CONNECT host bytes) and
// verifies appendViolationsSection scrubs them.
func TestSeatbeltProxyIntegration_NewlineCraftedHostRendersSafely(t *testing.T) {
	v := []SandboxViolation{
		{
			Source:   string(SourceNetworkProxy),
			Host:     "evil.example\nFAKE-VIOLATION: line 2",
			Port:     443,
			Protocol: string(ProtocolHTTPSConnect),
			Reason:   string(ReasonNotInAllowlist),
		},
	}
	body := appendViolationsSection("BASH RESULT\n", v)
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "FAKE-VIOLATION:") && !strings.HasPrefix(line, "- ") {
			t.Errorf("crafted host injected new line: %q", line)
		}
	}
}
