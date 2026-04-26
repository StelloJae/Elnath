// Package tools — netproxy_proxy.go
//
// v41 / B3b-4-0 proxy core. Self-contained library used by the
// macOS Seatbelt and Linux bwrap substrate lanes (B3b-4-2, B3b-4-3)
// to enforce domain + IP allowlists for outbound TCP traffic. NOT
// wired into BashRunner in this lane.
//
// Partner-locked pins observed here:
//   - DNS rebinding is not fully defended (cite Codex
//     network-proxy/README.md:217-219).
//   - No allowLocalBinding boolean. Local services are reached only
//     via explicit per-port entries.
//   - Forked-child self-exec proxy model. No in-process goroutine
//     proxy.
//   - Source enum is fixed at four values.
//   - No ProxyEnabled config flag — substrate lanes infer proxy need
//     from allowlist shape.
//
// This file implements two listener entrypoints:
//   - ServeHTTPConnect: HTTP CONNECT method only. NO MITM, NO body
//     inspection, NO support for plain HTTP forward-proxy GET/POST.
//   - ServeSOCKS5:      SOCKS5 TCP CONNECT command (0x01) only. UDP
//     ASSOCIATE (0x03) and BIND (0x02) are explicitly rejected with
//     RFC1928 reply code 0x07 (command not supported).
//
// Both listeners apply the same Allowlist + Denylist policy via
// EvaluateWithDenylist. Decisions are emitted through the EventSink;
// per critic mini-lap N1 carry-forward, accept-loop errors that
// aren't "listener closed" go through sink.EmitError instead of
// being silently discarded.

package tools

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// connectIOTimeout bounds initial proxy handshake reads. The proxy
// is the only egress path for the sandbox; a stuck client must not
// be able to wedge a goroutine forever.
const connectIOTimeout = 30 * time.Second

// httpHeaderMaxBytes caps total bytes consumed across the HTTP
// CONNECT request line + headers. Slow-loris clients that chain many
// small headers within connectIOTimeout are blocked by this cap (N1
// hardening). 8KiB matches typical web server header limits.
const httpHeaderMaxBytes = 8 * 1024

// httpHeaderMaxLines caps the count of header lines on a CONNECT
// request. Most browsers send <20; 50 leaves comfortable headroom
// while preventing pathological clients from racing the byte cap with
// many tiny lines (N1 hardening).
const httpHeaderMaxLines = 50

// ServeHTTPConnect runs an HTTP CONNECT-only proxy on listener until
// the listener is closed or ctx is canceled. Returns nil on graceful
// shutdown; any non-graceful accept error after the listener is
// shown to be open is emitted to sink.EmitError and the loop exits
// with that error wrapped.
//
// The handler accepts ONLY the HTTP CONNECT method. GET / POST /
// PUT / etc. are rejected with HTTP 405 + a deny Decision marked
// ReasonModeGuard. The CONNECT target is parsed as host:port, run
// through EvaluateWithDenylist, and either tunneled (200 Connection
// established) or rejected (403 + x-proxy-error header).
//
// resolver may be nil; it is consulted only when the CONNECT host
// is a hostname that matches the allowlist, to apply the resolved-IP
// special-range check (defense against allowlisted public hostname
// resolving to private IP).
func ServeHTTPConnect(
	ctx context.Context,
	listener net.Listener,
	allow Allowlist,
	deny Denylist,
	resolver Resolver,
	sink EventSink,
) error {
	if sink == nil {
		return errors.New("netproxy: sink required")
	}

	// Cancel propagation: when ctx is canceled, close the listener
	// so Accept returns. This avoids deadlocking on Accept when the
	// caller cancels but doesn't also call listener.Close.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if isClosedListenerErr(err) || ctx.Err() != nil {
				return nil
			}
			sink.EmitError(fmt.Errorf("netproxy: http accept: %w", err))
			return err
		}
		go handleHTTPConnect(ctx, conn, allow, deny, resolver, sink)
	}
}

// isClosedListenerErr identifies the "listener closed" error that
// every Go listener returns from Accept after Close. Both
// `net.ErrClosed` and the literal substring "use of closed network
// connection" appear depending on Go version.
func isClosedListenerErr(err error) bool {
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(err.Error(), "use of closed network connection")
}

