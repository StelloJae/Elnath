package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// B3b-4-2 Phase E hardening: N1 HTTP CONNECT header drain bytes/lines
// caps + N2 splice end-of-tunnel half-close pattern.
//
// N1 — slow-loris client can chain many small headers within the
// connectIOTimeout window. Without per-request total-bytes and
// per-line-count caps, a misbehaving client wedges proxy resources.
// Remediation: reject headers exceeding 8KiB total or 50 lines with
// HTTP 431 + ReasonModeGuard decision.
//
// N2 — splice end-of-tunnel deadline race truncates in-flight bytes
// when the proxy slams SetDeadline(now()) on both endpoints to
// unstick the second goroutine. Remediation: prefer net.TCPConn
// CloseWrite half-close so io.Copy on the still-active side sees a
// natural EOF.

// TestHTTPConnect_RejectsExcessiveHeaderBytes covers the N1 byte cap.
// A client that ships >8KiB of headers must receive HTTP 431 +
// ReasonModeGuard decision.
func TestHTTPConnect_RejectsExcessiveHeaderBytes(t *testing.T) {
	allow, _ := ParseAllowlist([]string{"github.com:443"})
	sink := &captureSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	// Send a CONNECT request line followed by many small header lines
	// totaling well over 8KiB. The proxy must reject before the
	// terminating empty line.
	if _, err := conn.Write([]byte("CONNECT github.com:443 HTTP/1.1\r\n")); err != nil {
		t.Fatalf("write request line: %v", err)
	}
	for i := 0; i < 200; i++ {
		// 200 lines × ~60 bytes = ~12KiB, easily over 8KiB.
		hdr := fmt.Sprintf("X-Filler-%03d: %s\r\n", i, strings.Repeat("a", 50))
		if _, err := conn.Write([]byte(hdr)); err != nil {
			break
		}
	}

	// Read the response status line. The proxy must respond with 431
	// before it sees the terminating CRLF.
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(statusLine, "431") {
		t.Errorf("expected HTTP 431; got %q", statusLine)
	}

	time.Sleep(20 * time.Millisecond)
	decisions := sink.snapshotDecisions()
	if len(decisions) == 0 {
		t.Fatalf("expected at least one deny decision")
	}
	last := decisions[len(decisions)-1]
	if last.Allow {
		t.Errorf("expected deny decision; got allow %+v", last)
	}
	if last.Reason != ReasonModeGuard {
		t.Errorf("expected ReasonModeGuard; got %q", last.Reason)
	}
}

// TestHTTPConnect_RejectsExcessiveHeaderLineCount covers the N1 line
// cap. A client that ships >50 short header lines (each well under
// the byte cap) must still be rejected.
func TestHTTPConnect_RejectsExcessiveHeaderLineCount(t *testing.T) {
	allow, _ := ParseAllowlist([]string{"github.com:443"})
	sink := &captureSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("CONNECT github.com:443 HTTP/1.1\r\n")); err != nil {
		t.Fatalf("write request line: %v", err)
	}
	for i := 0; i < 100; i++ {
		// Very short headers — total bytes stay under 8KiB but line
		// count exceeds 50.
		hdr := fmt.Sprintf("X-%d: a\r\n", i)
		if _, err := conn.Write([]byte(hdr)); err != nil {
			break
		}
	}

	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(statusLine, "431") {
		t.Errorf("expected HTTP 431; got %q", statusLine)
	}
}

// TestHTTPConnect_AcceptsNormalHeaderCount confirms the cap does NOT
// reject reasonable browsers / tools. Curl typically sends ~10
// headers; a real proxy must let those through.
func TestHTTPConnect_AcceptsNormalHeaderCount(t *testing.T) {
	allow, _ := ParseAllowlist([]string{"github.com:443"})
	sink := &captureSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	conn, err := net.Dial("tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	connReq := strings.Builder{}
	connReq.WriteString("CONNECT github.com:443 HTTP/1.1\r\n")
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&connReq, "X-Header-%d: value\r\n", i)
	}
	connReq.WriteString("\r\n")
	if _, err := conn.Write([]byte(connReq.String())); err != nil {
		t.Fatalf("write request: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	// Allowlist contains github.com:443 but the proxy will fail to
	// dial in this test; we just want to confirm it didn't reject
	// for header reasons. Acceptable status lines: 200 (succeeded
	// upstream — improbable), 502 (bad gateway upstream), or 403
	// (denied by some other rule). Anything OTHER than 431 / 400
	// proves headers passed the cap.
	if strings.Contains(statusLine, "431") || strings.Contains(statusLine, "400") {
		t.Errorf("normal header count must NOT be rejected; got %q", statusLine)
	}
	_ = sink
}

// TestHTTPConnect_TunnelHalfCloseAvoidsTruncation pins N2: when an
// HTTP CONNECT tunnel ends gracefully, the second goroutine must
// observe a natural EOF rather than a deadline-as-cancel that could
// truncate in-flight bytes.
func TestHTTPConnect_TunnelHalfCloseAvoidsTruncation(t *testing.T) {
	// Build a bespoke upstream that, after the client sends a small
	// query, replies with a large body and then closes its write
	// side. The test asserts the client receives the FULL body.
	largeBody := strings.Repeat("ABCDEFGHIJ", 4096) // ~40KiB
	upstreamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	defer upstreamLn.Close()
	go func() {
		conn, err := upstreamLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Read whatever the client sent.
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		// Write the large body and half-close the write side.
		_, _ = io.WriteString(conn, largeBody)
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		// Hold the read side open briefly; the test will close
		// from the client side.
		buf2 := make([]byte, 256)
		_, _ = conn.Read(buf2)
	}()
	upstreamHostPort := upstreamLn.Addr().String()
	host, portStr, _ := net.SplitHostPort(upstreamHostPort)
	port := mustAtoi(t, portStr)

	allow, _ := ParseAllowlist([]string{fmt.Sprintf("%s:%d", host, port)})
	sink := &captureSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, nil, sink)
	defer stop()

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

	// Send a tiny request through the tunnel, then half-close write.
	if _, err := conn.Write([]byte("Q")); err != nil {
		t.Fatalf("write query: %v", err)
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}

	// Read the large body. Set a generous deadline so a true EOF on
	// the upstream side resolves cleanly.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	body, err := io.ReadAll(br)
	if err != nil && err != io.EOF {
		t.Fatalf("read body: %v", err)
	}
	if len(body) != len(largeBody) {
		t.Errorf("expected full body of %d bytes; got %d", len(largeBody), len(body))
	}
}

// TestHTTPConnect_SinkErrorOnExcessiveHeaders confirms the deny
// decision flows through the production sink path so observability
// is preserved (partner-mini-lap N1 carry-forward).
func TestHTTPConnect_SinkErrorOnExcessiveHeaders(t *testing.T) {
	allow, _ := ParseAllowlist([]string{"github.com:443"})
	sink := &captureSink{}
	proxyAddr, stop := startProxy(t, allow, Denylist{}, nil, sink)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr.String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("CONNECT github.com:443 HTTP/1.1\r\n")); err != nil {
		t.Fatalf("write request line: %v", err)
	}
	huge := strings.Repeat("X-Huge: "+strings.Repeat("a", 200)+"\r\n", 100)
	_, _ = conn.Write([]byte(huge))

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(conn)
	_, _ = br.ReadString('\n')

	time.Sleep(20 * time.Millisecond)
	decisions := sink.snapshotDecisions()
	if len(decisions) == 0 {
		t.Fatalf("expected at least one deny decision in sink")
	}
}
