//go:build darwin

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestDefaultSeatbeltProfile_AllowsSessionWritesAndDeniesDefault(t *testing.T) {
	req := BashRunRequest{SessionDir: "/private/tmp/elnath-test-session"}
	p := defaultSeatbeltProfile(req)

	required := []string{
		"(version 1)",
		"(deny default)",
		`(allow file-write* (subpath "/private/tmp/elnath-test-session"))`,
		"(allow file-read*)",
		"(allow process-exec)",
		// B3b-2 = filesystem-only prototype; network unrestricted on
		// purpose. Network deny lands in B3b-2.5.
		"(allow network*)",
	}
	for _, want := range required {
		if !strings.Contains(p, want) {
			t.Errorf("default profile missing %q:\n%s", want, p)
		}
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
