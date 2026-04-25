package tools

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------
// EventSink test helper
// ---------------------------------------------------------------

type captureSink struct {
	mu        sync.Mutex
	decisions []Decision
	errors    []error
}

func (c *captureSink) EmitDecision(d Decision) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.decisions = append(c.decisions, d)
}

func (c *captureSink) EmitError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = append(c.errors, err)
}

func (c *captureSink) snapshotDecisions() []Decision {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Decision, len(c.decisions))
	copy(out, c.decisions)
	return out
}

func (c *captureSink) snapshotErrors() []error {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]error, len(c.errors))
	copy(out, c.errors)
	return out
}

// ---------------------------------------------------------------
// HTTP CONNECT listener tests
// ---------------------------------------------------------------

// startTestUpstream spawns an httptest.Server and returns its
// listener address. Body for any request is upstreamBody.
func startTestUpstream(t *testing.T, upstreamBody string, status int) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(upstreamBody))
	}))
	t.Cleanup(srv.Close)
	host, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	return srv, net.JoinHostPort(host, port)
}

// startProxy launches ServeHTTPConnect on a fresh ephemeral
// listener; returns the bound address and a cancel func that closes
// the listener.
func startProxy(t *testing.T, allow Allowlist, deny Denylist, resolver Resolver, sink EventSink) (net.Addr, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = ServeHTTPConnect(ctx, listener, allow, deny, resolver, sink)
	}()
	return listener.Addr(), func() {
		cancel()
		_ = listener.Close()
	}
}

func TestHTTPConnect_AllowedTunnel(t *testing.T) {
	upstream, upstreamHostPort := startTestUpstream(t, "OK upstream body", 200)
	_ = upstream

	host, portStr, _ := net.SplitHostPort(upstreamHostPort)
	port := mustAtoi(t, portStr)
	allow, _ := ParseAllowlist([]string{fmt.Sprintf("%s:%d", host, port)})
	sink := &captureSink{}

	proxyAddr, stop := startProxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	// Open a TCP connection to the proxy and send a CONNECT request.
	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	connReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", upstreamHostPort, upstreamHostPort)
	if _, err := conn.Write([]byte(connReq)); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(statusLine, "200") {
		t.Fatalf("expected 200; got %q", statusLine)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	// Now send an HTTP/1.1 GET / through the tunnel and read the body.
	getReq := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", upstreamHostPort)
	if _, err := conn.Write([]byte(getReq)); err != nil {
		t.Fatalf("write get: %v", err)
	}
	resp, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("read upstream resp: %v", err)
	}
	respStr := string(resp)
	if !strings.Contains(respStr, "OK upstream body") {
		t.Errorf("expected upstream body in response; got %q", respStr)
	}

	// Verify a single Allow Decision was recorded.
	time.Sleep(20 * time.Millisecond) // allow async sink emit
	decisions := sink.snapshotDecisions()
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision; got %d (%+v)", len(decisions), decisions)
	}
	if !decisions[0].Allow {
		t.Errorf("expected allow; got %+v", decisions[0])
	}
	if decisions[0].Source != SourceNetworkProxy {
		t.Errorf("expected SourceNetworkProxy; got %q", decisions[0].Source)
	}
	if decisions[0].Protocol != ProtocolHTTPSConnect {
		t.Errorf("expected ProtocolHTTPSConnect; got %q", decisions[0].Protocol)
	}
}

func TestHTTPConnect_DeniedReturns403(t *testing.T) {
	allow, _ := ParseAllowlist([]string{"github.com:443"})
	sink := &captureSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	connReq := "CONNECT gitlab.com:443 HTTP/1.1\r\nHost: gitlab.com:443\r\n\r\n"
	if _, err := conn.Write([]byte(connReq)); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(statusLine, "403") {
		t.Errorf("expected 403; got %q", statusLine)
	}

	// Verify x-proxy-error header present.
	headers, _ := io.ReadAll(br)
	if !strings.Contains(string(headers), "x-proxy-error") {
		t.Errorf("expected x-proxy-error header; got %q", string(headers))
	}
	if !strings.Contains(string(headers), "blocked-by-allowlist") {
		t.Errorf("expected blocked-by-allowlist marker; got %q", string(headers))
	}

	time.Sleep(20 * time.Millisecond)
	decisions := sink.snapshotDecisions()
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision; got %d", len(decisions))
	}
	if decisions[0].Allow {
		t.Errorf("expected deny; got %+v", decisions[0])
	}
	if decisions[0].Reason != ReasonNotInAllowlist {
		t.Errorf("expected ReasonNotInAllowlist; got %q", decisions[0].Reason)
	}
}

