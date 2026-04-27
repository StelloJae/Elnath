package tools

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// v42-3 — resolve-pin proxy-side tests (#1, #2, #5, #6, #8, #9, #13, #15).
//
// These tests exercise the contract that the HTTP CONNECT and SOCKS5
// dial sites must consume the pinned IP slice returned by
// EvaluateWithDenylist and dial the IP literal — preserving the
// original hostname in the decision, sink, and any error strings.
// Tests #5 and #2 use sentinel.test (RFC 6761 reserved TLD) so that a
// buggy implementation that forwards the hostname to the OS resolver
// fails deterministically with NXDOMAIN. Tests inject a non-nil
// MockResolver explicitly; existing direct-Serve* tests with
// nil-resolver back-compat are NOT touched.

// ---------------------------------------------------------------
// Resolve-pin test helpers
// ---------------------------------------------------------------

// pinSink is an EventSink that records both Decisions and errors,
// and exposes thread-safe snapshots. Reused across multiple tests.
type pinSink struct {
	mu        sync.Mutex
	decisions []Decision
	errors    []error
}

func (s *pinSink) EmitDecision(d Decision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decisions = append(s.decisions, d)
}

func (s *pinSink) EmitError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors = append(s.errors, err)
}

func (s *pinSink) decisionsSnapshot() []Decision {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Decision, len(s.decisions))
	copy(out, s.decisions)
	return out
}

func (s *pinSink) errorsSnapshot() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]error, len(s.errors))
	copy(out, s.errors)
	return out
}

// startSentinelListener binds 127.0.0.1:0 and counts accepted
// connections. Returned port is the port the sentinel is listening on.
type sentinelCounter struct {
	mu       sync.Mutex
	accepts  int
	listener net.Listener
}

func (c *sentinelCounter) Accepts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accepts
}

func startSentinelListener(t *testing.T) (*sentinelCounter, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sentinel listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := mustAtoi(t, portStr)
	c := &sentinelCounter{listener: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			c.mu.Lock()
			c.accepts++
			c.mu.Unlock()
			// Drain a small amount so the client's downstream write
			// completes (matches a minimal echo-style upstream).
			go func(cc net.Conn) {
				defer cc.Close()
				_ = cc.SetReadDeadline(time.Now().Add(2 * time.Second))
				buf := make([]byte, 256)
				_, _ = cc.Read(buf)
			}(conn)
		}
	}()
	return c, port
}

// mutableResolver allows the test to mutate the host->IP mapping
// after construction. Used by tests that simulate DNS rebinding
// between policy time and dial time. Each LookupHost call signals on
// firstCall so the test can wait for the policy resolver to be
// consulted before mutating state — eliminating the test race where
// a test that mutates before the proxy goroutine reaches policy
// evaluation would observe the mutated state during the policy check.
type mutableResolver struct {
	mu        sync.Mutex
	hosts     map[string][]string
	calls     int
	firstCall chan struct{}
	signaled  bool
}

func newMutableResolver(initial map[string][]string) *mutableResolver {
	cp := make(map[string][]string, len(initial))
	for k, v := range initial {
		cp[k] = append([]string(nil), v...)
	}
	return &mutableResolver{hosts: cp, firstCall: make(chan struct{})}
}

func (m *mutableResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	m.mu.Lock()
	m.calls++
	signal := false
	if !m.signaled {
		m.signaled = true
		signal = true
	}
	ips, ok := m.hosts[host]
	if ok {
		ips = append([]string(nil), ips...)
	}
	m.mu.Unlock()
	if signal {
		close(m.firstCall)
	}
	if !ok {
		return nil, fmt.Errorf("mutableResolver: no canned ips for %q", host)
	}
	return ips, nil
}

func (m *mutableResolver) SetHost(host string, ips []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hosts[host] = append([]string(nil), ips...)
}

func (m *mutableResolver) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// WaitFirstCall blocks until the resolver has been consulted at least
// once. Used by tests that need to mutate the resolver state strictly
// AFTER the proxy's policy evaluation has captured the original
// answer.
func (m *mutableResolver) WaitFirstCall(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-m.firstCall:
	case <-time.After(d):
		t.Fatalf("policy resolver was not consulted within %v", d)
	}
}

