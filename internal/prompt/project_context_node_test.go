package prompt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	testing "testing"
)

func TestProjectContextNodeSkipsWhenNotExistingCode(t *testing.T) {
	t.Parallel()

	got, err := NewProjectContextNode(50).Render(context.Background(), &RenderState{WorkDir: t.TempDir(), UserInput: "fix request id middleware"})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestProjectContextNodeRendersGitInfoAndHints(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "checkout", "-b", "feature/prompt-graph")
	runGit(t, root, "remote", "add", "origin", "git@github.com:stello/elnath.git")

	for _, path := range []string{
		"internal/middleware/request_id.go",
		"internal/logging/logger.go",
		"pkg/transport/context.go",
		"test/integration/request_id.test.ts",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		body := "package test\n"
		if strings.HasSuffix(path, "context.go") {
			body = "package test\n// request id middleware logger structured logging\n"
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	got, err := NewProjectContextNode(50).Render(context.Background(), &RenderState{
		ExistingCode: true,
		WorkDir:      root,
		UserInput:    "add request id middleware and thread logging",
	})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	for _, want := range []string{"Project context:", "Git branch: feature/prompt-graph", "Git remote: git@github.com:stello/elnath.git", "internal/middleware/request_id.go", "pkg/transport/context.go"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render = %q, want substring %q", got, want)
		}
	}
}

func TestProjectContextNodeRedactsRemoteCredentials(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		remote     string
		wantSubstr string
		forbidden  []string
	}{
		{
			name:       "https with token",
			remote:     "https://token:secret@github.com/stello/elnath.git",
			wantSubstr: "https://github.com/stello/elnath.git",
			forbidden:  []string{"token", "secret@"},
		},
		{
			name:       "ssh with token",
			remote:     "ssh://git:deploytoken@github.com/stello/elnath.git",
			wantSubstr: "ssh://github.com/stello/elnath.git",
			forbidden:  []string{"deploytoken"},
		},
		{
			name:       "scp-style passthrough",
			remote:     "git@github.com:stello/elnath.git",
			wantSubstr: "git@github.com:stello/elnath.git",
			forbidden:  nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			runGit(t, root, "init")
			runGit(t, root, "checkout", "-b", "feature/prompt-graph")
			runGit(t, root, "remote", "add", "origin", tc.remote)

			got, err := NewProjectContextNode(50).Render(context.Background(), &RenderState{
				ExistingCode: true,
				WorkDir:      root,
				UserInput:    "fix handler",
			})
			if err != nil {
				t.Fatalf("Render error: %v", err)
			}
			if !strings.Contains(got, "Git remote: "+tc.wantSubstr) {
				t.Fatalf("Render = %q, want substring %q", got, tc.wantSubstr)
			}
			for _, secret := range tc.forbidden {
				if strings.Contains(got, secret) {
					t.Fatalf("Render = %q, should redact %q", got, secret)
				}
			}
		})
	}
}

func TestLikelyRepoFilesPrefersRuntimeOverTests(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	files := map[string]string{
		"packages/vitest/src/runtime/worker.ts":          "retry telemetry worker runtime",
		"packages/vitest/src/node/types/worker.ts":       "retry telemetry worker type",
		"test/cli/test/retry-telemetry.test.ts":          "retry telemetry worker test",
		"examples/opentelemetry/src/basic.test.ts":       "retry telemetry example",
		"test/browser/fixtures/user-event/retry.test.ts": "retry telemetry fixture",
	}
	for path, body := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	hints := likelyRepoFiles(root, "extend an existing TypeScript worker flow to emit retry telemetry without regressing current behavior", 5)
	if len(hints) == 0 {
		t.Fatal("expected non-empty hints")
	}
	joined := strings.Join(hints, "\n")
	if !strings.Contains(joined, "packages/vitest/src/runtime/worker.ts") {
		t.Fatalf("expected runtime worker hint, got %v", hints)
	}
	if strings.Index(joined, "test/cli/test/retry-telemetry.test.ts") != -1 && strings.Index(joined, "packages/vitest/src/runtime/worker.ts") > strings.Index(joined, "test/cli/test/retry-telemetry.test.ts") {
		t.Fatalf("expected runtime worker file to rank ahead of test file, got %v", hints)
	}
}

func TestKeywordHintsSkipsGenericBrownfieldWords(t *testing.T) {
	t.Parallel()

	hints := keywordHints("extend an existing TypeScript worker flow to emit retry telemetry without regressing current behavior")
	joined := strings.Join(hints, ",")
	for _, banned := range []string{"extend", "existing", "flow", "emit", "current", "behavior", "regressing"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("expected %q to be filtered from keyword hints, got %v", banned, hints)
		}
	}
	for _, want := range []string{"retry", "telemetry", "worker"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in keyword hints, got %v", want, hints)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
