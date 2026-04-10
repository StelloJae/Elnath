package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func setupGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "test"},
		{"config", "user.email", "test@test.com"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	for _, args := range [][]string{
		{"add", "README.md"},
		{"commit", "-m", "initial commit"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}

	return dir
}

func TestGitToolMeta(t *testing.T) {
	tool := NewGitTool(NewPathGuard(t.TempDir(), nil))

	if got := tool.Name(); got != "git" {
		t.Errorf("Name() = %q, want %q", got, "git")
	}
	if got := tool.Description(); got == "" {
		t.Error("Description() returned empty string")
	}
	schema := tool.Schema()
	if len(schema) == 0 {
		t.Error("Schema() returned empty JSON")
	}
}

func TestGitToolExecute(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T) string
		params     map[string]any
		wantError  bool
		wantOutput string
	}{
		{
			name:  "status clean repo",
			setup: setupGitRepo,
			params: map[string]any{
				"subcommand": "status",
			},
			wantError: false,
		},
		{
			name:  "log shows initial commit",
			setup: setupGitRepo,
			params: map[string]any{
				"subcommand": "log",
			},
			wantError:  false,
			wantOutput: "initial commit",
		},
		{
			name:  "commit with empty message returns error",
			setup: setupGitRepo,
			params: map[string]any{
				"subcommand": "commit",
				"message":    "",
			},
			wantError: true,
		},
		{
			name: "commit with valid message",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := setupGitRepo(t)
				if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("content"), 0o644); err != nil {
					t.Fatalf("write new.txt: %v", err)
				}
				cmd := exec.Command("git", "add", "new.txt")
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git add: %s: %v", out, err)
				}
				return dir
			},
			params: map[string]any{
				"subcommand": "commit",
				"message":    "add new file",
			},
			wantError:  false,
			wantOutput: "new file",
		},
		{
			name:  "branch lists branches",
			setup: setupGitRepo,
			params: map[string]any{
				"subcommand": "branch",
			},
			wantError: false,
		},
		{
			name: "diff shows modifications",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := setupGitRepo(t)
				if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# modified"), 0o644); err != nil {
					t.Fatalf("modify README.md: %v", err)
				}
				return dir
			},
			params: map[string]any{
				"subcommand": "diff",
			},
			wantError:  false,
			wantOutput: "README.md",
		},
		{
			name:  "unsupported subcommand returns error",
			setup: setupGitRepo,
			params: map[string]any{
				"subcommand": "rebase",
			},
			wantError: true,
		},
		{
			name:      "invalid JSON params returns error",
			setup:     func(t *testing.T) string { return t.TempDir() },
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.setup(t)
			tool := NewGitTool(NewPathGuard(dir, nil))

			var params []byte
			if tc.params != nil {
				params = mustMarshal(t, tc.params)
			} else {
				params = []byte("not valid json{{{")
			}

			res, err := tool.Execute(context.Background(), params)
			if err != nil {
				t.Fatalf("Execute returned unexpected Go error: %v", err)
			}
			if tc.wantError && !res.IsError {
				t.Errorf("expected error result, got output: %s", res.Output)
			}
			if !tc.wantError && res.IsError {
				t.Errorf("unexpected error result: %s", res.Output)
			}
			if tc.wantOutput != "" && !strings.Contains(res.Output, tc.wantOutput) {
				t.Errorf("output does not contain %q:\n%s", tc.wantOutput, res.Output)
			}
		})
	}
}