func TestHTTPConnect_DenylistWinsReturns403(t *testing.T) {
	allow, _ := ParseAllowlist([]string{"**.github.com:443"})
	deny, _ := ParseDenylist([]string{"evil.github.com:443"})
	sink := &captureSink{}
	proxyAddr, stop := startProxy(t, allow, deny, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("CONNECT evil.github.com:443 HTTP/1.1\r\nHost: evil.github.com:443\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(conn)
	statusLine, _ := br.ReadString('\n')
	if !strings.Contains(statusLine, "403") {
		t.Errorf("expected 403; got %q", statusLine)
	}
	headers, _ := io.ReadAll(br)
	if !strings.Contains(string(headers), "blocked-by-denylist") {
		t.Errorf("expected blocked-by-denylist; got %q", string(headers))
	}
}

func TestHTTPConnect_NonConnectMethodRejected(t *testing.T) {
	allow, _ := ParseAllowlist([]string{"github.com:443"})
	sink := &captureSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	// GET instead of CONNECT.
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: github.com\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(conn)
	statusLine, _ := br.ReadString('\n')
	if !strings.Contains(statusLine, "405") && !strings.Contains(statusLine, "400") {
		t.Errorf("expected 405 or 400 for non-CONNECT; got %q", statusLine)
	}

	time.Sleep(20 * time.Millisecond)
	decisions := sink.snapshotDecisions()
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision; got %d", len(decisions))
	}
	if decisions[0].Reason != ReasonModeGuard && decisions[0].Reason != ReasonProtocolUnsupported {
		t.Errorf("expected ReasonModeGuard or ReasonProtocolUnsupported; got %q", decisions[0].Reason)
	}
}

func TestHTTPConnect_UpstreamSidesteps403IsNotASandboxViolation(t *testing.T) {
	// Upstream itself returns 403 (real HTTP 403 from the server,
	// not a proxy block). The proxy must allow the tunnel and forward
	// the 403 transparently — the proxy does not record this as a
	// sandbox violation because the proxy's policy was satisfied.
	upstream, upstreamHostPort := startTestUpstream(t, "real upstream 403", 403)
	_ = upstream
	host, portStr, _ := net.SplitHostPort(upstreamHostPort)
	port := mustAtoi(t, portStr)
	allow, _ := ParseAllowlist([]string{fmt.Sprintf("%s:%d", host, port)})
	sink := &captureSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	connReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", upstreamHostPort, upstreamHostPort)
	if _, err := conn.Write([]byte(connReq)); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	br := bufio.NewReader(conn)
	statusLine, _ := br.ReadString('\n')
	if !strings.Contains(statusLine, "200") {
		t.Fatalf("CONNECT itself should succeed (200); got %q", statusLine)
	}
	// Drain headers.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("drain: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	getReq := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", upstreamHostPort)
	if _, err := conn.Write([]byte(getReq)); err != nil {
		t.Fatalf("get: %v", err)
	}
	resp, _ := io.ReadAll(br)
	if !strings.Contains(string(resp), "real upstream 403") {
		t.Errorf("upstream body should be forwarded; got %q", string(resp))
	}

	time.Sleep(20 * time.Millisecond)
	decisions := sink.snapshotDecisions()
	if len(decisions) != 1 {
		t.Fatalf("expected 1 allow decision; got %d", len(decisions))
	}
	if !decisions[0].Allow {
		t.Errorf("upstream-403 should not be a deny decision; got %+v", decisions[0])
	}
}

// ---------------------------------------------------------------
// SOCKS5 TCP listener tests
// ---------------------------------------------------------------

func startSocks5Proxy(t *testing.T, allow Allowlist, deny Denylist, resolver Resolver, sink EventSink) (net.Addr, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = ServeSOCKS5(ctx, listener, allow, deny, resolver, sink)
	}()
	return listener.Addr(), func() {
		cancel()
		_ = listener.Close()
	}
}

// socks5Greet performs the SOCKS5 greeting (no-auth) handshake;
// returns when ready to send the request.
func socks5Greet(t *testing.T, conn net.Conn) {
	t.Helper()
	// Greeting: VER=5, NMETHODS=1, METHOD=0 (no auth).
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write greet: %v", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read greet response: %v", err)
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		t.Fatalf("expected greet 0x05 0x00; got %x %x", resp[0], resp[1])
	}
}

