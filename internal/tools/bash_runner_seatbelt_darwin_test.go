//go:build darwin

package tools

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Darwin-only runtime tests: actually invoke /usr/bin/sandbox-exec to
// verify the SBPL profile enforces session-scoped writes. These tests
// require an active macOS sandbox-exec binary; they live behind the
// darwin build tag so they never compile on Linux/Windows.

func seatbeltSessionDirs(t *testing.T) (sessionDir, outsideDir string) {
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

func TestSeatbeltRunner_AllowsSessionWrite(t *testing.T) {
	sessionDir, _ := seatbeltSessionDirs(t)

	runner := NewSeatbeltRunner()
	req := BashRunRequest{
		Command:    "echo allowed > inside.txt",
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := runner.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success: %s", res.Output)
	}

	target := filepath.Join(sessionDir, "inside.txt")
	data, statErr := os.ReadFile(target)
	if statErr != nil {
		t.Fatalf("expected file at %s: %v", target, statErr)
	}
	if !strings.Contains(string(data), "allowed") {
		t.Errorf("file content = %q, want substring 'allowed'", string(data))
	}
}

func TestSeatbeltRunner_BlocksOutsideWrite(t *testing.T) {
	sessionDir, outsideDir := seatbeltSessionDirs(t)
	sentinel := filepath.Join(outsideDir, "leak.txt")

	runner := NewSeatbeltRunner()
	req := BashRunRequest{
		Command:    fmt.Sprintf(`echo leak > %q`, sentinel),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := runner.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error for write outside session, got: %s", res.Output)
	}
	if _, statErr := os.Stat(sentinel); statErr == nil {
		_ = os.Remove(sentinel)
		t.Fatalf("file leaked outside session at %s — Seatbelt did not block", sentinel)
	}
	// Best-effort violation surfacing: the SBPL deny may emit
	// "Operation not permitted" on stderr. If we detected it, the
	// runner should populate Violations; if the kernel logged via a
	// different channel, Violations may stay empty (heuristic).
	_ = res.Violations
}

func TestSeatbeltRunner_PerInvocationProfileCleanup(t *testing.T) {
	sessionDir, _ := seatbeltSessionDirs(t)

	runner := NewSeatbeltRunner()
	req := BashRunRequest{
		Command:    "true",
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	}

	pattern := filepath.Join(os.TempDir(), "elnath-seatbelt-*.sb")
	before, _ := filepath.Glob(pattern)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := runner.Run(ctx, req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	after, _ := filepath.Glob(pattern)
	if len(after) > len(before) {
		t.Errorf("profile temp file leaked: %d before / %d after — per-invocation cleanup failed", len(before), len(after))
	}
}

func TestSeatbeltRunner_PreservesB1MetadataShape(t *testing.T) {
	sessionDir, _ := seatbeltSessionDirs(t)

	runner := NewSeatbeltRunner()
	req := BashRunRequest{
		Command:    "echo metadata-marker",
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := runner.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success: %s", res.Output)
	}
	for _, want := range []string{
		"BASH RESULT",
		"status: success",
		"metadata-marker",
	} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("expected %q in output, got:\n%s", want, res.Output)
		}
	}
	if res.ExitCode == nil || *res.ExitCode != 0 {
		t.Errorf("expected exit_code=0, got %v", res.ExitCode)
	}
}

func TestSeatbeltProfile_DefaultDenyContent(t *testing.T) {
	req := BashRunRequest{SessionDir: "/private/tmp/elnath-test-session"}
	p := seatbeltProfile(req, nil)

	required := []string{
		"(version 1)",
		"(deny default)",
		`(allow file-write* (subpath "/private/tmp/elnath-test-session"))`,
		"(allow file-read*)",
		"(allow process-exec)",
	}
	for _, want := range required {
		if !strings.Contains(p, want) {
			t.Errorf("profile missing %q:\n%s", want, p)
		}
	}
	// B3b-2.5 baseline: no (allow network*) — default-deny network.
	if strings.Contains(p, "(allow network*)") {
		t.Errorf("profile must NOT contain (allow network*) under default-deny:\n%s", p)
	}
	// No allowlist entries → no (allow network-outbound ...) lines.
	if strings.Contains(p, "(allow network-outbound") {
		t.Errorf("empty allowlist should produce zero network-outbound lines:\n%s", p)
	}
}

func TestSeatbeltProfile_AllowlistEmitsLocalhostEntries(t *testing.T) {
	// Seatbelt SBPL only accepts "*" or "localhost" as the host part of
	// (remote ip "host:port") — non-localhost IPs are rejected by the
	// SBPL parser. Phase 1 of B3b-2.5 therefore restricts the allowlist
	// to loopback IPs and emits the SBPL-acceptable "localhost:port"
	// form regardless of which loopback variant the caller specified.
	req := BashRunRequest{SessionDir: "/private/tmp/x"}
	allowlist := []string{"127.0.0.1:5555", "[::1]:8080"}
	p := seatbeltProfile(req, allowlist)

	for _, port := range []string{"5555", "8080"} {
		want := fmt.Sprintf(`(allow network-outbound (remote ip "localhost:%s"))`, port)
		if !strings.Contains(p, want) {
			t.Errorf("profile missing localhost rule for port %s:\n%s", port, p)
		}
	}
	// The raw "127.0.0.1" or "::1" host strings must NOT appear in
	// the profile because Seatbelt rejects them; the translation to
	// "localhost" is what makes the allowlist actually enforce.
	if strings.Contains(p, "127.0.0.1:") {
		t.Errorf("profile must translate 127.0.0.1 to localhost:\n%s", p)
	}
	if !strings.Contains(p, "(deny default)") {
		t.Errorf("profile must keep (deny default) baseline:\n%s", p)
	}
}

