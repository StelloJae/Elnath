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
	"testing"
	"time"
)

// netproxy_bridge_spike covers the v41 / B3b-4-S0 spike. It proves that
// the architect's bwrap + self-exec + UDS-bridge design is buildable in
// pure Go end-to-end. NOT a production proxy: blind TCP forwarding
// only, no HTTP CONNECT parsing, no SOCKS5 framing, no domain policy.
//
// What this test proves (mapped to the 9 acceptance items):
//
//  1. bwrap --unshare-net starts                     — bwrap argv accepted
//  2. self-exec bridge runs inside the netns          — child exit code 0
//  3. bridge binds TCP loopback inside netns          — curl reaches it
//  4. bridge forwards one connection to host UDS      — body round-trip
//  5. user command sees the bridge as its endpoint    — curl GET succeeds
//  6. direct egress without bridge fails              — control test below
//  7. bridge exits cleanly when bwrap exits           — wait + pgrep check
//  8. no orphan bridge process remains                — pgrep returns none
//  9. CI evidence: this whole file runs under         — sandbox-linux.yml
//     `go test -tags=integration -race -count=1
//      -run NetnsBridgeSpike ./internal/tools/...`
//
// Build tags pin the file to Linux+integration so darwin developer
// machines (which lack bwrap) skip it at compile time and the unit
// test job (no `-tags=integration`) does not try to launch bwrap.

const spikeBridgeProcessTag = "netproxy-bridge-spike"

func skipIfBwrapUnavailableSpike(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("netns bridge spike is linux-only")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skipf("bwrap not on PATH: %v", err)
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skipf("curl not on PATH: %v", err)
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

// buildElnathBinary compiles the elnath binary into a tempdir so the
// bwrap argv can pass an absolute path. Using `/proc/self/exe` would
// resolve to the `go test` binary, not elnath, so an explicit build
// is required.
func buildElnathBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "elnath-spike")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/elnath")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build elnath: %v\n%s", err, out)
	}
	return binPath
}

// repoRoot walks up from the test file until it finds go.mod so the
// `go build` invocation above runs from the module root regardless of
// the test's CWD.
func repoRoot(t *testing.T) string {
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

// startHostUDSForwarder binds a Unix listener at udsPath and forwards
// every accepted connection to tcpEndpoint. Returns a stop func that
// closes the listener; outstanding goroutines die with the closed
// listener and accepted streams complete naturally on EOF.
func startHostUDSForwarder(t *testing.T, udsPath, tcpEndpoint string) func() {
	t.Helper()
	if err := os.RemoveAll(udsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove stale UDS: %v", err)
	}
	listener, err := net.Listen("unix", udsPath)
	if err != nil {
		t.Fatalf("listen unix %s: %v", udsPath, err)
	}
	if err := os.Chmod(udsPath, 0o666); err != nil {
		t.Fatalf("chmod UDS: %v", err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(uds net.Conn) {
				defer uds.Close()
				tcp, err := net.Dial("tcp", tcpEndpoint)
				if err != nil {
					return
				}
				defer tcp.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(tcp, uds); done <- struct{}{} }()
				go func() { _, _ = io.Copy(uds, tcp); done <- struct{}{} }()
				<-done
			}(conn)
		}
	}()
	return func() { _ = listener.Close() }
}

// pgrepBridge returns true if any process whose argv contains the
// spike marker is currently alive. Used to assert acceptance items 7
// and 8 (no orphan after a successful run).
func pgrepBridge(t *testing.T) bool {
	t.Helper()
	cmd := exec.Command("pgrep", "-f", spikeBridgeProcessTag)
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

// TestNetnsBridgeSpike_PositiveRoundTrip exercises the full happy path.
// Maps to acceptance items 1, 2, 3, 4, 5: bwrap launches the bridge
// inside an unshared netns, the bridge binds 127.0.0.1:<port>, curl
// inside the netns hits that port, the bridge proxies through the
// bind-mounted UDS to the host httptest.Server, the response body
// reaches curl. After the run, the bridge has exited cleanly (item 7).
func TestNetnsBridgeSpike_PositiveRoundTrip(t *testing.T) {
	skipIfBwrapUnavailableSpike(t)
	binPath := buildElnathBinary(t)

	const wantBody = "spike-pong-7c3f9a"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, wantBody)
	}))
	defer server.Close()
	tcpEndpoint := strings.TrimPrefix(server.URL, "http://")

	udsDir := t.TempDir()
	udsPath := filepath.Join(udsDir, "spike.sock")
	stopFwd := startHostUDSForwarder(t, udsPath, tcpEndpoint)
	defer stopFwd()

	const listenAddr = "127.0.0.1:18888"
	userCmd := fmt.Sprintf(
		"curl --silent --show-error --max-time 5 http://%s/spike",
		listenAddr,
	)

	args := []string{
		"--unshare-user",
		"--unshare-pid",
		"--unshare-net",
		"--die-with-parent",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	}
	for _, p := range bwrapHostReadBinds {
		args = append(args, "--ro-bind-try", p, p)
	}
	args = append(args,
		"--ro-bind", binPath, binPath,
		"--bind", udsDir, udsDir,
		"--",
		binPath, "netproxy-bridge-spike",
		"--uds", udsPath,
		"--listen", listenAddr,
		"--user-cmd", userCmd,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bwrap", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bwrap: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), wantBody) {
		t.Fatalf("expected response body %q in user-cmd output:\n%s", wantBody, out)
	}

	// Acceptance items 7+8: after bwrap returns, no orphan bridge
	// must remain. The bridge is the bwrap-spawned wrapper, so if it
	// exited cleanly there is nothing matching the spike tag.
	time.Sleep(200 * time.Millisecond)
	if pgrepBridge(t) {
		t.Errorf("orphan netproxy-bridge-spike process detected after run")
	}
}

// TestNetnsBridgeSpike_DirectEgressBlocked is the negative control for
// acceptance item 6. It runs curl inside bwrap WITHOUT going through
// the bridge subcommand and asserts the connection fails. Without a
// failing direct test the positive test does not prove that the
// netns is actually isolated; the bridge could theoretically be
// reaching the host directly.
func TestNetnsBridgeSpike_DirectEgressBlocked(t *testing.T) {
	skipIfBwrapUnavailableSpike(t)

	args := []string{
		"--unshare-user",
		"--unshare-pid",
		"--unshare-net",
		"--die-with-parent",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	}
	for _, p := range bwrapHostReadBinds {
		args = append(args, "--ro-bind-try", p, p)
	}
	args = append(args,
		"--",
		"/bin/sh", "-c",
		"curl --silent --show-error --connect-timeout 2 --max-time 4 http://1.1.1.1/ ; echo EXIT=$?",
	)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bwrap", args...)
	out, _ := cmd.CombinedOutput()
	body := string(out)

	// curl must report a failure: either Network is unreachable
	// (typical for an empty netns), Connection refused, or a timeout.
	// We accept any non-zero exit (recorded by the EXIT marker) and
	// assert the body did NOT contain a successful HTTP response
	// indicator from the upstream.
	if !strings.Contains(body, "EXIT=") {
		t.Fatalf("expected EXIT= marker; output was:\n%s", body)
	}
	if strings.Contains(body, "EXIT=0") {
		t.Fatalf("direct egress unexpectedly succeeded — netns isolation broken; output:\n%s", body)
	}

	// Item 8 again, even on the negative path: nothing should be left
	// over.
	if pgrepBridge(t) {
		t.Errorf("orphan netproxy-bridge-spike process detected after negative test")
	}
}