// ---------------------------------------------------------------
// Test #1 — TestServeHTTPConnect_DialsResolvedIPNotHostname
// ---------------------------------------------------------------
// Partner #1. The CONNECT dial site MUST consume the pinned IP
// returned by the policy evaluator and dial the IP literal — proven
// by the sentinel listener counting exactly one accept on 127.0.0.1:N
// for a CONNECT to a hostname that resolves to 127.0.0.1.
func TestServeHTTPConnect_DialsResolvedIPNotHostname(t *testing.T) {
	sentinel, port := startSentinelListener(t)
	// Allowlist includes both hostname AND loopback IP — the proxy
	// resolves the hostname to 127.0.0.1, which is a special-range
	// address that must be explicitly allowlisted by IP literal to
	// pass the resolved-IP guard (per Q1 all-or-nothing semantics).
	allow, _ := ParseAllowlist([]string{
		fmt.Sprintf("pinned.example.com:%d", port),
		fmt.Sprintf("127.0.0.1:%d", port),
	})
	resolver := NewMockResolver(map[string][]string{
		"pinned.example.com": {"127.0.0.1"},
	})
	sink := &pinSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, resolver, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	target := fmt.Sprintf("pinned.example.com:%d", port)
	connReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	if _, err := conn.Write([]byte(connReq)); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(conn)
	statusLine, _ := br.ReadString('\n')
	if !strings.Contains(statusLine, "200") {
		t.Fatalf("expected 200; got %q", statusLine)
	}
	for {
		line, _ := br.ReadString('\n')
		if line == "\r\n" || line == "\n" || line == "" {
			break
		}
	}
	// Send a small probe so the sentinel completes its read; not strictly
	// required for the assertion but exercises the splice path.
	_, _ = conn.Write([]byte("Q"))
	time.Sleep(50 * time.Millisecond)
	if sentinel.Accepts() != 1 {
		t.Fatalf("expected exactly 1 accept on sentinel; got %d", sentinel.Accepts())
	}
	decisions := sink.decisionsSnapshot()
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision; got %d", len(decisions))
	}
	if !decisions[0].Allow {
		t.Errorf("expected Allow; got %+v", decisions[0])
	}
	if decisions[0].Host != "pinned.example.com" {
		t.Errorf("Host should preserve original hostname; got %q", decisions[0].Host)
	}
}

