package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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
	if !strings.Contains(res.Output, "status: timeout") {
		t.Errorf("output %q does not carry status: timeout", res.Output)
	}
	if !strings.Contains(res.Output, "timed_out: true") {
		t.Errorf("output %q does not carry timed_out: true", res.Output)
	}
}

// TestBash_OutputSmallWithoutTruncation verifies that commands whose
// output fits under the per-stream cap are returned verbatim with no
// truncation marker. P0-3 bounded output.
func TestBash_OutputSmallWithoutTruncation(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	res, err := tool.Execute(context.Background(), makeBashParams(t, "echo hello", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if strings.Contains(res.Output, "output truncated") {
		t.Errorf("small output should not carry truncation marker: %q", res.Output)
	}
	if !strings.Contains(res.Output, "STDOUT:\nhello") {
		t.Errorf("STDOUT section missing hello: %q", res.Output)
	}
	if !strings.Contains(res.Output, "stdout_bytes_raw: 6") {
		t.Errorf("metadata stdout_bytes_raw missing/wrong: %q", res.Output)
	}
	if !strings.Contains(res.Output, "stdout_truncated: false") {
		t.Errorf("metadata stdout_truncated missing/wrong: %q", res.Output)
	}
}

// TestBash_OutputStdoutExceedsCap ensures stdout larger than the
// per-stream cap is truncated and flagged.
func TestBash_OutputStdoutExceedsCap(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	// 400_000 bytes of 'a' exceeds the 256 KiB per-stream cap.
	cmd := "head -c 400000 /dev/zero | tr '\\000' 'a'"
	res, err := tool.Execute(context.Background(), makeBashParams(t, cmd, nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "stdout_bytes_raw: 400000") {
		t.Errorf("stdout_bytes_raw missing/wrong: %q", res.Output[:200])
	}
	if !strings.Contains(res.Output, "stdout_truncated: true") {
		t.Errorf("stdout_truncated flag missing/wrong: %q", res.Output[:200])
	}
	if !strings.Contains(res.Output, "output truncated") {
		t.Errorf("stream-level truncation marker missing")
	}
	// Output size is bounded near 2 * cap + small header budget.
	if len(res.Output) > 2*bashOutputCapPerStream+4096 {
		t.Errorf("output len = %d, want near per-stream cap", len(res.Output))
	}
}

// TestBash_OutputStderrExceedsCap ensures stderr is capped
// independently and does not affect stdout reporting.
func TestBash_OutputStderrExceedsCap(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	cmd := "head -c 400000 /dev/zero | tr '\\000' 'b' 1>&2"
	res, err := tool.Execute(context.Background(), makeBashParams(t, cmd, nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "stdout_bytes_raw: 0") {
		t.Errorf("stdout should be empty; output=%q", res.Output[:200])
	}
	if !strings.Contains(res.Output, "stderr_bytes_raw: 400000") {
		t.Errorf("stderr_bytes_raw missing/wrong: %q", res.Output[:200])
	}
	if !strings.Contains(res.Output, "stderr_truncated: true") {
		t.Errorf("stderr_truncated flag missing/wrong: %q", res.Output[:200])
	}
}

// TestBash_OutputBothStreamsCappedIndependently emits large output on
// both streams and verifies each is truncated separately.
func TestBash_OutputBothStreamsCappedIndependently(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	cmd := "head -c 400000 /dev/zero | tr '\\000' 'a'; head -c 400000 /dev/zero | tr '\\000' 'b' 1>&2"
	res, err := tool.Execute(context.Background(), makeBashParams(t, cmd, nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "stdout_bytes_raw: 400000") ||
		!strings.Contains(res.Output, "stdout_truncated: true") {
		t.Errorf("stdout not truncated: %q", res.Output[:200])
	}
	if !strings.Contains(res.Output, "stderr_bytes_raw: 400000") ||
		!strings.Contains(res.Output, "stderr_truncated: true") {
		t.Errorf("stderr not truncated: %q", res.Output[:200])
	}
}

// TestBash_OutputPreservesHeadAndTail emits recognizable markers at
// the start and end of a large output and checks both survive the
// cap; the middle must be dropped behind a truncation marker.
func TestBash_OutputPreservesHeadAndTail(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	// 400_000 bytes of 'x' between HEAD and TAIL.
	cmd := "printf HEADMARKER; head -c 400000 /dev/zero | tr '\\000' 'x'; printf TAILMARKER"
	res, err := tool.Execute(context.Background(), makeBashParams(t, cmd, nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "HEADMARKER") {
		t.Errorf("head preserved HEADMARKER missing")
	}
	if !strings.Contains(res.Output, "TAILMARKER") {
		t.Errorf("tail preserved TAILMARKER missing")
	}
	if !strings.Contains(res.Output, "output truncated") {
		t.Errorf("truncation marker missing")
	}
}

// TestBash_OutputCommandNotKilledOnCapExceed guarantees that a
// command producing more bytes than the cap still finishes with
// exit 0, matching real shell semantics. P0-3 scope is drop/
// truncate, not flow-control or kill.
func TestBash_OutputCommandNotKilledOnCapExceed(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	cmd := "head -c 400000 /dev/zero | tr '\\000' 'a'; echo AFTER-TRUNCATE; exit 0"
	res, err := tool.Execute(context.Background(), makeBashParams(t, cmd, nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("command should exit 0 even after cap exceed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "AFTER-TRUNCATE") {
		t.Errorf("post-truncation output lost; bash was killed prematurely: %q", res.Output[len(res.Output)-200:])
	}
}

// TestBash_OutputNonZeroExitAfterTruncation preserves Lane 2.2
// invariant: a non-zero exit after large output is still a
// recoverable tool_result(IsError=true), not a fatal workflow abort.
func TestBash_OutputNonZeroExitAfterTruncation(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	cmd := "head -c 400000 /dev/zero | tr '\\000' 'a'; exit 42"
	res, err := tool.Execute(context.Background(), makeBashParams(t, cmd, nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("exit 42 should surface as IsError=true")
	}
	if !strings.Contains(res.Output, "stdout_bytes_raw: 400000") ||
		!strings.Contains(res.Output, "stdout_truncated: true") {
		t.Errorf("stdout metadata missing on error path: %q", res.Output[:200])
	}
	if !strings.Contains(res.Output, "exit_code: 42") {
		t.Errorf("exit_code metadata missing on error path: %q", res.Output[:200])
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
	// pwd resolves symlinks (macOS /tmp → /private/tmp); the absolute
	// path appears inside the STDOUT section of the metadata-formatted
	// body, so we just confirm the captured cwd ends in /sub.
	if !strings.Contains(res.Output, string(filepath.Separator)+"sub\n") {
		t.Errorf("STDOUT did not capture a path ending with /sub: %q", res.Output)
	}
	if !strings.Contains(res.Output, "cwd: sub") {
		t.Errorf("metadata cwd should be session-relative 'sub'; got: %q", res.Output)
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

// TestBash_EnvDoesNotLeakSecret verifies that provider API keys on the
// host environment are not inherited by bash invocations. P0-2 clean
// env baseline.
func TestBash_EnvDoesNotLeakSecret(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-leak")
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	res, err := tool.Execute(context.Background(), makeBashParams(t, `printf %s "$OPENAI_API_KEY"`, nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if strings.Contains(res.Output, "sk-leak") {
		t.Errorf("OPENAI_API_KEY leaked into bash; output=%q", res.Output)
	}
}

// TestBash_EnvBlocksSshAuthSock: SSH_AUTH_SOCK exposure would let a
// shell command authenticate to other hosts as the real user.
func TestBash_EnvBlocksSshAuthSock(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/tmp/elnath-test-agent.sock")
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	res, err := tool.Execute(context.Background(), makeBashParams(t, `printf %s "$SSH_AUTH_SOCK"`, nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(res.Output, "elnath-test-agent.sock") {
		t.Errorf("SSH_AUTH_SOCK leaked: %q", res.Output)
	}
}

// TestBash_EnvBlocksBashEnvInjection verifies that a host-set BASH_ENV
// does not cause bash to source attacker-controlled files before the
// user command runs.
func TestBash_EnvBlocksBashEnvInjection(t *testing.T) {
	injectorDir := t.TempDir()
	injector := filepath.Join(injectorDir, "injector.sh")
	if err := os.WriteFile(injector, []byte("echo ELNATH_INJECTED_MARKER\n"), 0o644); err != nil {
		t.Fatalf("write injector: %v", err)
	}
	t.Setenv("BASH_ENV", injector)

	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	res, err := tool.Execute(context.Background(), makeBashParams(t, "echo ok", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(res.Output, "ELNATH_INJECTED_MARKER") {
		t.Errorf("BASH_ENV injection fired; output=%q", res.Output)
	}
	if !strings.Contains(res.Output, "ok") {
		t.Errorf("expected 'ok' in output, got %q", res.Output)
	}
}

// TestBash_EnvBlocksAwsSecret enforces the AWS_ prefix policy.
func TestBash_EnvBlocksAwsSecret(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-leak")
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	res, err := tool.Execute(context.Background(), makeBashParams(t, `printf %s "$AWS_SECRET_ACCESS_KEY"`, nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(res.Output, "aws-leak") {
		t.Errorf("AWS_SECRET_ACCESS_KEY leaked: %q", res.Output)
	}
}

// TestBash_EnvSetsHomeToSession pins HOME inside the per-session
// workspace so shell commands cannot read or write through the real
// user's home directory.
func TestBash_EnvSetsHomeToSession(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	t.Setenv("HOME", "/not/the/session")

	guard := NewPathGuard(root, nil)
	tool := NewBashTool(guard)
	ctx := WithSessionID(context.Background(), "home-sess")

	res, err := tool.Execute(ctx, makeBashParams(t, `printf %s "$HOME"`, nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	wantSuffix := filepath.Join("sessions", "home-sess")
	if !strings.Contains(res.Output, wantSuffix) {
		t.Errorf("HOME = %q, expected to contain %q", res.Output, wantSuffix)
	}
	if !strings.Contains(res.Output, realRoot) {
		t.Errorf("HOME = %q, expected to be under %q", res.Output, realRoot)
	}
	if strings.Contains(res.Output, "/not/the/session") {
		t.Errorf("HOME leaked host value: %q", res.Output)
	}
}

// TestBash_EnvTmpDirInsideSession ensures TMPDIR is rewritten to the
// session workspace's .tmp subdirectory.
func TestBash_EnvTmpDirInsideSession(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	guard := NewPathGuard(root, nil)
	tool := NewBashTool(guard)
	ctx := WithSessionID(context.Background(), "tmp-sess")

	res, err := tool.Execute(ctx, makeBashParams(t, `printf %s "$TMPDIR"`, nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	wantSuffix := filepath.Join("sessions", "tmp-sess", ".tmp")
	if !strings.Contains(res.Output, wantSuffix) {
		t.Errorf("TMPDIR = %q, expected to contain %q", res.Output, wantSuffix)
	}
	if !strings.Contains(res.Output, realRoot) {
		t.Errorf("TMPDIR = %q, expected to be under %q", res.Output, realRoot)
	}
}

// TestBash_EnvPinsShellAndTerm ensures bash sees a deterministic
// non-interactive TERM and a known SHELL regardless of the host.
func TestBash_EnvPinsShellAndTerm(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("SHELL", "/opt/homebrew/bin/fish")
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	res, err := tool.Execute(context.Background(), makeBashParams(t, `printf "%s|%s" "$TERM" "$SHELL"`, nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "dumb|/bin/bash") {
		t.Errorf("TERM|SHELL = %q, want contains 'dumb|/bin/bash'", res.Output)
	}
}

// TestBash_ProcessGroup_ChildKilledOnTimeout verifies that a
// background child spawned by the bash command does not become an
// orphan when the tool times out. The sleep child must be reaped as
// part of the session's process group. P0-4 process group cleanup.
func TestBash_ProcessGroup_ChildKilledOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group cleanup deferred on Windows (TODO: Job Objects)")
	}
	root := t.TempDir()
	guard := NewPathGuard(root, nil)
	tool := NewBashTool(guard)

	ctx := WithSessionID(context.Background(), "pgrp-child")
	sessionDir, err := guard.EnsureSessionWorkDir("pgrp-child")
	if err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	realSession, err := filepath.EvalSymlinks(sessionDir)
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	pidFile := filepath.Join(realSession, "child.pid")

	script := `sleep 60 & echo $! > child.pid; wait`
	res, err := tool.Execute(ctx, makeBashParams(t, script, map[string]any{"timeout_ms": 300}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected timeout error; got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "status: timeout") {
		t.Errorf("expected 'status: timeout' in output; got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "timed_out: true") {
		t.Errorf("expected 'timed_out: true' in output; got: %s", res.Output)
	}

	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("parse pid: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // ESRCH — child reaped
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("child pid %d still alive after timeout; process group cleanup failed", pid)
}

// TestBash_ProcessGroup_NormalCommandUnaffected ensures that adding
// process group handling does not change the happy-path exit
// semantics.
func TestBash_ProcessGroup_NormalCommandUnaffected(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	res, err := tool.Execute(context.Background(), makeBashParams(t, "echo ok", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "STDOUT:\nok") {
		t.Errorf("expected STDOUT:\\nok in output; got: %s", res.Output)
	}
}

// TestBash_ProcessGroup_NonZeroExitStillRecoverable confirms that
// P0-4 does not regress Lane 2.2: non-zero exits remain recoverable
// tool_result(IsError=true), not fatal workflow errors.
func TestBash_ProcessGroup_NonZeroExitStillRecoverable(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	res, err := tool.Execute(context.Background(), makeBashParams(t, "exit 42", nil))
	if err != nil {
		t.Fatalf("Execute err must stay nil on non-zero exit: %v", err)
	}
	if !res.IsError {
		t.Fatalf("exit 42 should surface as IsError=true; got success: %s", res.Output)
	}
}

// TestBash_ProcessGroup_TimeoutMetadata documents the timeout
// message shape: the agent should see both the duration and that
// the child process tree was cleaned up.
func TestBash_ProcessGroup_TimeoutMetadata(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	res, err := tool.Execute(context.Background(), makeBashParams(t, "sleep 10", map[string]any{"timeout_ms": 200}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected timeout error; got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "status: timeout") {
		t.Errorf("output %q should carry status: timeout", res.Output)
	}
	if !strings.Contains(res.Output, "timed_out: true") {
		t.Errorf("output %q should carry timed_out: true", res.Output)
	}
}

// TestBash_ProcessGroup_SigkillEscalation verifies that a command
// which ignores SIGTERM is still killed by the follow-up SIGKILL
// after the grace period. The parent shell traps TERM/INT and loops
// forever; only SIGKILL will take it down.
func TestBash_ProcessGroup_SigkillEscalation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGKILL escalation not modeled on Windows")
	}
	root := t.TempDir()
	guard := NewPathGuard(root, nil)
	tool := NewBashTool(guard)

	ctx := WithSessionID(context.Background(), "pgrp-escal")
	sessionDir, err := guard.EnsureSessionWorkDir("pgrp-escal")
	if err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	realSession, err := filepath.EvalSymlinks(sessionDir)
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	pidFile := filepath.Join(realSession, "parent.pid")

	script := `trap "" TERM INT; echo $$ > parent.pid; while :; do sleep 1; done`
	res, err := tool.Execute(ctx, makeBashParams(t, script, map[string]any{"timeout_ms": 200}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected timeout error; got: %s", res.Output)
	}

	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("parse pid: %v", err)
	}

	// bashKillGrace (2s) + slack for reap.
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // ESRCH — parent killed via SIGKILL escalation
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("parent pid %d survived SIGTERM+grace+SIGKILL path", pid)
}

// TestBash_ProcessGroup_BackgroundChildKilledAfterNormalExit guards
// against GPT review finding P0-4-B: a child backgrounded by the user
// command survives the parent bash's normal exit unless the tool
// cleans up the leftover process group. The child inherits the same
// pgid via Setpgid=true, so terminating the group after Wait returns
// reaps it. Without this cleanup the orphaned sleep would be
// reparented to init and leak until host teardown.
func TestBash_ProcessGroup_BackgroundChildKilledAfterNormalExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background child cleanup deferred on Windows (TODO: Job Objects)")
	}
	root := t.TempDir()
	guard := NewPathGuard(root, nil)
	tool := NewBashTool(guard)

	ctx := WithSessionID(context.Background(), "pgrp-bgchild")
	sessionDir, err := guard.EnsureSessionWorkDir("pgrp-bgchild")
	if err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	realSession, err := filepath.EvalSymlinks(sessionDir)
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	pidFile := filepath.Join(realSession, "child.pid")

	// Parent bash exits 0 without waiting; sleep should be reaped by
	// the post-Wait process group cleanup.
	script := `sleep 30 >/dev/null 2>&1 & echo $! > child.pid; echo done`
	res, err := tool.Execute(ctx, makeBashParams(t, script, nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success; got error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "done") {
		t.Errorf("expected output to contain 'done'; got: %s", res.Output)
	}

	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("parse pid: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // ESRCH — background child reaped via group cleanup
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("background child pid %d still alive after normal bash exit; post-Wait group cleanup failed", pid)
}

// TestBash_Preflight_RejectsMalformedShell confirms that a command
// whose shell syntax cannot be parsed is blocked at preflight rather
// than handed to bash. Before P0-5 the AST analyzer returned
// allow-through on parser errors, which meant dangerous patterns
// hidden inside unparseable constructs could still run. P0-5 flips
// the policy to fail-closed.
func TestBash_Preflight_RejectsMalformedShell(t *testing.T) {
	root := t.TempDir()
	guard := NewPathGuard(root, nil)
	tool := NewBashTool(guard)

	ctx := WithSessionID(context.Background(), "preflight-sess")
	sessionDir, err := guard.EnsureSessionWorkDir("preflight-sess")
	if err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	realSession, err := filepath.EvalSymlinks(sessionDir)
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	sideEffect := filepath.Join(realSession, "should_not_exist.txt")

	// The unparseable prefix must short-circuit: if any byte of the
	// command reached bash, the touch would create the witness file.
	script := `((( ; touch should_not_exist.txt`
	res, err := tool.Execute(ctx, makeBashParams(t, script, nil))
	if err != nil {
		t.Fatalf("Execute err must stay nil; preflight rejection is a recoverable tool_result: %v", err)
	}
	if !res.IsError {
		t.Fatalf("malformed command must surface as IsError=true; got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "command blocked") {
		t.Errorf("output %q should mention 'command blocked'", res.Output)
	}
	if !strings.Contains(res.Output, "shell syntax") {
		t.Errorf("output %q should mention parse failure reason", res.Output)
	}
	if _, statErr := os.Stat(sideEffect); statErr == nil {
		t.Errorf("side-effect file %q was created; bash executed a rejected command", sideEffect)
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
		// System-path matcher must respect path boundaries: /usr is a
		// system root, but /usr2 or /etcd are arbitrary user-owned dirs
		// that happen to share a textual prefix. Pre-fix the matcher
		// flagged them as dangerous via strings.HasPrefix("/usr"/"/etc")
		// alone, which both over-blocked legitimate paths and signaled
		// security where there was none.
		{command: "touch /usr", dangerous: true},
		{command: "touch /usr2/file", dangerous: false},
		{command: "touch /etcd/data", dangerous: false},
		{command: "mkdir -p /lib2/safe", dangerous: false},
		{command: "cp ./file /usr2-fake/path", dangerous: false},
		// P0-5 fail-closed: unparseable commands are blocked at
		// preflight because the AST analyzer cannot vet them for
		// dangerous patterns. The reason string embeds the parser
		// error, so we only assert it is non-empty in the table.
		{command: "(((", dangerous: true, reason: "shell syntax"},
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

// TestBash_ShellPinsToAbsolutePath confirms that command execution
// cannot be hijacked by planting a fake "bash" inside the session
// workspace. With absolute-path resolution, $BASH must point at a
// system binary regardless of any sibling named "bash" in cwd, and
// the fake script must not run.
func TestBash_ShellPinsToAbsolutePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash hardening is POSIX-only")
	}

	guard := NewPathGuard(t.TempDir(), nil)
	tool := NewBashTool(guard)

	const sessID = "shell-pin-sess"
	ctx := WithSessionID(context.Background(), sessID)
	sessionDir, err := guard.EnsureSessionWorkDir(sessID)
	if err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	sentinel := filepath.Join(sessionDir, "fake-bash-fired")
	fakeBash := filepath.Join(sessionDir, "bash")
	body := "#!/bin/sh\ntouch " + strconv.Quote(sentinel) + "\nprintf 'fake-bash-output'\n"
	if writeErr := os.WriteFile(fakeBash, []byte(body), 0o755); writeErr != nil {
		t.Fatalf("write fake bash: %v", writeErr)
	}

	res, err := tool.Execute(ctx, makeBashParams(t, "printf '%s' \"$BASH\"", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Output)
	}

	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatalf("fake bash sentinel %q was created — bash binary was not pinned", sentinel)
	}
	if strings.Contains(res.Output, "fake-bash-output") {
		t.Fatalf("fake bash output leaked into result: %q", res.Output)
	}
	if !strings.Contains(res.Output, "/bash") {
		t.Fatalf("$BASH did not appear absolute in output: %q", res.Output)
	}
	if strings.Contains(res.Output, sessionDir+"/bash") {
		t.Fatalf("$BASH resolved to fake inside session dir: %q", res.Output)
	}
}

// TestBash_Metadata_SuccessShape pins the metadata header emitted on
// the success path: status / exit_code / classification all reflect a
// clean run, duration_ms is present, and the cwd is reported as a
// session-relative path rather than an absolute host directory.
func TestBash_Metadata_SuccessShape(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	res, err := tool.Execute(context.Background(), makeBashParams(t, "echo ok", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Output)
	}
	for _, want := range []string{
		"BASH RESULT\n",
		"status: success",
		"exit_code: 0",
		"timed_out: false",
		"canceled: false",
		"classification: success",
		"cwd: .",
	} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("missing metadata line %q; body=%q", want, res.Output)
		}
	}
	if !strings.Contains(res.Output, "duration_ms: ") {
		t.Errorf("duration_ms not reported; body=%q", res.Output)
	}
	if strings.Contains(res.Output, t.TempDir()) {
		t.Errorf("absolute host path leaked into LLM-facing body: %q", res.Output)
	}
}

// TestBash_Metadata_NonZeroExitClassified confirms a generic non-zero
// exit becomes status=error, exit_code=N, classification=unknown_nonzero
// while staying recoverable (IsError=true, Execute err nil).
func TestBash_Metadata_NonZeroExitClassified(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	res, err := tool.Execute(context.Background(), makeBashParams(t, "exit 42", nil))
	if err != nil {
		t.Fatalf("Execute err must stay nil for non-zero exits: %v", err)
	}
	if !res.IsError {
		t.Fatalf("exit 42 should surface as IsError=true")
	}
	for _, want := range []string{
		"status: error",
		"exit_code: 42",
		"classification: unknown_nonzero",
	} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("missing %q; body=%q", want, res.Output)
		}
	}
}

// TestBash_Metadata_CommandNotFoundClassified covers the 127 exit
// code path: bash exits 127 when the requested program is missing,
// which the classifier maps to "command_not_found" so the agent can
// react with an install/path fix rather than a generic retry.
func TestBash_Metadata_CommandNotFoundClassified(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	res, err := tool.Execute(context.Background(),
		makeBashParams(t, "elnath-not-a-real-binary-xyz", nil))
	if err != nil {
		t.Fatalf("Execute err must stay nil: %v", err)
	}
	if !res.IsError {
		t.Fatalf("missing binary should surface as IsError=true; got: %s", res.Output)
	}
	for _, want := range []string{
		"status: error",
		"exit_code: 127",
		"classification: command_not_found",
	} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("missing %q; body=%q", want, res.Output)
		}
	}
}

// TestBash_Metadata_TimeoutShape pairs the captured-output guarantee
// with the metadata: a timed-out command must report status=timeout,
// timed_out=true, and canceled=false. duration_ms is bounded by the
// timeout (plus the kill-grace window) so the agent can tell whether
// the kill was clean.
func TestBash_Metadata_TimeoutShape(t *testing.T) {
	tool := NewBashTool(NewPathGuard(t.TempDir(), nil))
	res, err := tool.Execute(context.Background(), makeBashParams(t, "sleep 10", map[string]any{
		"timeout_ms": 200,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected timeout error result, got success: %s", res.Output)
	}
	for _, want := range []string{
		"status: timeout",
		"timed_out: true",
		"canceled: false",
		"classification: timeout",
	} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("missing %q; body=%q", want, res.Output)
		}
	}
	if strings.Contains(res.Output, "exit_code: 0") {
		t.Errorf("timeout must not report a clean exit code; body=%q", res.Output)
	}
}

// TestBash_Metadata_CWDStaysSessionRelative pins the no-host-path-leak
// invariant for the cwd field: the metadata must report the
// working directory relative to the session root, never the absolute
// host path that would expose /Users/... or /tmp/... details.
func TestBash_Metadata_CWDStaysSessionRelative(t *testing.T) {
	root := t.TempDir()
	guard := NewPathGuard(root, nil)
	tool := NewBashTool(guard)

	const sessID = "cwd-rel-sess"
	ctx := WithSessionID(context.Background(), sessID)
	sessionDir, err := guard.EnsureSessionWorkDir(sessID)
	if err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sessionDir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	res, err := tool.Execute(ctx, makeBashParams(t, "echo ok", map[string]any{
		"working_dir": "nested",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "cwd: nested") {
		t.Errorf("cwd not session-relative; body=%q", res.Output)
	}
	if strings.Contains(res.Output, "cwd: "+sessionDir) {
		t.Errorf("absolute session path leaked into cwd; body=%q", res.Output)
	}
}
