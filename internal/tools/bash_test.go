package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func makeBashParams(t *testing.T, command string, extraFields map[string]any) json.RawMessage {
	t.Helper()
	m := map[string]any{"command": command}
	for k, v := range extraFields {
		m[k] = v
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal bash params: %v", err)
	}
	return raw
}

func TestBashExecute(t *testing.T) {
	tool := NewBashTool(t.TempDir())

	res, err := tool.Execute(context.Background(), makeBashParams(t, "echo hello", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if !strings.Contains(res.Output, "hello") {
		t.Errorf("output %q does not contain %q", res.Output, "hello")
	}
}

func TestBashTimeout(t *testing.T) {
	tool := NewBashTool(t.TempDir())

	// 1000 ms timeout, but sleep 10 s — must time out.
	res, err := tool.Execute(context.Background(), makeBashParams(t, "sleep 10", map[string]any{
		"timeout_ms": 1000,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected timeout error result, got success: %s", res.Output)
	}
	if !strings.Contains(res.Output, "timed out") {
		t.Errorf("output %q does not mention timeout", res.Output)
	}
}

func TestBashWorkingDir(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(t.TempDir()) // tool's own default workDir is irrelevant here

	// Resolve the real path because t.TempDir() may return a symlink on macOS.
	realDir, err := os.Lstat(dir)
	_ = realDir
	if err != nil {
		t.Fatalf("stat temp dir: %v", err)
	}

	res, err := tool.Execute(context.Background(), makeBashParams(t, "pwd", map[string]any{
		"working_dir": dir,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}

	// pwd may resolve symlinks differently on macOS (/private/var vs /var);
	// compare the base name to keep the test portable.
	gotTrimmed := strings.TrimSpace(res.Output)
	if !strings.HasSuffix(gotTrimmed, strings.TrimRight(dir, "/")) &&
		!strings.Contains(gotTrimmed, strings.TrimLeft(dir, "/")) {
		// Fallback: just check the last path component matches.
		wantBase := dir[strings.LastIndex(dir, "/")+1:]
		if !strings.HasSuffix(gotTrimmed, wantBase) {
			t.Errorf("pwd output %q does not match working_dir %q", gotTrimmed, dir)
		}
	}
}