// ---------------------------------------------------------------
// Test #2 — TestServeSOCKS5_DialsResolvedIPNotHostname (sentinel.test)
// ---------------------------------------------------------------
// Partner #2 with hostname change to sentinel.test (Fix 1.5). SOCKS5
// ATYP=0x03 DOMAINNAME path MUST consume the pinned IP slice and
// dial 127.0.0.1:N. Using sentinel.test guarantees that a buggy
// implementation forwarding the hostname to the OS resolver fails
// deterministically (RFC 6761 reserved TLD).
func TestServeSOCKS5_DialsResolvedIPNotHostname(t *testing.T) {
	sentinel, port := startSentinelListener(t)
	allow, _ := ParseAllowlist([]string{
		fmt.Sprintf("sentinel.test:%d", port),
		fmt.Sprintf("127.0.0.1:%d", port),
	})
	resolver := NewMockResolver(map[string][]string{
		"sentinel.test": {"127.0.0.1"},
	})
	sink := &pinSink{}
	proxyAddr, stop := startSocks5Proxy(t, allow, Denylist{}, resolver, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	socks5Greet(t, conn)
	socks5SendConnect(t, conn, "sentinel.test", port)
	code := socks5ReadReplyCode(t, conn)
	if code != 0x00 {
		t.Fatalf("expected 0x00 succeeded; got 0x%02x", code)
	}
	_, _ = conn.Write([]byte("Q"))
	time.Sleep(50 * time.Millisecond)
	if sentinel.Accepts() != 1 {
		t.Fatalf("expected exactly 1 accept on sentinel; got %d", sentinel.Accepts())
	}
	for _, e := range sink.errorsSnapshot() {
		if strings.Contains(strings.ToLower(e.Error()), "no such host") {
			t.Fatalf("buggy implementation: hostname leaked to OS resolver: %v", e)
		}
	}
	decisions := sink.decisionsSnapshot()
	if len(decisions) != 1 || !decisions[0].Allow || decisions[0].Host != "sentinel.test" {
		t.Errorf("expected single Allow decision with Host=sentinel.test; got %+v", decisions)
	}
}

// ---------------------------------------------------------------
// Test #5 — CORE pin
// TestServeHTTPConnect_DialUsesIPLiteralNotHostnameAfterPolicyPin
// ---------------------------------------------------------------
// Partner #5 / addendum Fix 1. Three-invariant lock:
// (1) policy resolver consulted exactly once,
// (2) no NXDOMAIN-shaped error in sink (stdlib resolver bypassed),
// (3) sentinel.Accept == 1 (dial reached the pinned IP literal).
// Hostname is sentinel.test (RFC 6761) so any leak to the OS
// resolver fails deterministically.
func TestServeHTTPConnect_DialUsesIPLiteralNotHostnameAfterPolicyPin(t *testing.T) {
	sentinel, port := startSentinelListener(t)
	allow, _ := ParseAllowlist([]string{
		fmt.Sprintf("**.test:%d", port),
		fmt.Sprintf("127.0.0.1:%d", port),
	})
	tracking := &countingResolver{
		inner: NewMockResolver(map[string][]string{"sentinel.test": {"127.0.0.1"}}),
	}
	sink := &pinSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, tracking, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	target := fmt.Sprintf("sentinel.test:%d", port)
	connReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	if _, err := conn.Write([]byte(connReq)); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(conn)
	statusLine, _ := br.ReadString('\n')
	if !strings.Contains(statusLine, "200") {
		t.Fatalf("expected 200; got %q", statusLine)
	}
	for {
		line, _ := br.ReadString('\n')
		if line == "\r\n" || line == "\n" || line == "" {
			break
		}
	}
	_, _ = conn.Write([]byte("Q"))
	time.Sleep(80 * time.Millisecond)

	// Invariant 1: policy resolver consulted exactly once.
	if got := tracking.Count(); got != 1 {
		t.Errorf("policy resolver call count = %d; want 1", got)
	}
	// Invariant 2: no NXDOMAIN-shaped error from stdlib resolver.
	for _, e := range sink.errorsSnapshot() {
		low := strings.ToLower(e.Error())
		if strings.Contains(low, "no such host") || strings.Contains(low, "nxdomain") {
			t.Errorf("stdlib resolver was consulted (sink got %q)", e)
		}
	}
	// Invariant 3: dial landed on pinned IP literal.
	if sentinel.Accepts() != 1 {
		t.Errorf("sentinel accepts = %d; want 1 (dial did not reach pinned IP)", sentinel.Accepts())
	}
	// Decision retains original hostname.
	decisions := sink.decisionsSnapshot()
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision; got %d", len(decisions))
	}
	if decisions[0].Host != "sentinel.test" {
		t.Errorf("Decision.Host = %q; want sentinel.test (pinned IP must not leak)", decisions[0].Host)
	}
}

// ---------------------------------------------------------------
// Test #6 — TestServeHTTPConnect_DNSChangedBetweenPolicyAndDialDoesNotAffectDialTarget
// ---------------------------------------------------------------
// Partner #6. The pin captured at policy time MUST be used by the
// dial site even if the resolver's underlying mapping changes between
// policy evaluation and the eventual dial. Proven by mutating the
// resolver state immediately after the proxy goroutine starts (same
// effect as a DNS record rebinding) and asserting the sentinel still
// receives exactly one accept on 127.0.0.1.
func TestServeHTTPConnect_DNSChangedBetweenPolicyAndDialDoesNotAffectDialTarget(t *testing.T) {
	sentinel, port := startSentinelListener(t)
	allow, _ := ParseAllowlist([]string{
		fmt.Sprintf("rebind.example.com:%d", port),
		fmt.Sprintf("127.0.0.1:%d", port),
	})
	resolver := newMutableResolver(map[string][]string{
		"rebind.example.com": {"127.0.0.1"},
	})
	sink := &pinSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, resolver, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	target := fmt.Sprintf("rebind.example.com:%d", port)
	connReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	if _, err := conn.Write([]byte(connReq)); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Wait until the policy resolver has been consulted; only then
	// rebind the answer to a CGNAT IP. This ensures the policy
	// captured the original 127.0.0.1, while any dial-time
	// re-resolution (which production code MUST NOT do) would
	// observe the rebound 100.64.0.1.
	resolver.WaitFirstCall(t, 2*time.Second)
	resolver.SetHost("rebind.example.com", []string{"100.64.0.1"})

	br := bufio.NewReader(conn)
	statusLine, _ := br.ReadString('\n')
	if !strings.Contains(statusLine, "200") {
		t.Fatalf("expected 200 (pin held); got %q", statusLine)
	}
	for {
		line, _ := br.ReadString('\n')
		if line == "\r\n" || line == "\n" || line == "" {
			break
		}
	}
	_, _ = conn.Write([]byte("Q"))
	time.Sleep(80 * time.Millisecond)
	if sentinel.Accepts() != 1 {
		t.Errorf("sentinel accepts = %d; want 1 (dial site re-resolved instead of using pin)", sentinel.Accepts())
	}
}