// socks5SendConnect sends a SOCKS5 CONNECT (cmd=1) request with the
// given hostname and port.
func socks5SendConnect(t *testing.T, conn net.Conn, host string, port int) {
	t.Helper()
	if len(host) > 255 {
		t.Fatalf("host too long")
	}
	buf := make([]byte, 0, 7+len(host))
	buf = append(buf, 0x05, 0x01, 0x00, 0x03, byte(len(host)))
	buf = append(buf, []byte(host)...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	buf = append(buf, portBytes...)
	if _, err := conn.Write(buf); err != nil {
		t.Fatalf("write connect: %v", err)
	}
}

func socks5ReadReplyCode(t *testing.T, conn net.Conn) byte {
	t.Helper()
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatalf("read reply hdr: %v", err)
	}
	if hdr[0] != 0x05 {
		t.Fatalf("expected ver 5; got %x", hdr[0])
	}
	// Drain BND.ADDR + BND.PORT depending on ATYP.
	switch hdr[3] {
	case 0x01:
		_, _ = io.ReadFull(conn, make([]byte, 4+2))
	case 0x03:
		l := make([]byte, 1)
		_, _ = io.ReadFull(conn, l)
		_, _ = io.ReadFull(conn, make([]byte, int(l[0])+2))
	case 0x04:
		_, _ = io.ReadFull(conn, make([]byte, 16+2))
	}
	return hdr[1]
}

func TestSOCKS5_AllowedTCPConnect(t *testing.T) {
	upstream, upstreamHostPort := startTestUpstream(t, "OK upstream body", 200)
	_ = upstream
	host, portStr, _ := net.SplitHostPort(upstreamHostPort)
	port := mustAtoi(t, portStr)
	allow, _ := ParseAllowlist([]string{fmt.Sprintf("%s:%d", host, port)})
	sink := &captureSink{}
	proxyAddr, stop := startSocks5Proxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	socks5Greet(t, conn)
	socks5SendConnect(t, conn, host, port)
	code := socks5ReadReplyCode(t, conn)
	if code != 0x00 {
		t.Fatalf("expected reply 0x00 (succeeded); got %x", code)
	}

	getReq := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", upstreamHostPort)
	if _, err := conn.Write([]byte(getReq)); err != nil {
		t.Fatalf("write get: %v", err)
	}
	body, _ := io.ReadAll(conn)
	if !strings.Contains(string(body), "OK upstream body") {
		t.Errorf("expected upstream body; got %q", string(body))
	}

	time.Sleep(20 * time.Millisecond)
	decisions := sink.snapshotDecisions()
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision; got %d", len(decisions))
	}
	if !decisions[0].Allow {
		t.Errorf("expected allow; got %+v", decisions[0])
	}
	if decisions[0].Protocol != ProtocolSOCKS5TCP {
		t.Errorf("expected ProtocolSOCKS5TCP; got %q", decisions[0].Protocol)
	}
}

func TestSOCKS5_DeniedReplyCode(t *testing.T) {
	allow, _ := ParseAllowlist([]string{"github.com:443"})
	sink := &captureSink{}
	proxyAddr, stop := startSocks5Proxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	socks5Greet(t, conn)
	socks5SendConnect(t, conn, "gitlab.com", 443)
	code := socks5ReadReplyCode(t, conn)
	// 0x02 = connection not allowed by ruleset (RFC 1928).
	if code != 0x02 {
		t.Errorf("expected reply 0x02 (not allowed); got %x", code)
	}

	time.Sleep(20 * time.Millisecond)
	decisions := sink.snapshotDecisions()
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision; got %d", len(decisions))
	}
	if decisions[0].Allow {
		t.Errorf("expected deny; got %+v", decisions[0])
	}
}

func TestSOCKS5_UDPAssociateRejected(t *testing.T) {
	allow, _ := ParseAllowlist([]string{"github.com:443"})
	sink := &captureSink{}
	proxyAddr, stop := startSocks5Proxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	socks5Greet(t, conn)
	// Build CONNECT-like request but with cmd=0x03 (UDP ASSOCIATE).
	host := "github.com"
	buf := make([]byte, 0, 7+len(host))
	buf = append(buf, 0x05, 0x03, 0x00, 0x03, byte(len(host)))
	buf = append(buf, []byte(host)...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, 443)
	buf = append(buf, portBytes...)
	if _, err := conn.Write(buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	code := socks5ReadReplyCode(t, conn)
	// 0x07 = command not supported (RFC 1928).
	if code != 0x07 {
		t.Errorf("expected reply 0x07 (command not supported); got %x", code)
	}

	time.Sleep(20 * time.Millisecond)
	decisions := sink.snapshotDecisions()
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision; got %d", len(decisions))
	}
	if decisions[0].Reason != ReasonProtocolUnsupported {
		t.Errorf("expected ReasonProtocolUnsupported; got %q", decisions[0].Reason)
	}
}