func handleHTTPConnect(
	ctx context.Context,
	client net.Conn,
	allow Allowlist,
	deny Denylist,
	resolver Resolver,
	sink EventSink,
) {
	defer client.Close()
	_ = client.SetReadDeadline(time.Now().Add(connectIOTimeout))

	br := bufio.NewReader(client)
	requestLine, err := br.ReadString('\n')
	if err != nil {
		sink.EmitError(fmt.Errorf("netproxy http: read request line: %w", err))
		return
	}
	parts := strings.Fields(strings.TrimSpace(requestLine))
	if len(parts) < 3 {
		writeHTTPStatus(client, 400, "bad request", "")
		d, _ := NewDeny(SourceNetworkProxy, ReasonModeGuard, "", 0, ProtocolHTTPSConnect)
		sink.EmitDecision(d)
		return
	}
	method := strings.ToUpper(parts[0])
	target := parts[1]

	// Drain remaining headers; a CONNECT may include Host, User-Agent,
	// Proxy-Authorization, etc. We do not inspect them but must
	// consume the empty line so the tunnel is at the right boundary.
	//
	// N1 hardening: cap total header bytes at httpHeaderMaxBytes and
	// per-request line count at httpHeaderMaxLines. Slow-loris
	// clients that chain many small headers within connectIOTimeout
	// would otherwise wedge a goroutine.
	totalHeaderBytes := len(requestLine)
	headerLineCount := 0
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			sink.EmitError(fmt.Errorf("netproxy http: read header: %w", err))
			return
		}
		if line == "\r\n" || line == "\n" {
			break
		}
		totalHeaderBytes += len(line)
		headerLineCount++
		if totalHeaderBytes > httpHeaderMaxBytes || headerLineCount > httpHeaderMaxLines {
			writeHTTPStatus(client, 431, "request header fields too large",
				"x-proxy-error: blocked-by-header-cap")
			host, port := splitHostPortBestEffort(target)
			d, _ := NewDeny(SourceNetworkProxy, ReasonModeGuard, host, port, ProtocolHTTPSConnect)
			sink.EmitDecision(d)
			return
		}
	}
	_ = client.SetReadDeadline(time.Time{})

	if method != "CONNECT" {
		host, port := splitHostPortBestEffort(target)
		writeHTTPStatus(client, 405, "method not allowed",
			"x-proxy-error: blocked-by-method-policy\r\n"+
				"x-proxy-method: "+method)
		d, _ := NewDeny(SourceNetworkProxy, ReasonModeGuard, host, port, ProtocolHTTPSConnect)
		sink.EmitDecision(d)
		return
	}

	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" || port == "" {
		writeHTTPStatus(client, 400, "bad CONNECT target", "x-proxy-error: invalid-target")
		d, _ := NewDeny(SourceNetworkProxy, ReasonInvalidConfig, target, 0, ProtocolHTTPSConnect)
		sink.EmitDecision(d)
		return
	}
	portN, err := strconv.Atoi(port)
	if err != nil || portN <= 0 || portN > 65535 {
		writeHTTPStatus(client, 400, "bad CONNECT port", "x-proxy-error: invalid-target")
		d, _ := NewDeny(SourceNetworkProxy, ReasonInvalidConfig, host, 0, ProtocolHTTPSConnect)
		sink.EmitDecision(d)
		return
	}

	decision := EvaluateWithDenylist(ctx, allow, deny, host, portN, ProtocolHTTPSConnect, resolver)
	sink.EmitDecision(decision)
	if !decision.Allow {
		writeHTTPStatus(client, 403, "forbidden",
			"x-proxy-error: "+httpProxyErrorTag(decision.Reason)+"\r\n"+
				"x-proxy-source: "+string(decision.Source))
		return
	}

	// Tunnel: dial upstream, write 200, splice both ways.
	dialer := &net.Dialer{Timeout: connectIOTimeout}
	upstream, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		writeHTTPStatus(client, 502, "bad gateway", "x-proxy-error: upstream-dial-failed")
		sink.EmitError(fmt.Errorf("netproxy http: dial %s:%s: %w", host, port, err))
		return
	}
	defer upstream.Close()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		sink.EmitError(fmt.Errorf("netproxy http: write 200: %w", err))
		return
	}
	splice(client, upstream, br)
}

