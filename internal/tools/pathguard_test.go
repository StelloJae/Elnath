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