func TestSOCKS5_BindCommandRejected(t *testing.T) {
	allow, _ := ParseAllowlist([]string{"github.com:443"})
	sink := &captureSink{}
	proxyAddr, stop := startSocks5Proxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	socks5Greet(t, conn)
	// cmd=0x02 (BIND).
	host := "github.com"
	buf := make([]byte, 0, 7+len(host))
	buf = append(buf, 0x05, 0x02, 0x00, 0x03, byte(len(host)))
	buf = append(buf, []byte(host)...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, 443)
	buf = append(buf, portBytes...)
	if _, err := conn.Write(buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	code := socks5ReadReplyCode(t, conn)
	if code != 0x07 {
		t.Errorf("expected reply 0x07 (command not supported); got %x", code)
	}
}

// ---------------------------------------------------------------
// M2 — SOCKS5 ATYP=0x04 (IPv6) binary-path regression tests
// ---------------------------------------------------------------
// The text-based scoped-IPv6 reject guard at evaluate-time only fires
// for hostnames containing '%'. SOCKS5 ATYP=0x04 supplies 16 raw
// bytes; the resulting net.IP(buf).String() never carries '%', so the
// only defense for non-public addresses arriving via this path is the
// IP classifier in isSpecialRangeIP. These tests pin that defense
// against silent regression. Production code is unchanged — the
// existing classifier already catches both link-local and v4-mapped
// loopback, since net.IP(16-byte buf).String() for ::ffff:127.0.0.1
// renders as "127.0.0.1" which falls into the v4 loopback branch.

// socks5SendConnectIPv6 sends a SOCKS5 CONNECT (cmd=1) request with
// ATYP=0x04 and the supplied 16 raw bytes as DST.ADDR.
func socks5SendConnectIPv6(t *testing.T, conn net.Conn, ipv6 [16]byte, port int) {
	t.Helper()
	buf := make([]byte, 0, 4+16+2)
	buf = append(buf, 0x05, 0x01, 0x00, 0x04)
	buf = append(buf, ipv6[:]...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	buf = append(buf, portBytes...)
	if _, err := conn.Write(buf); err != nil {
		t.Fatalf("write atyp=4 connect: %v", err)
	}
}

func TestSOCKS5_ATYP4_LinkLocalIPv6BlockedByDefault(t *testing.T) {
	// Sentinel listener: if the proxy ever dials upstream for a
	// link-local target, we'll see a connection attempt here. fail-fast.
	sentinel, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sentinel listen: %v", err)
	}
	defer sentinel.Close()
	gotDial := make(chan struct{}, 1)
	go func() {
		if c, err := sentinel.Accept(); err == nil {
			c.Close()
			select {
			case gotDial <- struct{}{}:
			default:
			}
		}
	}()

	// Empty allowlist + empty denylist — link-local must be denied
	// purely by the special-range classifier.
	allow, _ := ParseAllowlist([]string{})
	sink := &captureSink{}
	proxyAddr, stop := startSocks5Proxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	socks5Greet(t, conn)
	// fe80::1 raw bytes.
	var fe80 [16]byte
	fe80[0] = 0xfe
	fe80[1] = 0x80
	fe80[15] = 0x01
	socks5SendConnectIPv6(t, conn, fe80, 80)

	code := socks5ReadReplyCode(t, conn)
	if code == 0x00 {
		t.Fatalf("link-local fe80::1 must NOT receive 0x00 succeeded")
	}
	// Per RFC 1928 §6, 0x02 = "connection not allowed by ruleset" is
	// the deny-by-policy code emitted by the proxy.
	if code != 0x02 {
		t.Errorf("expected reply 0x02 (not allowed); got 0x%02x", code)
	}

	time.Sleep(20 * time.Millisecond)
	decisions := sink.snapshotDecisions()
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision; got %d (%+v)", len(decisions), decisions)
	}
	d := decisions[0]
	if d.Allow {
		t.Errorf("expected deny; got %+v", d)
	}
	if d.Source != SourceNetworkProxy {
		t.Errorf("expected SourceNetworkProxy; got %q", d.Source)
	}
	if d.Protocol != ProtocolSOCKS5TCP {
		t.Errorf("expected ProtocolSOCKS5TCP; got %q", d.Protocol)
	}
	// Either ReasonNotInAllowlist (no allowlist match path) or
	// ReasonLocalBindingDisabled (special-range classifier path) is
	// acceptable — both are deny-by-design for fe80::1 with an empty
	// allowlist. Pin to either to avoid over-specifying the production
	// branch order.
	if d.Reason != ReasonLocalBindingDisabled && d.Reason != ReasonNotInAllowlist {
		t.Errorf("expected ReasonLocalBindingDisabled or ReasonNotInAllowlist; got %q", d.Reason)
	}
	if d.Host != "fe80::1" {
		t.Errorf("expected Host=fe80::1; got %q", d.Host)
	}
	if d.Port != 80 {
		t.Errorf("expected Port=80; got %d", d.Port)
	}

	// Confirm no upstream dial happened.
	select {
	case <-gotDial:
		t.Fatalf("proxy dialed sentinel for a link-local target — production bug")
	default:
	}
}

