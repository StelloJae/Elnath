//go:build linux

package main

import (
	"bytes"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// B3b-4-3 M-3 unit coverage. The bridge subcommand's per-connection
// goroutine (bridgeConn) must surface UDS dial failures to stderr so
// the parent BwrapRunner has signal to act on. Pre-fix the function
// silently returned, leaving zero trace and amplifying the M-1
// race-window symptom: a dead proxy child produced a bare network
// failure in BashRunResult.Output with no diagnostic to disambiguate
// "user network call failed" from "supervised proxy died".
//
// The runtime contract being pinned: when the UDS path does not exist
// (or is not a socket), bridgeConn closes the accepted TCP connection
// promptly AND writes a single `netproxy-bridge: dial <path>: <err>`
// line to the supplied stderr writer. Lines are intentionally not
// timestamped — the parent's slog wrap stamps the timestamp.

// TestBridgeConn_UDSDialFailureLogsToStderr asserts the M-3 fix:
// when net.Dial("unix", path) fails (path missing, kernel refuses,
// etc.), bridgeConn must NOT silently return; it must emit a single
// diagnostic line to the provided stderr writer.
func TestBridgeConn_UDSDialFailureLogsToStderr(t *testing.T) {
	// Create a TCP pair so bridgeConn has a real net.Conn to defer-
	// Close. Anything that supports Close() is fine; we use a real
	// loopback dial because bridgeConn does not parse anything.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	tcpConn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer tcpConn.Close()
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer accepted.Close()

	// Point at a non-existent UDS path so net.Dial fails fast.
	udsPath := filepath.Join(t.TempDir(), "missing.sock")
	var stderr bytes.Buffer

	done := make(chan struct{})
	go func() {
		defer close(done)
		bridgeConn(accepted, udsPath, &stderr)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bridgeConn did not return within 2s after UDS dial failure")
	}

	got := stderr.String()
	if !strings.Contains(got, "netproxy-bridge: dial") {
		t.Errorf("stderr missing diagnostic line; got: %q", got)
	}
	if !strings.Contains(got, udsPath) {
		t.Errorf("stderr missing UDS path; got: %q", got)
	}
}
