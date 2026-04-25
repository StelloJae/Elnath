package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B3b-1 file tool containment tests.
//
// These tests assert the post-fix behavior of write_file / edit_file /
// read_file: when a session id is bound to ctx, the tool MUST resolve
// paths against the session workspace and reject any resolved target
// that escapes the session boundary (after symlink-safe canonicalization).
//
// Until the B3b-1 fix lands, the tools call PathGuard.Resolve which
// resolves against the root WorkDir without escape detection — so these
// assertions fail (RED state). Implementation is expected to extend
// PathGuard with a session-scoped resolver and rewire the three tool
// callsites (file.go:74, file.go:175, file.go:269).

func b3b1Setup(t *testing.T, sessionID string) (root, sessionDir string, ctx context.Context) {
	t.Helper()
	root = t.TempDir()
	guard := NewPathGuard(root, nil)
	dir, err := guard.EnsureSessionWorkDir(sessionID)
	if err != nil {
		t.Fatalf("EnsureSessionWorkDir: %v", err)
	}
	ctx = WithSessionID(context.Background(), sessionID)
	return root, dir, ctx
}

func b3b1MarshalParams(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

// ---------------------------------------------------------------------------
// WriteTool containment
// ---------------------------------------------------------------------------

func TestWriteTool_RejectsAbsolutePathOutsideSession(t *testing.T) {
	root, _, ctx := b3b1Setup(t, "sess-A")
	tool := NewWriteTool(NewPathGuard(root, nil))

	outside := filepath.Join(root, "outside-target.txt")
	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path": outside,
		"content":   "should not land",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for absolute path outside session; output: %s", res.Output)
	}
	if _, statErr := os.Stat(outside); statErr == nil {
		t.Fatalf("file unexpectedly created at %s — session escape was not blocked", outside)
	}
}

func TestWriteTool_RejectsParentDirEscape(t *testing.T) {
	_, _, ctx := b3b1Setup(t, "sess-A")
	tool := NewWriteTool(NewPathGuard(t.TempDir(), nil))

	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path": "../../../../escape.txt",
		"content":   "should not land",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for ../ escape; output: %s", res.Output)
	}
}

func TestWriteTool_AllowsRelativePathInSession(t *testing.T) {
	root, sessionDir, ctx := b3b1Setup(t, "sess-A")
	tool := NewWriteTool(NewPathGuard(root, nil))

	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path": "inside.txt",
		"content":   "ok",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("relative write inside session should succeed: %s", res.Output)
	}
	want := filepath.Join(sessionDir, "inside.txt")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("expected file at %s: %v", want, err)
	}
	if string(data) != "ok" {
		t.Errorf("content = %q, want %q", string(data), "ok")
	}
}

func TestWriteTool_AllowsAbsolutePathInsideSession(t *testing.T) {
	root, sessionDir, ctx := b3b1Setup(t, "sess-A")
	tool := NewWriteTool(NewPathGuard(root, nil))

	target := filepath.Join(sessionDir, "abs-inside.txt")
	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path": target,
		"content":   "ok-abs",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("absolute write inside session should succeed: %s", res.Output)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected file at %s: %v", target, err)
	}
	if string(data) != "ok-abs" {
		t.Errorf("content = %q, want %q", string(data), "ok-abs")
	}
}

func TestWriteTool_RejectsSymlinkEscape(t *testing.T) {
	root, sessionDir, ctx := b3b1Setup(t, "sess-A")

	outside := filepath.Join(t.TempDir(), "elnath-b3b1-write-target")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}

	link := filepath.Join(sessionDir, "escape-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	tool := NewWriteTool(NewPathGuard(root, nil))
	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path": "escape-link/leak.txt",
		"content":   "should not land outside",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for symlink escape; output: %s", res.Output)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "leak.txt")); statErr == nil {
		t.Fatalf("file unexpectedly created via symlink — escape was not blocked")
	}
}

func TestWriteTool_RejectsPrefixTrickAcrossSessions(t *testing.T) {
	root, _, ctxA := b3b1Setup(t, "abc")
	guard := NewPathGuard(root, nil)
	otherSession, err := guard.EnsureSessionWorkDir("abcdef")
	if err != nil {
		t.Fatalf("EnsureSessionWorkDir abcdef: %v", err)
	}
	tool := NewWriteTool(guard)

	target := filepath.Join(otherSession, "victim.txt")
	res, err := tool.Execute(ctxA, b3b1MarshalParams(t, map[string]any{
		"file_path": target,
		"content":   "cross-session leak",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for cross-session prefix trick; output: %s", res.Output)
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Fatalf("cross-session write succeeded — prefix trick was not blocked")
	}
}

func TestWriteTool_RejectsTildeHostHomeEscape(t *testing.T) {
	root, _, ctx := b3b1Setup(t, "sess-A")
	tool := NewWriteTool(NewPathGuard(root, nil))

	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path": "~/elnath-b3b1-tilde-leak.txt",
		"content":   "should not land in host home",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for tilde host-home path; output: %s", res.Output)
	}
	if home, herr := os.UserHomeDir(); herr == nil {
		leak := filepath.Join(home, "elnath-b3b1-tilde-leak.txt")
		if _, statErr := os.Stat(leak); statErr == nil {
			_ = os.Remove(leak)
			t.Fatalf("file leaked to host home — tilde escape was not blocked")
		}
	}
}

// ---------------------------------------------------------------------------
// EditTool containment
// ---------------------------------------------------------------------------

func TestEditTool_RejectsAbsolutePathOutsideSession(t *testing.T) {
	root, _, ctx := b3b1Setup(t, "sess-A")
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tool := NewEditTool(NewPathGuard(root, nil))

	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path":  outside,
		"old_string": "hello",
		"new_string": "pwned",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for absolute path outside session; output: %s", res.Output)
	}
	data, _ := os.ReadFile(outside)
	if strings.Contains(string(data), "pwned") {
		t.Fatalf("file outside session was modified despite expected rejection")
	}
}

