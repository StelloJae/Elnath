package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithSessionID_RoundTrip(t *testing.T) {
	ctx := WithSessionID(context.Background(), "sess-xyz")
	if got := SessionIDFrom(ctx); got != "sess-xyz" {
		t.Fatalf("got %q, want %q", got, "sess-xyz")
	}
}

func TestSessionIDFrom_DefaultEmpty(t *testing.T) {
	if got := SessionIDFrom(context.Background()); got != "" {
		t.Fatalf("expected empty session id from bare context, got %q", got)
	}
}

func TestSessionIDFrom_NilContextSafe(t *testing.T) {
	//nolint:staticcheck // intentional nil-context probe
	if got := SessionIDFrom(nil); got != "" {
		t.Fatalf("expected empty session id from nil context, got %q", got)
	}
}

// TestBashTool_UsesSessionWorkspace verifies that BashTool.Execute respects
// the session id on ctx and runs commands inside the per-session subdir.
func TestBashTool_UsesSessionWorkspace(t *testing.T) {
	root := t.TempDir()
	guard := NewPathGuard(root, nil)
	bt := NewBashTool(guard)

	ctx := WithSessionID(context.Background(), "sess-A")
	res, err := bt.Execute(ctx, json.RawMessage(`{"command":"pwd"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	wantDir := filepath.Join(root, "sessions", "sess-A")
	if !strings.Contains(res.Output, wantDir) {
		t.Fatalf("expected pwd output to contain %q, got %q", wantDir, res.Output)
	}
}

func TestBashTool_NoSessionFallsBackToRootWorkDir(t *testing.T) {
	root := t.TempDir()
	guard := NewPathGuard(root, nil)
	bt := NewBashTool(guard)

	res, err := bt.Execute(context.Background(), json.RawMessage(`{"command":"pwd"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, root) {
		t.Fatalf("expected pwd output to contain root %q, got %q", root, res.Output)
	}
	if strings.Contains(res.Output, filepath.Join(root, "sessions")) {
		t.Fatalf("no session id given but cwd is under sessions/: %q", res.Output)
	}
}

// TestBashTool_SessionsAreIsolated confirms cross-session contamination is
// blocked: a file written in session A is invisible from session B.
func TestBashTool_SessionsAreIsolated(t *testing.T) {
	root := t.TempDir()
	guard := NewPathGuard(root, nil)
	bt := NewBashTool(guard)

	ctxA := WithSessionID(context.Background(), "alpha")
	ctxB := WithSessionID(context.Background(), "beta")

	if _, err := bt.Execute(ctxA, json.RawMessage(`{"command":"echo hi > marker.txt"}`)); err != nil {
		t.Fatalf("alpha write: %v", err)
	}
	res, err := bt.Execute(ctxB, json.RawMessage(`{"command":"ls"}`))
	if err != nil {
		t.Fatalf("beta ls: %v", err)
	}
	if strings.Contains(res.Output, "marker.txt") {
		t.Fatalf("beta should not see alpha's marker.txt; got %q", res.Output)
	}
}

// TestGlobTool_UsesSessionWorkspace verifies the glob tool searches inside
// the session subdir when ctx carries a session id.
func TestGlobTool_UsesSessionWorkspace(t *testing.T) {
	root := t.TempDir()
	guard := NewPathGuard(root, nil)

	bt := NewBashTool(guard)
	ctxA := WithSessionID(context.Background(), "globsess")
	if _, err := bt.Execute(ctxA, json.RawMessage(`{"command":"echo body > note.md"}`)); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	gt := NewGlobTool(guard)
	res, err := gt.Execute(ctxA, json.RawMessage(`{"pattern":"*.md"}`))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if !strings.Contains(res.Output, "note.md") {
		t.Fatalf("glob should find note.md inside session workspace, got %q", res.Output)
	}

	ctxB := WithSessionID(context.Background(), "otherglob")
	resB, err := gt.Execute(ctxB, json.RawMessage(`{"pattern":"*.md"}`))
	if err != nil {
		t.Fatalf("glob session B: %v", err)
	}
	if strings.Contains(resB.Output, "note.md") {
		t.Fatalf("session B must not see session A's note.md, got %q", resB.Output)
	}
}

// TestGitTool_UsesSessionWorkspace verifies git runs inside the session
// subdir. We initialize a repo from bash inside the session workspace, then
// `git status` from the same session must succeed.
func TestGitTool_UsesSessionWorkspace(t *testing.T) {
	root := t.TempDir()
	guard := NewPathGuard(root, nil)

	bt := NewBashTool(guard)
	ctxA := WithSessionID(context.Background(), "gitsess")
	if _, err := bt.Execute(ctxA, json.RawMessage(`{"command":"git init -q"}`)); err != nil {
		t.Fatalf("git init: %v", err)
	}

	gt := NewGitTool(guard)
	res, err := gt.Execute(ctxA, json.RawMessage(`{"subcommand":"status"}`))
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.Contains(res.Output, "not a git repository") {
		t.Fatalf("git status from same session should see the repo, got %q", res.Output)
	}

	// A different session must not see the gitsess repo.
	ctxB := WithSessionID(context.Background(), "othergit")
	resB, err := gt.Execute(ctxB, json.RawMessage(`{"subcommand":"status"}`))
	if err != nil {
		t.Fatalf("git status session B: %v", err)
	}
	if !strings.Contains(resB.Output, "not a git repository") {
		t.Fatalf("session B must not see session A's git repo, got %q", resB.Output)
	}
}
