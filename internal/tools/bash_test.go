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

func TestBashEmptyCommand(t *testing.T) {
	tool := NewBashTool(t.TempDir())

	res, err := tool.Execute(context.Background(), makeBashParams(t, "   ", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for empty command, got success: %s", res.Output)
	}
}

func TestBashInvalidParams(t *testing.T) {
	tool := NewBashTool(t.TempDir())

	res, err := tool.Execute(context.Background(), json.RawMessage(`not-valid-json`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for invalid JSON params, got success: %s", res.Output)
	}
}

func TestBashAccessors(t *testing.T) {
	tool := NewBashTool(t.TempDir())

	if tool.Name() != "bash" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "bash")
	}
	if tool.Description() == "" {
		t.Error("Description() returned empty string")
	}
	schema := tool.Schema()
	if len(schema) == 0 {
		t.Error("Schema() returned empty")
	}
}

func TestAnalyzeCommand(t *testing.T) {
	cases := []struct {
		command   string
		dangerous bool
		reason    string
	}{
		{command: "echo hello", dangerous: false},
		{command: "sudo rm -rf /", dangerous: true},
		{command: "dd if=/dev/zero of=/dev/sda", dangerous: true},
		{command: "rm -rf /", dangerous: true},
		{command: "rm -rf ~", dangerous: true},
		{command: "rm -fr /", dangerous: true},
		{command: "rm file.txt", dangerous: false},
		{command: "chmod 777 /etc/passwd", dangerous: true},
		{command: "chown root /usr/bin/test", dangerous: true},
		{command: "chmod 644 myfile.txt", dangerous: false},
		{command: "git push --force origin main", dangerous: true},
		{command: "git push --force origin feature", dangerous: false},
		{command: "git push origin main", dangerous: false},
		{command: "git push -f origin master", dangerous: true},
		{command: "(((", dangerous: false}, // unparseable — bash will report the error
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.command, func(t *testing.T) {
			dangerous, reason := analyzeCommand(tc.command)
			if dangerous != tc.dangerous {
				t.Errorf("analyzeCommand(%q) dangerous=%v, want %v (reason=%q)",
					tc.command, dangerous, tc.dangerous, reason)
			}
			if tc.dangerous && reason == "" {
				t.Errorf("analyzeCommand(%q) returned dangerous=true but empty reason", tc.command)
			}
		})
	}
}