func TestEditTool_RejectsParentDirEscape(t *testing.T) {
	root, _, ctx := b3b1Setup(t, "sess-A")
	outside := filepath.Join(root, "victim.txt")
	if err := os.WriteFile(outside, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tool := NewEditTool(NewPathGuard(root, nil))

	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path":  "../../../victim.txt",
		"old_string": "x",
		"new_string": "y",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for ../ escape; output: %s", res.Output)
	}
}

func TestEditTool_RejectsSymlinkEscape(t *testing.T) {
	root, sessionDir, ctx := b3b1Setup(t, "sess-A")

	outside := filepath.Join(t.TempDir(), "elnath-b3b1-edit-target")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed victim: %v", err)
	}

	link := filepath.Join(sessionDir, "outdir")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	tool := NewEditTool(NewPathGuard(root, nil))
	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path":  "outdir/victim.txt",
		"old_string": "hello",
		"new_string": "pwned",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for symlink escape; output: %s", res.Output)
	}
	data, _ := os.ReadFile(victim)
	if strings.Contains(string(data), "pwned") {
		t.Fatalf("victim file was modified via symlink — escape not blocked")
	}
}

func TestEditTool_RejectsTildeHostHomeEscape(t *testing.T) {
	root, _, ctx := b3b1Setup(t, "sess-A")
	tool := NewEditTool(NewPathGuard(root, nil))

	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path":  "~/elnath-b3b1-tilde-edit.txt",
		"old_string": "x",
		"new_string": "y",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for tilde host-home path; output: %s", res.Output)
	}
}

// ---------------------------------------------------------------------------
// ReadTool containment
//
// Per partner verdict ("read/write policy clearly separated"), reads MAY have
// distinct acceptance shape. For B3b-1 baseline reads also reject session
// escape; explicit allowRead config (e.g., /etc/os-release) is a future
// sub-lane after substrate ships.
// ---------------------------------------------------------------------------

func TestReadTool_RejectsAbsolutePathOutsideSession(t *testing.T) {
	root, _, ctx := b3b1Setup(t, "sess-A")
	outside := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(outside, []byte("top-secret\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tool := NewReadTool(NewPathGuard(root, nil))

	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path": outside,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for absolute read outside session; output: %s", res.Output)
	}
	if strings.Contains(res.Output, "top-secret") {
		t.Fatalf("read leaked outside-session secret content")
	}
}

func TestReadTool_RejectsParentDirEscape(t *testing.T) {
	root, _, ctx := b3b1Setup(t, "sess-A")
	if err := os.WriteFile(filepath.Join(root, "victim.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tool := NewReadTool(NewPathGuard(root, nil))

	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path": "../../../victim.txt",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for ../ read escape; output: %s", res.Output)
	}
}

func TestReadTool_RejectsSymlinkEscape(t *testing.T) {
	root, sessionDir, ctx := b3b1Setup(t, "sess-A")

	outside := filepath.Join(t.TempDir(), "elnath-b3b1-read-target")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("top-secret\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	link := filepath.Join(sessionDir, "outdir")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	tool := NewReadTool(NewPathGuard(root, nil))
	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path": "outdir/secret.txt",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for read symlink escape; output: %s", res.Output)
	}
	if strings.Contains(res.Output, "top-secret") {
		t.Fatalf("read leaked outside-session secret content via symlink")
	}
}

func TestReadTool_AllowsRelativePathInSession(t *testing.T) {
	root, sessionDir, ctx := b3b1Setup(t, "sess-A")
	if err := os.WriteFile(filepath.Join(sessionDir, "ok.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tool := NewReadTool(NewPathGuard(root, nil))

	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path": "ok.txt",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("relative read inside session should succeed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "hi") {
		t.Errorf("expected content 'hi' in output, got %q", res.Output)
	}
}

func TestReadTool_RejectsTildeHostHomeEscape(t *testing.T) {
	root, _, ctx := b3b1Setup(t, "sess-A")
	tool := NewReadTool(NewPathGuard(root, nil))

	res, err := tool.Execute(ctx, b3b1MarshalParams(t, map[string]any{
		"file_path": "~/.ssh/id_rsa",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for tilde host-home path; output: %s", res.Output)
	}
}

// ---------------------------------------------------------------------------
// Legacy fallback (no session id) — ensures B3b-1 does not break callers
// that never bind a session id to ctx (e.g., legacy tests).
// ---------------------------------------------------------------------------

func TestWriteTool_NoSessionResolvesAgainstWorkDir(t *testing.T) {
	root := t.TempDir()
	tool := NewWriteTool(NewPathGuard(root, nil))

	res, err := tool.Execute(context.Background(), b3b1MarshalParams(t, map[string]any{
		"file_path": "legacy.txt",
		"content":   "ok",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("legacy write should succeed: %s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(root, "legacy.txt")); err != nil {
		t.Fatalf("legacy file not at expected path: %v", err)
	}
}
