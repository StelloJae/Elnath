package wiki

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestGitSync(t *testing.T) (*GitSync, string) {
	t.Helper()
	dir := t.TempDir()
	g := NewGitSync(dir, slog.Default())
	return g, dir
}

func TestGitSyncInitCreatesGitDir(t *testing.T) {
	g, dir := newTestGitSync(t)

	if err := g.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		t.Errorf(".git directory not created after Init()")
	}
}

func TestGitSyncInitIdempotent(t *testing.T) {
	g, _ := newTestGitSync(t)

	if err := g.Init(); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if err := g.Init(); err != nil {
		t.Fatalf("second Init: %v", err)
	}
}

func TestGitSyncCommitAfterFileCreate(t *testing.T) {
	g, dir := newTestGitSync(t)

	if err := g.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "page.md"), []byte("# Hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := g.Commit("add page.md"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Verify the file is tracked (git status should be clean).
	out, err := runGitCmd(dir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected clean working tree after Commit, got: %q", out)
	}
}

func TestGitSyncCommitNoChangesIsNoop(t *testing.T) {
	g, dir := newTestGitSync(t)

	if err := g.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Seed an initial commit so HEAD exists.
	if err := os.WriteFile(filepath.Join(dir, "seed.md"), []byte("seed"), 0o644); err != nil {
		t.Fatalf("WriteFile seed: %v", err)
	}
	if err := g.Commit("seed"); err != nil {
		t.Fatalf("first Commit: %v", err)
	}

	// Commit with no changes should not error.
	if err := g.Commit("no-op"); err != nil {
		t.Fatalf("no-op Commit: %v", err)
	}
}

func TestGitSyncCommitMessageInLog(t *testing.T) {
	g, dir := newTestGitSync(t)

	if err := g.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	msg := "feat: initial wiki page"
	if err := g.Commit(msg); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	log, err := runGitCmd(dir, "log", "--oneline", "-1")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(log, msg) {
		t.Errorf("git log %q does not contain message %q", log, msg)
	}
}