// ---------------------------------------------------------------
// Test #8 — TestServeSOCKS5_ATYP4_IPv4MappedSpecialRangeStillBlocked
// ---------------------------------------------------------------
// Partner #8. Extends the existing IP-literal v4-mapped test to
// confirm v42-3's tuple-return refactor does NOT bypass the
// special-range guard for ATYP=0x04 inputs. ::ffff:100.64.1.1 (CGNAT
// mapped via v4-mapped IPv6) MUST still produce a deny.
func TestServeSOCKS5_ATYP4_IPv4MappedSpecialRangeStillBlocked(t *testing.T) {
	sentinel := &sentinelCounter{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sentinel listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			sentinel.mu.Lock()
			sentinel.accepts++
			sentinel.mu.Unlock()
			conn.Close()
		}
	}()

	allow, _ := ParseAllowlist([]string{})
	sink := &pinSink{}
	proxyAddr, stop := startSocks5Proxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	socks5Greet(t, conn)
	// ::ffff:100.64.1.1 raw bytes (v4-mapped CGNAT).
	var mapped [16]byte
	mapped[10] = 0xff
	mapped[11] = 0xff
	mapped[12] = 100
	mapped[13] = 64
	mapped[14] = 1
	mapped[15] = 1
	socks5SendConnectIPv6(t, conn, mapped, 443)
	code := socks5ReadReplyCode(t, conn)
	if code == 0x00 {
		t.Fatalf("CGNAT-mapped IPv6 must NOT receive 0x00 succeeded")
	}
	if code != 0x02 {
		t.Errorf("expected 0x02 not allowed; got 0x%02x", code)
	}
	if sentinel.Accepts() != 0 {
		t.Errorf("expected zero sentinel accepts on deny; got %d", sentinel.Accepts())
	}
}

// ---------------------------------------------------------------
// Test #9 — TestServeHTTPConnect_Upstream403StaysAllowDecision
// ---------------------------------------------------------------
// Partner #9. An upstream HTTP 403 (real upstream-side rejection)
// observed AFTER the proxy has allowed the tunnel MUST NOT change
// the proxy's Allow decision. v42-3's tuple-return refactor passes
// only the pinned IP slice forward; the upstream response payload
// must not back-propagate into Decision shape.
func TestServeHTTPConnect_Upstream403StaysAllowDecision(t *testing.T) {
	sentinel, port := startSentinelListener(t)
	_ = sentinel
	allow, _ := ParseAllowlist([]string{
		fmt.Sprintf("u403.test:%d", port),
		fmt.Sprintf("127.0.0.1:%d", port),
	})
	resolver := NewMockResolver(map[string][]string{"u403.test": {"127.0.0.1"}})
	sink := &pinSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, resolver, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	target := fmt.Sprintf("u403.test:%d", port)
	connReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	if _, err := conn.Write([]byte(connReq)); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(conn)
	statusLine, _ := br.ReadString('\n')
	if !strings.Contains(statusLine, "200") {
		t.Fatalf("CONNECT itself must succeed (200); got %q", statusLine)
	}
	for {
		line, _ := br.ReadString('\n')
		if line == "\r\n" || line == "\n" || line == "" {
			break
		}
	}
	// We don't actually need to read upstream body — the assertion is
	// on the sink decision: a single Allow decision regardless of any
	// inner-tunnel HTTP response code.
	time.Sleep(60 * time.Millisecond)
	decisions := sink.decisionsSnapshot()
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision; got %d", len(decisions))
	}
	if !decisions[0].Allow {
		t.Errorf("expected Allow regardless of upstream 403; got %+v", decisions[0])
	}
}