func TestSOCKS5_ATYP4_IPv4MappedLoopbackBlockedByDefault(t *testing.T) {
	sentinel, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sentinel listen: %v", err)
	}
	defer sentinel.Close()
	gotDial := make(chan struct{}, 1)
	go func() {
		if c, err := sentinel.Accept(); err == nil {
			c.Close()
			select {
			case gotDial <- struct{}{}:
			default:
			}
		}
	}()

	allow, _ := ParseAllowlist([]string{})
	sink := &captureSink{}
	proxyAddr, stop := startSocks5Proxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	socks5Greet(t, conn)
	// ::ffff:127.0.0.1 raw bytes (v4-mapped IPv6).
	var mapped [16]byte
	mapped[10] = 0xff
	mapped[11] = 0xff
	mapped[12] = 127
	mapped[15] = 0x01
	socks5SendConnectIPv6(t, conn, mapped, 22)

	code := socks5ReadReplyCode(t, conn)
	if code == 0x00 {
		t.Fatalf("v4-mapped loopback ::ffff:127.0.0.1 must NOT receive 0x00 succeeded")
	}
	if code != 0x02 {
		t.Errorf("expected reply 0x02 (not allowed); got 0x%02x", code)
	}

	time.Sleep(20 * time.Millisecond)
	decisions := sink.snapshotDecisions()
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision; got %d (%+v)", len(decisions), decisions)
	}
	d := decisions[0]
	if d.Allow {
		t.Errorf("expected deny; got %+v", d)
	}
	if d.Source != SourceNetworkProxy {
		t.Errorf("expected SourceNetworkProxy; got %q", d.Source)
	}
	if d.Protocol != ProtocolSOCKS5TCP {
		t.Errorf("expected ProtocolSOCKS5TCP; got %q", d.Protocol)
	}
	if d.Reason != ReasonLocalBindingDisabled && d.Reason != ReasonNotInAllowlist {
		t.Errorf("expected ReasonLocalBindingDisabled or ReasonNotInAllowlist; got %q", d.Reason)
	}
	// net.IP(16-byte ::ffff:127.0.0.1).String() renders as "127.0.0.1"
	// (verified: stdlib collapses v4-mapped form to dotted v4). Pin to
	// what production actually emits.
	if d.Host != "127.0.0.1" {
		t.Errorf("expected Host=127.0.0.1 (rendered v4-mapped form); got %q", d.Host)
	}
	if d.Port != 22 {
		t.Errorf("expected Port=22; got %d", d.Port)
	}

	select {
	case <-gotDial:
		t.Fatalf("proxy dialed sentinel for v4-mapped loopback — production bug")
	default:
	}
}

// ---------------------------------------------------------------
// Sink behaviour
// ---------------------------------------------------------------

func TestServeHTTPConnect_AcceptLoopErrorsObservable(t *testing.T) {
	// Closing the listener should NOT cause the sink to record any
	// error; that's the "listener closed" path. But OTHER accept
	// errors must be observable. We can't easily inject a non-EOF
	// accept error, so we instead assert that the function returns
	// nil (graceful) on Close and that no spurious error decisions
	// are recorded.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	sink := &captureSink{}
	allow, _ := ParseAllowlist([]string{"github.com:443"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeHTTPConnect(ctx, listener, allow, Denylist{}, nil, sink)
	}()

	cancel()
	_ = listener.Close()
	select {
	case err := <-done:
		if err != nil {
			// ServeHTTPConnect should distinguish listener-closed
			// (returns nil) from other errors.
			t.Errorf("graceful shutdown should return nil; got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeHTTPConnect did not return after Close")
	}

	// No errors should be recorded for graceful shutdown.
	if errs := sink.snapshotErrors(); len(errs) != 0 {
		t.Errorf("expected 0 errors on graceful shutdown; got %v", errs)
	}
}

// ---------------------------------------------------------------
// helpers
// ---------------------------------------------------------------

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("bad int: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}