// startTestTCPServer starts a goroutine listener on 127.0.0.1:0 and
// returns the bound port plus a flag that flips to true once any
// client connects. Cleanup closes the listener at test end.
func startTestTCPServer(t *testing.T) (port int, accepted *atomic.Bool) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	addr := listener.Addr().(*net.TCPAddr)
	accepted = &atomic.Bool{}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Store(true)
			_ = conn.Close()
		}
	}()
	return addr.Port, accepted
}

func TestSeatbeltNetwork_DefaultDenyBlocksLocalTCP(t *testing.T) {
	port, accepted := startTestTCPServer(t)
	sessionDir, _ := seatbeltSessionDirs(t)

	runner, err := NewSeatbeltRunnerWithAllowlist(nil)
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist(nil): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, runErr := runner.Run(ctx, BashRunRequest{
		Command:    fmt.Sprintf("nc -z -w 2 127.0.0.1 %d", port),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}

	if !res.IsError {
		t.Errorf("expected nc connection to fail under default-deny network; output: %s", res.Output)
	}
	// Give the kernel a beat to settle in case a SYN raced the deny.
	time.Sleep(150 * time.Millisecond)
	if accepted.Load() {
		t.Errorf("server accepted connection — Seatbelt did NOT block default-deny outbound TCP")
	}
}

func TestSeatbeltNetwork_AllowlistAllowsExactIPPort(t *testing.T) {
	port, accepted := startTestTCPServer(t)
	sessionDir, _ := seatbeltSessionDirs(t)

	allowlist := []string{fmt.Sprintf("127.0.0.1:%d", port)}
	runner, err := NewSeatbeltRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, runErr := runner.Run(ctx, BashRunRequest{
		Command:    fmt.Sprintf("nc -z -w 2 127.0.0.1 %d", port),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if res.IsError {
		t.Fatalf("expected allowlisted endpoint to be reachable; output: %s", res.Output)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if accepted.Load() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("server did NOT accept connection — Seatbelt allowlist did not permit 127.0.0.1:%d", port)
}

func TestSeatbeltNetwork_AllowlistDeniesDifferentPort(t *testing.T) {
	allowedPort, _ := startTestTCPServer(t)
	deniedPort, deniedAccepted := startTestTCPServer(t)
	sessionDir, _ := seatbeltSessionDirs(t)

	allowlist := []string{fmt.Sprintf("127.0.0.1:%d", allowedPort)}
	runner, err := NewSeatbeltRunnerWithAllowlist(allowlist)
	if err != nil {
		t.Fatalf("NewSeatbeltRunnerWithAllowlist: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, runErr := runner.Run(ctx, BashRunRequest{
		Command:    fmt.Sprintf("nc -z -w 2 127.0.0.1 %d", deniedPort),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if !res.IsError {
		t.Errorf("expected nc to fail to non-allowlisted port; output: %s", res.Output)
	}
	time.Sleep(150 * time.Millisecond)
	if deniedAccepted.Load() {
		t.Errorf("server on non-allowlisted port accepted connection — allowlist scoping failed")
	}
}

// The pre-B3b-4-2 TestSeatbeltNetwork_DomainAllowlistRejected was
// removed when the factory began spawning a netproxy child for
// domain entries instead of rejecting them. The new behavior is
// covered by TestSeatbeltRunner_DomainAllowlistSpawnsProxyChildAndCapturesPorts
// (unit-level, uses binary-override mechanism) and the integration
// suite `TestSeatbeltProxyIntegration_AllowedDomainHTTPRequestSucceeds`
// (sandbox-exec + real proxy round-trip).

func TestSeatbeltNetwork_FactoryRejectsInvalidAllowlist(t *testing.T) {
	// A malformed allowlist must not yield a runner — silent fallback to
	// DirectRunner or a no-policy SeatbeltRunner is the failure mode the
	// v41 verdict explicitly closed.
	_, err := NewBashRunnerForConfig(SandboxConfig{
		Mode:             "seatbelt",
		NetworkAllowlist: []string{"not-an-ip-or-port"},
	})
	if err == nil {
		t.Fatalf("expected validation error from factory")
	}
}

func TestSeatbeltRunner_GitToolUnderRunnerStaysContained(t *testing.T) {
	// Carry the B3b-1.5 host HOME .gitconfig fsmonitor regression
	// across the substrate boundary: even when GitTool runs through
	// SeatbeltRunner the fsmonitor must not fire because cleanBashEnv
	// pins HOME to the session workspace and SBPL does not re-expose
	// the host home.
	fakeHome := t.TempDir()
	sentinelDir := t.TempDir()
	sentinel := filepath.Join(sentinelDir, "fsmonitor-sentinel")
	gitconfig := fmt.Sprintf("[core]\n\tfsmonitor = sh -c 'touch %s; echo {}'\n", sentinel)
	if err := os.WriteFile(filepath.Join(fakeHome, ".gitconfig"), []byte(gitconfig), 0o644); err != nil {
		t.Fatalf("write fake gitconfig: %v", err)
	}
	t.Setenv("HOME", fakeHome)

	dir := setupGitRepo(t)
	gt := NewGitToolWithRunner(NewPathGuard(dir, nil), NewSeatbeltRunner())

	if _, err := gt.Execute(context.Background(), mustMarshal(t, map[string]any{
		"subcommand": "status",
	})); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatalf("host HOME .gitconfig fsmonitor was triggered under SeatbeltRunner — HOME leakage NOT prevented")
	}
}