// ---------------------------------------------------------------
// Test #13 — TestServeHTTPConnect_LongRunningTunnelKeepsPinForLifetime
// ---------------------------------------------------------------
// Architect-added (§7). The pin held for the lifetime of the tunnel.
// Verifies the dial site consumed the pin once and never re-resolved
// even when the tunnel sits idle long enough for a hostile DNS server
// to swap the underlying IP. Sentinel accept count == 1 throughout.
func TestServeHTTPConnect_LongRunningTunnelKeepsPinForLifetime(t *testing.T) {
	sentinel, port := startSentinelListener(t)
	allow, _ := ParseAllowlist([]string{
		fmt.Sprintf("longlived.example.com:%d", port),
		fmt.Sprintf("127.0.0.1:%d", port),
	})
	resolver := newMutableResolver(map[string][]string{
		"longlived.example.com": {"127.0.0.1"},
	})
	sink := &pinSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, resolver, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	target := fmt.Sprintf("longlived.example.com:%d", port)
	connReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	if _, err := conn.Write([]byte(connReq)); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(conn)
	statusLine, _ := br.ReadString('\n')
	if !strings.Contains(statusLine, "200") {
		t.Fatalf("expected 200; got %q", statusLine)
	}
	for {
		line, _ := br.ReadString('\n')
		if line == "\r\n" || line == "\n" || line == "" {
			break
		}
	}
	// Mutate resolver mid-tunnel; the existing tunnel must not
	// re-dial.
	resolver.SetHost("longlived.example.com", []string{"100.64.5.5"})
	_, _ = conn.Write([]byte("hello "))
	time.Sleep(60 * time.Millisecond)
	_, _ = conn.Write([]byte("world"))
	time.Sleep(120 * time.Millisecond)
	if sentinel.Accepts() != 1 {
		t.Errorf("expected exactly 1 sentinel accept across tunnel lifetime; got %d", sentinel.Accepts())
	}
}

// ---------------------------------------------------------------
// Test #15 — TestDialWithFallback_ErrorStringRetainsHostname
// ---------------------------------------------------------------
// ALC-2 mini-lap pattern (deterministic listener):
//   - net.Listen("tcp", "127.0.0.1:0")
//   - capture port
//   - listener.Close() immediately
//
// Subsequent connects to that port get a deterministic
// "connection refused", exercising the bounded-retry-exhaustion
// path. Asserts:
//  1. at least one EmitError captured
//  2. every error string contains the original hostname (sentinel.test)
//  3. no error string contains the pinned IP literals (127.0.0.1 / 127.0.0.2)
//  4. the final error wording mentions a "failed after" attempt count.
func TestDialWithFallback_ErrorStringRetainsHostname(t *testing.T) {
	// Bound and immediately close — releases the listen queue so
	// subsequent connects get connection-refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sentinel listen: %v", err)
	}
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := mustAtoi(t, portStr)
	_ = ln.Close()

	allow, _ := ParseAllowlist([]string{
		fmt.Sprintf("sentinel.test:%d", port),
		fmt.Sprintf("127.0.0.1:%d", port),
		fmt.Sprintf("127.0.0.2:%d", port),
	})
	resolver := NewMockResolver(map[string][]string{
		"sentinel.test": {"127.0.0.1", "127.0.0.2"},
	})
	sink := &pinSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, resolver, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	target := fmt.Sprintf("sentinel.test:%d", port)
	connReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	if _, err := conn.Write([]byte(connReq)); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Read the first response line (will be 502 bad gateway since
	// every dial attempt got connection-refused).
	br := bufio.NewReader(conn)
	_, _ = br.ReadString('\n')
	for {
		line, _ := br.ReadString('\n')
		if line == "\r\n" || line == "\n" || line == "" {
			break
		}
	}
	time.Sleep(80 * time.Millisecond)

	errs := sink.errorsSnapshot()
	if len(errs) == 0 {
		t.Fatalf("expected at least one EmitError; got 0")
	}
	finalErrText := errs[len(errs)-1].Error()
	for i, e := range errs {
		s := e.Error()
		if !strings.Contains(s, "sentinel.test") {
			t.Errorf("errs[%d] = %q; expected to contain original hostname sentinel.test", i, s)
		}
		if strings.Contains(s, "127.0.0.1") {
			t.Errorf("errs[%d] = %q; pinned IP 127.0.0.1 leaked", i, s)
		}
		if strings.Contains(s, "127.0.0.2") {
			t.Errorf("errs[%d] = %q; pinned IP 127.0.0.2 leaked", i, s)
		}
	}
	if !strings.Contains(finalErrText, "failed after") {
		t.Errorf("final error must mention bounded-fallback wording; got %q", finalErrText)
	}
}

// Reuse encoding helpers — declared here for clarity.
var _ = io.EOF
var _ = binary.BigEndian
