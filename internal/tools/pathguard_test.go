package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathGuard_Resolve_AbsolutePath(t *testing.T) {
	g := NewPathGuard("/work", nil)
	got, err := g.Resolve("/tmp/foo.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/tmp/foo.txt" {
		t.Errorf("got %q, want /tmp/foo.txt", got)
	}
}

func TestPathGuard_Resolve_RelativePath(t *testing.T) {
	g := NewPathGuard("/work", nil)
	got, err := g.Resolve("sub/file.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/work/sub/file.go" {
		t.Errorf("got %q, want /work/sub/file.go", got)
	}
}

func TestPathGuard_Resolve_TildeExpansion(t *testing.T) {
	home, _ := os.UserHomeDir()
	g := NewPathGuard("/work", nil)

	got, err := g.Resolve("~/docs/notes.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(home, "docs/notes.md")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPathGuard_Resolve_TildeAlone(t *testing.T) {
	home, _ := os.UserHomeDir()
	g := NewPathGuard("/work", nil)

	got, err := g.Resolve("~")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != home {
		t.Errorf("got %q, want %q", got, home)
	}
}

func TestPathGuard_Resolve_EmptyPath(t *testing.T) {
	g := NewPathGuard("/work", nil)
	_, err := g.Resolve("")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestPathGuard_Resolve_DotDot(t *testing.T) {
	g := NewPathGuard("/work/sub", nil)
	got, err := g.Resolve("../other/file.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/work/other/file.go" {
		t.Errorf("got %q, want /work/other/file.go", got)
	}
}

func TestPathGuard_ResolveIn(t *testing.T) {
	g := NewPathGuard("/default", nil)
	got, err := g.ResolveIn("/custom", "file.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/custom/file.go" {
		t.Errorf("got %q, want /custom/file.go", got)
	}
}

func TestPathGuard_CheckWrite_Allowed(t *testing.T) {
	g := NewPathGuard("/work", []string{"/protected/src"})
	if err := g.CheckWrite("/other/dir/file.go"); err != nil {
		t.Errorf("unexpected deny: %v", err)
	}
}

func TestPathGuard_CheckWrite_Denied_ExactMatch(t *testing.T) {
	g := NewPathGuard("/work", []string{"/protected/src"})
	err := g.CheckWrite("/protected/src")
	if err == nil {
		t.Fatal("expected write denied for exact protected path")
	}
	if !strings.Contains(err.Error(), "write denied") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPathGuard_CheckWrite_Denied_ChildPath(t *testing.T) {
	g := NewPathGuard("/work", []string{"/protected/src"})
	err := g.CheckWrite("/protected/src/main.go")
	if err == nil {
		t.Fatal("expected write denied for child of protected path")
	}
}

func TestPathGuard_CheckWrite_SiblingAllowed(t *testing.T) {
	g := NewPathGuard("/work", []string{"/protected/src"})
	if err := g.CheckWrite("/protected/src-other/file.go"); err != nil {
		t.Errorf("sibling path should not be denied: %v", err)
	}
}

func TestPathGuard_CheckWrite_MultipleProtected(t *testing.T) {
	g := NewPathGuard("/work", []string{"/a", "/b/c"})
	if err := g.CheckWrite("/a/file"); err == nil {
		t.Error("expected deny for /a/file")
	}
	if err := g.CheckWrite("/b/c/deep/file"); err == nil {
		t.Error("expected deny for /b/c/deep/file")
	}
	if err := g.CheckWrite("/b/other"); err != nil {
		t.Errorf("/b/other should be allowed: %v", err)
	}
}

func TestPathGuard_ProtectedPaths_TildeExpansion(t *testing.T) {
	home, _ := os.UserHomeDir()
	g := NewPathGuard("/work", []string{"~/myproject"})

	target := filepath.Join(home, "myproject", "main.go")
	err := g.CheckWrite(target)
	if err == nil {
		t.Fatalf("expected deny for %q under ~/myproject", target)
	}
}

func TestPathGuard_ProtectedPaths_RelativeExpansion(t *testing.T) {
	g := NewPathGuard("/work", []string{"src"})
	err := g.CheckWrite("/work/src/file.go")
	if err == nil {
		t.Fatal("expected deny for relative protected path expanded to /work/src")
	}
}

func TestPathGuard_WorkDir(t *testing.T) {
	g := NewPathGuard("/my/dir", nil)
	if g.WorkDir() != "/my/dir" {
		t.Errorf("got %q, want /my/dir", g.WorkDir())
	}
}

func TestPathGuard_SessionWorkDir_PerSession(t *testing.T) {
	g := NewPathGuard("/work", nil)
	a := g.SessionWorkDir("session-A")
	b := g.SessionWorkDir("session-B")
	if a == b {
		t.Fatalf("expected distinct paths per session, got both %q", a)
	}
	if !strings.HasPrefix(a, "/work/") {
		t.Errorf("session A path %q should be under /work", a)
	}
	if !strings.HasPrefix(b, "/work/") {
		t.Errorf("session B path %q should be under /work", b)
	}
	if a != g.SessionWorkDir("session-A") {
		t.Errorf("same session id must yield deterministic path")
	}
}

func TestPathGuard_SessionWorkDir_EmptyFallsBackToRoot(t *testing.T) {
	g := NewPathGuard("/work", nil)
	if got := g.SessionWorkDir(""); got != "/work" {
		t.Errorf("empty session id should fall back to root WorkDir, got %q", got)
	}
}

func TestPathGuard_SessionWorkDir_RejectsPathSeparators(t *testing.T) {
	g := NewPathGuard("/work", nil)
	got := g.SessionWorkDir("../escape")
	if strings.Contains(got, "..") {
		t.Errorf("session id traversal not contained: %q", got)
	}
	if !strings.HasPrefix(got, "/work/") {
		t.Errorf("escape attempt should still resolve under /work, got %q", got)
	}
}

func TestPathGuard_EnsureSessionWorkDir_CreatesDir(t *testing.T) {
	root := t.TempDir()
	g := NewPathGuard(root, nil)
	dir, err := g.EnsureSessionWorkDir("abc-123")
	if err != nil {
		t.Fatalf("EnsureSessionWorkDir: %v", err)
	}
	if !strings.HasPrefix(dir, root) {
		t.Errorf("dir %q must be under root %q", dir, root)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat session dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("session path %q is not a directory", dir)
	}
}

func TestPathGuard_EnsureSessionWorkDir_EmptyReturnsRoot(t *testing.T) {
	root := t.TempDir()
	g := NewPathGuard(root, nil)
	dir, err := g.EnsureSessionWorkDir("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != root {
		t.Errorf("empty session id should return root %q, got %q", root, dir)
	}
}

func TestPathGuard_EnsureSessionWorkDir_IsolatesSessions(t *testing.T) {
	root := t.TempDir()
	g := NewPathGuard(root, nil)
	dirA, err := g.EnsureSessionWorkDir("alpha")
	if err != nil {
		t.Fatalf("alpha: %v", err)
	}
	dirB, err := g.EnsureSessionWorkDir("beta")
	if err != nil {
		t.Fatalf("beta: %v", err)
	}
	if dirA == dirB {
		t.Fatalf("expected isolation between sessions, both got %q", dirA)
	}
	if err := os.WriteFile(filepath.Join(dirA, "marker.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirB, "marker.txt")); !os.IsNotExist(err) {
		t.Fatalf("marker should not be visible from session beta, err=%v", err)
	}
}

func TestExpandHome(t *testing.T) {
	home := "/Users/test"
	tests := []struct {
		input string
		want  string
	}{
		{"~", home},
		{"~/foo", filepath.Join(home, "foo")},
		{"~other", "~other"},
		{"/abs/path", "/abs/path"},
		{"rel/path", "rel/path"},
	}
	for _, tt := range tests {
		got := expandHome(home, tt.input)
		if got != tt.want {
			t.Errorf("expandHome(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPathGuardCheckScope(t *testing.T) {
	workDir := t.TempDir()
	protected := filepath.Join(workDir, "protected")
	guard := NewPathGuard(workDir, []string{protected})
	firstDenied := filepath.Join(protected, "one", "file.go")

	tests := []struct {
		name      string
		scope     ToolScope
		wantError bool
		wantText  string
	}{
		{
			name:      "write path under protected directory is denied",
			scope:     ToolScope{WritePaths: []string{filepath.Join(protected, "file.go")}},
			wantError: true,
			wantText:  "write denied",
		},
		{
			name:      "allowed write path passes",
			scope:     ToolScope{WritePaths: []string{filepath.Join(workDir, "allowed", "file.go")}},
			wantError: false,
		},
		{
			name:      "read paths are ignored",
			scope:     ToolScope{ReadPaths: []string{filepath.Join(protected, "read-only.txt")}},
			wantError: false,
		},
		{
			name: "first denied write path is returned",
			scope: ToolScope{WritePaths: []string{
				filepath.Join(workDir, "allowed", "file.go"),
				firstDenied,
				filepath.Join(protected, "two", "file.go"),
			}},
			wantError: true,
			wantText:  firstDenied,
		},
		{
			name:      "empty scope passes",
			scope:     ToolScope{},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := guard.CheckScope(tt.scope)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.wantText) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantText)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckScope() unexpected error: %v", err)
			}
		})
	}
}