// httpProxyErrorTag maps a deny Reason to the Codex
// `x-proxy-error` header value (Codex network-proxy/README.md:90-103).
func httpProxyErrorTag(reason ProxyReason) string {
	switch reason {
	case ReasonNotInAllowlist:
		return "blocked-by-allowlist"
	case ReasonDeniedByRule:
		return "blocked-by-denylist"
	case ReasonModeGuard, ReasonProtocolUnsupported:
		return "blocked-by-method-policy"
	case ReasonDNSResolutionBlocked:
		return "blocked-by-dns-resolution"
	case ReasonLocalBindingDisabled:
		return "blocked-by-local-binding"
	case ReasonInvalidConfig:
		return "blocked-by-invalid-config"
	default:
		return "blocked-by-policy"
	}
}

// splitHostPortBestEffort returns host, port even if the target is
// not a clean host:port. Used only for diagnostic Decision fields.
func splitHostPortBestEffort(target string) (string, int) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return target, 0
	}
	p, _ := strconv.Atoi(port)
	return host, p
}

func writeHTTPStatus(w io.Writer, code int, reason, extraHeaders string) {
	statusLine := fmt.Sprintf("HTTP/1.1 %d %s\r\n", code, reason)
	hdr := "Content-Length: 0\r\nConnection: close\r\n"
	if extraHeaders != "" {
		hdr += extraHeaders
		if !strings.HasSuffix(hdr, "\r\n") {
			hdr += "\r\n"
		}
	}
	hdr += "\r\n"
	_, _ = io.WriteString(w, statusLine+hdr)
}

// splice copies bytes both ways between client and upstream. The
// supplied bufio.Reader holds any bytes already buffered on client
// after CONNECT — those must be drained into upstream first.
//
// N2 hardening: when both sides are *net.TCPConn the function uses
// CloseWrite half-close after the first goroutine finishes so the
// second copy sees a natural EOF and finishes its in-flight buffer
// instead of being interrupted by SetDeadline(time.Now()) which can
// truncate a large response. Falls back to the deadline-as-cancel
// pattern for non-TCPConn endpoints (rare; most splice paths use
// real TCP).
func splice(client net.Conn, upstream net.Conn, br *bufio.Reader) {
	if br != nil && br.Buffered() > 0 {
		buffered, _ := br.Peek(br.Buffered())
		_, _ = upstream.Write(buffered)
		_, _ = br.Discard(br.Buffered())
	}
	type direction struct {
		dst, src net.Conn
	}
	clientToUpstream := direction{dst: upstream, src: client}
	upstreamToClient := direction{dst: client, src: upstream}

	done := make(chan direction, 2)
	go func() { _, _ = io.Copy(clientToUpstream.dst, clientToUpstream.src); done <- clientToUpstream }()
	go func() { _, _ = io.Copy(upstreamToClient.dst, upstreamToClient.src); done <- upstreamToClient }()

	first := <-done
	// Half-close the destination so the still-active goroutine sees
	// a natural EOF on its source. This avoids the truncation race
	// where SetDeadline(time.Now()) interrupts an in-flight buffer
	// flush.
	if tcp, ok := first.dst.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	} else {
		// Non-TCP path: fall back to the deadline-as-cancel
		// pattern. Acceptable for the rare non-TCP splice (no
		// production path currently triggers this).
		_ = first.dst.SetDeadline(time.Now())
		_ = first.src.SetDeadline(time.Now())
	}
	<-done
}

// ServeSOCKS5 runs a SOCKS5 listener that supports only the TCP
// CONNECT command (0x01). No-auth (method 0x00) is the only
// supported method. UDP ASSOCIATE (0x03), BIND (0x02), GSSAPI auth,
// and username/password auth are all rejected with the appropriate
// RFC1928 reply / method codes.
//
// Returns nil on graceful shutdown; non-graceful accept errors are
// emitted to sink.EmitError and the loop exits.
func ServeSOCKS5(
	ctx context.Context,
	listener net.Listener,
	allow Allowlist,
	deny Denylist,
	resolver Resolver,
	sink EventSink,
) error {
	if sink == nil {
		return errors.New("netproxy: sink required")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if isClosedListenerErr(err) || ctx.Err() != nil {
				return nil
			}
			sink.EmitError(fmt.Errorf("netproxy: socks5 accept: %w", err))
			return err
		}
		go handleSOCKS5(ctx, conn, allow, deny, resolver, sink)
	}
}

