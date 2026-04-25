//go:build linux

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

// Linux-only runtime tests: actually invoke bwrap to verify the
// substrate enforces filesystem and network policy. These tests run on
// CI and on developer machines that have bwrap installed; they skip
// gracefully when the substrate is unavailable so the suite keeps
// running on hosts that lack user namespaces.

func skipIfBwrapUnavailable(t *testing.T) *BwrapRunner {
	t.Helper()
	r := NewBwrapRunner()
	p := r.Probe(context.Background())
	if !p.Available {
		t.Skipf("bwrap unavailable on this host: %s", p.Message)
	}
	return r
}

func bwrapSessionDirs(t *testing.T) (sessionDir, outsideDir string) {
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

func TestBwrapRunner_AllowsSessionWrite(t *testing.T) {
	r := skipIfBwrapUnavailable(t)
	sessionDir, _ := bwrapSessionDirs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := r.Run(ctx, BashRunRequest{
		Command:    "echo allowed > inside.txt",
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
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

func TestBwrapRunner_BlocksOutsideWrite(t *testing.T) {
	r := skipIfBwrapUnavailable(t)
	sessionDir, outsideDir := bwrapSessionDirs(t)
	sentinel := filepath.Join(outsideDir, "leak.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := r.Run(ctx, BashRunRequest{
		Command:    fmt.Sprintf(`echo leak > %q`, sentinel),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error for write outside session, got: %s", res.Output)
	}
	if _, statErr := os.Stat(sentinel); statErr == nil {
		_ = os.Remove(sentinel)
		t.Fatalf("file leaked outside session at %s — bwrap did not block", sentinel)
	}
}

func TestBwrapRunner_PreservesB1MetadataShape(t *testing.T) {
	r := skipIfBwrapUnavailable(t)
	sessionDir, _ := bwrapSessionDirs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := r.Run(ctx, BashRunRequest{
		Command:    "echo metadata-marker",
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success: %s", res.Output)
	}
	for _, want := range []string{"BASH RESULT", "status: success", "metadata-marker"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("expected %q in output:\n%s", want, res.Output)
		}
	}
	if res.ExitCode == nil || *res.ExitCode != 0 {
		t.Errorf("expected exit_code=0, got %v", res.ExitCode)
	}
}

func TestBwrapRunner_DefaultDenyNetwork(t *testing.T) {
	r := skipIfBwrapUnavailable(t)
	sessionDir, _ := bwrapSessionDirs(t)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	accepted := &atomic.Bool{}
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, runErr := r.Run(ctx, BashRunRequest{
		Command:    fmt.Sprintf("nc -z -w 2 127.0.0.1 %d", port),
		WorkDir:    sessionDir,
		SessionDir: sessionDir,
		DisplayCWD: ".",
	})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}

	// The host listener and the sandboxed bash live in separate network
	// namespaces — the connection cannot succeed and the host listener
	// must never see a SYN.
	if !res.IsError {
		t.Errorf("expected nc to fail under bwrap --unshare-net; output: %s", res.Output)
	}
	time.Sleep(150 * time.Millisecond)
	if accepted.Load() {
		t.Errorf("host listener accepted a connection — bwrap did not isolate the network namespace")
	}
}

func TestBwrapRunner_HostHomeGitconfigNotLoaded(t *testing.T) {
	r := skipIfBwrapUnavailable(t)

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
		t.Fatalf("host HOME .gitconfig fsmonitor was triggered under BwrapRunner — HOME leakage NOT prevented")
	}
}

func TestBwrapRunner_ProbeReportsAvailableOnHealthyHost(t *testing.T) {
	// On CI hosts where bwrap is installed and userns works the runner
	// must report Available=true with all three enforcement flags set.
	// On hosts without bwrap or with restricted userns the runner must
	// report Available=false with a diagnostic message — never an
	// available-but-broken middle ground.
	r := NewBwrapRunner()
	p := r.Probe(context.Background())
	if p.Available {
		if !p.FilesystemEnforced || !p.NetworkEnforced || !p.SandboxEnforced {
			t.Errorf("Available=true but enforcement flags incomplete: %+v", p)
		}
		return
	}
	if p.FilesystemEnforced || p.NetworkEnforced || p.SandboxEnforced {
		t.Errorf("Available=false but enforcement flags claim true: %+v", p)
	}
	if p.Message == "" {
		t.Errorf("unavailable probe must include a diagnostic message")
	}
}
