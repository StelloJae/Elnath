package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

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
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

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

func TestBashOutputTruncatesStdout(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(context.Background(), makeBashParams(t, "head -c 70000 /dev/zero | tr '\\000' 'a'", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if len(res.Output) > toolMaxOutputBytes {
		t.Fatalf("output len = %d, want <= %d", len(res.Output), toolMaxOutputBytes)
	}
	if !strings.Contains(res.Output, "output truncated") {
		t.Fatalf("expected truncation marker, got %q", res.Output[len(res.Output)-80:])
	}
}

func TestBashOutputTruncatesCombinedStreams(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(context.Background(), makeBashParams(t, "head -c 70000 /dev/zero | tr '\\000' 'b' 1>&2", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if len(res.Output) > toolMaxOutputBytes {
		t.Fatalf("output len = %d, want <= %d", len(res.Output), toolMaxOutputBytes)
	}
	if !strings.Contains(res.Output, "output truncated") {
		t.Fatalf("expected truncation marker, got %q", res.Output[len(res.Output)-80:])
	}
}

// TestBashWorkingDir_AllowsSessionSubdir confirms that a relative working_dir
// pointing inside the per-session workspace is accepted after P0-1 tightening.
func TestBashWorkingDir_AllowsSessionSubdir(t *testing.T) {
	root := t.TempDir()
	guard := NewPathGuard(root, nil)
	tool := NewBashTool(guard)

	ctx := WithSessionID(context.Background(), "wdir-inside")
	sessionDir, err := guard.EnsureSessionWorkDir("wdir-inside")
	if err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	sub := filepath.Join(sessionDir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	res, err := tool.Execute(ctx, makeBashParams(t, "pwd", map[string]any{
		"working_dir": "sub",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	// pwd resolves symlinks (macOS /tmp → /private/tmp); assert suffix.
	gotTrimmed := strings.TrimSpace(res.Output)
	if !strings.HasSuffix(gotTrimmed, string(filepath.Separator)+"sub") {
		t.Errorf("pwd output %q should end with /sub", gotTrimmed)
	}
}

// TestBashWorkingDir_RejectsExternalAbsolute replaces the permissive pre-P0-1
// TestBashWorkingDir: a working_dir that resolves outside the session root
// must be rejected as a boundary escape.
func TestBashWorkingDir_RejectsExternalAbsolute(t *testing.T) {
	outside := t.TempDir()
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(context.Background(), makeBashParams(t, "pwd", map[string]any{
		"working_dir": outside,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for external working_dir, got output: %s", res.Output)
	}
	if !strings.Contains(res.Output, "invalid working_dir") {
		t.Errorf("output %q should mention 'invalid working_dir'", res.Output)
	}
}

func TestBashWorkingDir_RejectsRootPath(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(context.Background(), makeBashParams(t, "pwd", map[string]any{
		"working_dir": "/",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for working_dir=/, got output: %s", res.Output)
	}
}

func TestBashWorkingDir_RejectsParentEscape(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(context.Background(), makeBashParams(t, "pwd", map[string]any{
		"working_dir": "../../../../",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for ../../../../ escape, got output: %s", res.Output)
	}
}

// TestBashWorkingDir_RejectsPrefixTrick guards against the classic
// HasPrefix boundary bug: a sibling directory whose name shares a prefix
// with the session root must not pass.
func TestBashWorkingDir_RejectsPrefixTrick(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	sibling := filepath.Join(parent, "root2")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatalf("mkdir sibling: %v", err)
	}
	tool := NewBashTool(NewPathGuard(root, nil))

	res, err := tool.Execute(context.Background(), makeBashParams(t, "pwd", map[string]any{
		"working_dir": sibling,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for prefix-trick sibling, got output: %s", res.Output)
	}
}

// TestBashWorkingDir_RejectsSymlinkEscape verifies that a symlink inside the
// session workspace that resolves to an outside directory is rejected.
func TestBashWorkingDir_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	guard := NewPathGuard(root, nil)
	tool := NewBashTool(guard)

	ctx := WithSessionID(context.Background(), "wdir-symlink")
	sessionDir, err := guard.EnsureSessionWorkDir("wdir-symlink")
	if err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(sessionDir, "escape")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	res, err := tool.Execute(ctx, makeBashParams(t, "pwd", map[string]any{
		"working_dir": "escape",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for symlink escape, got output: %s", res.Output)
	}
	if !strings.Contains(res.Output, "invalid working_dir") {
		t.Errorf("output %q should mention 'invalid working_dir'", res.Output)
	}
}

func TestBashWorkingDir_RejectsNonexistent(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(context.Background(), makeBashParams(t, "pwd", map[string]any{
		"working_dir": "does-not-exist",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for nonexistent working_dir, got output: %s", res.Output)
	}
}

func TestBashWorkingDir_RejectsFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	tool := NewBashTool(NewPathGuard(root, nil))

	res, err := tool.Execute(context.Background(), makeBashParams(t, "pwd", map[string]any{
		"working_dir": "file.txt",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for file working_dir, got output: %s", res.Output)
	}
	if !strings.Contains(res.Output, "not a directory") {
		t.Errorf("output %q should mention 'not a directory'", res.Output)
	}
}

func TestBashEmptyCommand(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(context.Background(), makeBashParams(t, "   ", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for empty command, got success: %s", res.Output)
	}
}

func TestBashInvalidParams(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(context.Background(), json.RawMessage(`not-valid-json`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for invalid JSON params, got success: %s", res.Output)
	}
}

func TestBashAccessors(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))

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
		{command: "rm -rf \"$HOME\"", dangerous: true},
		{command: "rm -fr /", dangerous: true},
		{command: "timeout 5 rm -rf /", dangerous: true},
		{command: "rm file.txt", dangerous: false},
		{command: "cp ./file /etc/passwd", dangerous: true},
		{command: "timeout 5 cp ./file /etc/passwd", dangerous: true},
		{command: "cp /etc/hosts ./hosts.copy", dangerous: false},
		{command: "mv ./tool /usr/local/bin/tool", dangerous: true},
		{command: "touch /etc/passwd", dangerous: true},
		{command: "touch -r /etc/hosts ./local-copy", dangerous: false},
		{command: "mkdir -p /usr/local/share/test", dangerous: true},
		{command: "chmod 777 /etc/passwd", dangerous: true},
		{command: "chmod 777 \"/etc/passwd\"", dangerous: true},
		{command: "chown root /usr/bin/test", dangerous: true},
		{command: "chmod 644 myfile.txt", dangerous: false},
		{command: "echo hi > /etc/passwd", dangerous: true},
		{command: "cat < /etc/passwd", dangerous: false},
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