// SOCKS5 reply codes per RFC 1928 §6.
const (
	socks5ReplySucceeded                byte = 0x00
	socks5ReplyConnectionNotAllowed     byte = 0x02
	socks5ReplyNetworkUnreachable       byte = 0x03
	socks5ReplyHostUnreachable          byte = 0x04
	socks5ReplyConnectionRefused        byte = 0x05
	socks5ReplyCommandNotSupported      byte = 0x07
	socks5ReplyAddressTypeNotSupported  byte = 0x08
)

func handleSOCKS5(
	ctx context.Context,
	client net.Conn,
	allow Allowlist,
	deny Denylist,
	resolver Resolver,
	sink EventSink,
) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(connectIOTimeout))

	br := bufio.NewReader(client)

	// --- Greeting: VER, NMETHODS, METHODS[NMETHODS] ---
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil {
		sink.EmitError(fmt.Errorf("netproxy socks5: read greet: %w", err))
		return
	}
	if hdr[0] != 0x05 {
		// Wrong protocol version — close without a reply (the client
		// is speaking something else, possibly SOCKS4).
		sink.EmitError(fmt.Errorf("netproxy socks5: unsupported version 0x%x", hdr[0]))
		return
	}
	nMethods := int(hdr[1])
	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(br, methods); err != nil {
		sink.EmitError(fmt.Errorf("netproxy socks5: read methods: %w", err))
		return
	}
	noAuthAvailable := false
	for _, m := range methods {
		if m == 0x00 {
			noAuthAvailable = true
			break
		}
	}
	if !noAuthAvailable {
		// Indicate "no acceptable methods" per RFC 1928 §3.
		_, _ = client.Write([]byte{0x05, 0xff})
		d, _ := NewDeny(SourceNetworkProxy, ReasonModeGuard, "", 0, ProtocolSOCKS5TCP)
		sink.EmitDecision(d)
		return
	}
	if _, err := client.Write([]byte{0x05, 0x00}); err != nil {
		sink.EmitError(fmt.Errorf("netproxy socks5: write greet ack: %w", err))
		return
	}

	// --- Request: VER, CMD, RSV, ATYP, DST.ADDR, DST.PORT ---
	reqHdr := make([]byte, 4)
	if _, err := io.ReadFull(br, reqHdr); err != nil {
		sink.EmitError(fmt.Errorf("netproxy socks5: read request hdr: %w", err))
		return
	}
	if reqHdr[0] != 0x05 {
		sink.EmitError(fmt.Errorf("netproxy socks5: bad version in request: 0x%x", reqHdr[0]))
		return
	}
	cmd := reqHdr[1]
	atyp := reqHdr[3]

	// Parse DST.ADDR + DST.PORT regardless of cmd so the EmitDecision
	// payload can include them on rejects.
	host, port, err := readSOCKS5DstAddrPort(br, atyp)
	if err != nil {
		writeSOCKS5Reply(client, socks5ReplyAddressTypeNotSupported, atyp)
		d, _ := NewDeny(SourceNetworkProxy, ReasonInvalidConfig, "", 0, ProtocolSOCKS5TCP)
		sink.EmitDecision(d)
		return
	}

	if cmd != 0x01 {
		// 0x02 BIND, 0x03 UDP ASSOCIATE, anything else.
		writeSOCKS5Reply(client, socks5ReplyCommandNotSupported, atyp)
		d, _ := NewDeny(SourceNetworkProxy, ReasonProtocolUnsupported, host, port, ProtocolSOCKS5TCP)
		sink.EmitDecision(d)
		return
	}

	_ = client.SetDeadline(time.Time{})

	decision := EvaluateWithDenylist(ctx, allow, deny, host, port, ProtocolSOCKS5TCP, resolver)
	sink.EmitDecision(decision)
	if !decision.Allow {
		writeSOCKS5Reply(client, socks5ReplyConnectionNotAllowed, atyp)
		return
	}

	dialer := &net.Dialer{Timeout: connectIOTimeout}
	upstream, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		writeSOCKS5Reply(client, socks5ReplyHostUnreachable, atyp)
		sink.EmitError(fmt.Errorf("netproxy socks5: dial %s:%d: %w", host, port, err))
		return
	}
	defer upstream.Close()
	writeSOCKS5Reply(client, socks5ReplySucceeded, atyp)
	splice(client, upstream, br)
}

