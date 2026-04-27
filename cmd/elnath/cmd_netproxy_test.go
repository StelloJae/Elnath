package main

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// B3b-4-2 Phase A: production `elnath netproxy ...` subcommand handler.
// Cross-platform (no build tag). The handler is a thin wrapper around
// tools.RunProxyChildMain that wires stderr to os.Stderr so --help text
// reaches the operator. Direct unit test of the handler covers the
// happy path (--help) and the bad-args path (parser failure surfaces a
// non-nil error).

func TestCmdNetproxy_HelpFlagPrintsUsageAndReturnsNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, stderr := captureOutput(t, func() {
		if err := cmdNetproxy(ctx, []string{"--help"}); err != nil {
			t.Fatalf("cmdNetproxy(--help): %v", err)
		}
	})
	if !strings.Contains(stderr, "usage: elnath netproxy") {
		t.Errorf("stderr missing usage line; got: %q", stderr)
	}
}

func TestCmdNetproxy_NoListenersOrInvalidConfigReturnsError(t *testing.T) {
	// Without --http-listen / --socks-listen / pre-bound listeners
	// (cross-process invocation has no in-Go listener handles), the
	// child must refuse to start and surface a non-nil error.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := cmdNetproxy(ctx, []string{"--allow", "github.com:443"})
	if err == nil {
		t.Errorf("expected error when no listeners specified")
	}
}

func TestCmdNetproxy_BindFailureReturnsError(t *testing.T) {
	// Use an unparseable address so the child's listener bind step
	// fails. The handler must return a non-nil error.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := cmdNetproxy(ctx, []string{
		"--http-listen", "not-a-real-host:99999",
		"--allow", "github.com:443",
	})
	if err == nil {
		t.Errorf("expected error on bind failure")
	}
}

// TestCmdNetproxy_StructLiteralWiresSystemResolver — Test #12.
// Architect Q4 Layer A. The cmdNetproxy struct literal at
// cmd_netproxy.go:43-49 MUST set Resolver: tools.NewSystemResolver()
// so the production proxy reaches the SSRF guard at
// netproxy_policy.go:328-343 (which is dead code without an injected
// resolver). Verifies via observable: a CONNECT to a .test hostname
// (RFC 6761 reserved TLD; never resolves on the OS resolver) routed
// through the production handler MUST yield a 403 with
// blocked-by-dns-resolution, proving the system resolver was actively
// consulted at policy-evaluation time.
func TestCmdNetproxy_StructLiteralWiresSystemResolver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- cmdNetproxy(ctx, []string{
			"--http-listen", "127.0.0.1:0",
			"--allow", "sentinel.test:443",
		})
	}()

	// The handler binds 127.0.0.1:0 then publishes the bound port via
	// the readiness preamble on stdout. We can't easily read stdout
	// (the production handler routes ReadyWriter to os.Stdout) so we
	// poll a known port range — instead, the simpler harness is to
	// pre-bind a listener and pass it via stdin args. But the handler
	// doesn't accept pre-bound listeners. So we use a different probe:
	// after a short wait, sweep recent TCP listeners on loopback by
	// calling net.Dial with a brief retry and matching on the
	// connection-established + 403 reply pattern. This is brittle, so
	// instead we use a stricter approach: --http-listen with an
	// explicit ephemeral port chosen by us.
	//
	// Bind a probe socket to grab an ephemeral port, close it, and
	// reuse the same address for --http-listen. There's a small race
	// (another process could grab it), but on a clean test machine
	// it's robust.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	probeAddr := probe.Addr().String()
	probe.Close()

	// Restart the handler bound to the probe address.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("first cmdNetproxy did not exit")
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	done2 := make(chan error, 1)
	go func() {
		done2 <- cmdNetproxy(ctx2, []string{
			"--http-listen", probeAddr,
			"--allow", "sentinel.test:443",
		})
	}()

	// Wait briefly for the handler to bind.
	deadline := time.Now().Add(2 * time.Second)
	var conn net.Conn
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", probeAddr)
		if err == nil {
			conn = c
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conn == nil {
		t.Fatal("could not connect to cmdNetproxy after 2s")
	}
	defer conn.Close()

	connReq := "CONNECT sentinel.test:443 HTTP/1.1\r\nHost: sentinel.test:443\r\n\r\n"
	if _, err := conn.Write([]byte(connReq)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(conn)
	statusLine, _ := br.ReadString('\n')
	headers, _ := readHeaders(br)

	// 403 with blocked-by-dns-resolution proves the SystemResolver
	// was wired (NXDOMAIN on .test causes the policy evaluator to
	// emit ReasonDNSResolutionBlocked). 502 (bad gateway) would
	// indicate the dial site received "sentinel.test:443" as a string
	// and tried to resolve it via the dialer, which means the policy
	// guard ran without DNS — i.e., Resolver was NOT wired.
	if !strings.Contains(statusLine, "403") {
		t.Errorf("expected 403 (SystemResolver active); got status=%q headers=%q", statusLine, headers)
	}
	if !strings.Contains(headers, "blocked-by-dns-resolution") {
		t.Errorf("expected blocked-by-dns-resolution header; got %q", headers)
	}

	cancel2()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("cmdNetproxy did not exit after cancel")
	}
}

func readHeaders(br *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return sb.String(), err
		}
		sb.WriteString(line)
		if line == "\r\n" || line == "\n" {
			return sb.String(), nil
		}
	}
}