// readSOCKS5DstAddrPort parses DST.ADDR + DST.PORT from r given the
// declared ATYP byte. Returns (host, port, err). For ATYP=0x03
// (DOMAINNAME) the leading length byte is consumed.
func readSOCKS5DstAddrPort(r io.Reader, atyp byte) (string, int, error) {
	switch atyp {
	case 0x01: // IPv4
		buf := make([]byte, 4)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", 0, err
		}
		ip := net.IP(buf).String()
		port, err := readSOCKS5Port(r)
		return ip, port, err
	case 0x03: // DOMAINNAME
		l := make([]byte, 1)
		if _, err := io.ReadFull(r, l); err != nil {
			return "", 0, err
		}
		buf := make([]byte, int(l[0]))
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", 0, err
		}
		port, err := readSOCKS5Port(r)
		return string(buf), port, err
	case 0x04: // IPv6
		buf := make([]byte, 16)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", 0, err
		}
		ip := net.IP(buf).String()
		port, err := readSOCKS5Port(r)
		return ip, port, err
	default:
		return "", 0, fmt.Errorf("netproxy socks5: unsupported atyp 0x%x", atyp)
	}
}

func readSOCKS5Port(r io.Reader) (int, error) {
	buf := make([]byte, 2)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint16(buf)), nil
}

// writeSOCKS5Reply writes a SOCKS5 reply with the given code. BND.ADDR
// is left as 0.0.0.0 (or :: for IPv6) and BND.PORT as 0 because the
// proxy does not bind a stable address that the client can use; this
// matches Codex's pattern (bind addr is informational only). atyp
// reflects the request's atyp so the parser on the client side
// always reads the right number of bytes.
func writeSOCKS5Reply(w io.Writer, code byte, atyp byte) {
	var bnd []byte
	switch atyp {
	case 0x04:
		bnd = make([]byte, 16+2)
	default:
		// For ATYP=0x03 (DOMAINNAME) reply, RFC1928 says the reply
		// MUST use a fixed atyp; we use 0x01 (IPv4) with 0.0.0.0:0.
		atyp = 0x01
		bnd = make([]byte, 4+2)
	}
	hdr := []byte{0x05, code, 0x00, atyp}
	out := append(hdr, bnd...)
	_, _ = w.Write(out)
}

// ---------------------------------------------------------------
// Convenience sink implementations
// ---------------------------------------------------------------

// ChannelEventSink fans Decisions and errors into separate channels
// with a bounded buffer per channel. When the buffer is full,
// EmitDecision/EmitError DROP the event rather than blocking; this
// preserves the partner-locked invariant that the accept loop never
// blocks on telemetry.
//
// dropCount is the number of events that were dropped due to a full
// buffer. Test code can read it to assert correctness.
type ChannelEventSink struct {
	Decisions chan Decision
	Errors    chan error

	mu        sync.Mutex
	dropCount int
}

// NewChannelEventSink returns a sink with the given buffer size. A
// buffer of zero is permitted but every emit will drop unless a
// reader is waiting on the channel.
func NewChannelEventSink(buf int) *ChannelEventSink {
	return &ChannelEventSink{
		Decisions: make(chan Decision, buf),
		Errors:    make(chan error, buf),
	}
}

// EmitDecision sends d to Decisions or drops it if the channel is
// full. Goroutine-safe.
func (s *ChannelEventSink) EmitDecision(d Decision) {
	select {
	case s.Decisions <- d:
	default:
		s.mu.Lock()
		s.dropCount++
		s.mu.Unlock()
	}
}

// EmitError sends err to Errors or drops it if the channel is full.
// Goroutine-safe.
func (s *ChannelEventSink) EmitError(err error) {
	select {
	case s.Errors <- err:
	default:
		s.mu.Lock()
		s.dropCount++
		s.mu.Unlock()
	}
}

// DropCount returns the number of events dropped because the
// corresponding channel was full.
func (s *ChannelEventSink) DropCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropCount
}
