package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return raw
}

// TestReadTool creates a temp file then reads it via ReadTool.
func TestReadTool(t *testing.T) {
	dir := t.TempDir()
	content := "line one\nline two\nline three\n"
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := NewReadTool(dir)
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"file_path": "test.txt",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	for _, line := range []string{"line one", "line two", "line three"} {
		if !strings.Contains(res.Output, line) {
			t.Errorf("output does not contain %q:\n%s", line, res.Output)
		}
	}
}

func TestReadToolTruncatesLargeOutput(t *testing.T) {
	dir := t.TempDir()
	largeLine := strings.Repeat("a", 2000)
	var content strings.Builder
	for i := 0; i < 80; i++ {
		content.WriteString(largeLine)
		content.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(content.String()), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := NewReadTool(dir)
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"file_path": "large.txt",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if len(res.Output) > toolMaxOutputBytes {
		t.Fatalf("output len = %d, want <= %d", len(res.Output), toolMaxOutputBytes)
	}
	if !strings.Contains(res.Output, "output truncated") {
		t.Fatalf("expected truncation marker in output")
	}
	if !strings.Contains(res.Output, "     1\t") {
		t.Fatalf("expected numbered read output, got %q", res.Output[:40])
	}
}

// TestWriteTool writes via WriteTool then reads back and checks content.
func TestWriteTool(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)

	content := "hello from write tool"
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"file_path": "out.txt",
		"content":   content,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}

	data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile after write: %v", err)
	}
	if string(data) != content {
		t.Errorf("written content = %q, want %q", string(data), content)
	}
}

// TestGlobTool creates several files then globs for *.txt.
func TestGlobTool(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("setup %s: %v", name, err)
		}
	}

	tool := NewGlobTool(dir)
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"pattern": "*.txt",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}

	if !strings.Contains(res.Output, "a.txt") {
		t.Errorf("output does not contain a.txt:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "b.txt") {
		t.Errorf("output does not contain b.txt:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "c.go") {
		t.Errorf("output should not contain c.go:\n%s", res.Output)
	}
}

// TestGrepTool creates files with known content then greps for a pattern.
func TestGrepTool(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"alpha.txt": "the quick brown fox\njumps over the lazy dog\n",
		"beta.txt":  "no match here\n",
		"gamma.txt": "another quick example\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("setup %s: %v", name, err)
		}
	}

	tool := NewGrepTool(dir)
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"pattern": "quick",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}

	if !strings.Contains(res.Output, "alpha.txt") {
		t.Errorf("output does not mention alpha.txt:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "gamma.txt") {
		t.Errorf("output does not mention gamma.txt:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "beta.txt") {
		t.Errorf("output should not mention beta.txt:\n%s", res.Output)
	}
}

// ---------------------------------------------------------------------------
// Accessor tests
// ---------------------------------------------------------------------------

func TestReadToolAccessors(t *testing.T) {
	tool := NewReadTool(t.TempDir())
	if tool.Name() != "read_file" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "read_file")
	}
	if tool.Description() == "" {
		t.Error("Description() empty")
	}
	if len(tool.Schema()) == 0 {
		t.Error("Schema() empty")
	}
}

func TestWriteToolAccessors(t *testing.T) {
	tool := NewWriteTool(t.TempDir())
	if tool.Name() != "write_file" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "write_file")
	}
	if tool.Description() == "" {
		t.Error("Description() empty")
	}
	if len(tool.Schema()) == 0 {
		t.Error("Schema() empty")
	}
}

func TestGlobToolAccessors(t *testing.T) {
	tool := NewGlobTool(t.TempDir())
	if tool.Name() != "glob" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "glob")
	}
	if tool.Description() == "" {
		t.Error("Description() empty")
	}
	if len(tool.Schema()) == 0 {
		t.Error("Schema() empty")
	}
}

func TestGrepToolAccessors(t *testing.T) {
	tool := NewGrepTool(t.TempDir())
	if tool.Name() != "grep" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "grep")
	}
	if tool.Description() == "" {
		t.Error("Description() empty")
	}
	if len(tool.Schema()) == 0 {
		t.Error("Schema() empty")
	}
}

func TestEditToolAccessors(t *testing.T) {
	tool := NewEditTool(t.TempDir())
	if tool.Name() != "edit_file" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "edit_file")
	}
	if tool.Description() == "" {
		t.Error("Description() empty")
	}
	if len(tool.Schema()) == 0 {
		t.Error("Schema() empty")
	}
}

// ---------------------------------------------------------------------------
// EditTool tests
// ---------------------------------------------------------------------------

func TestEditTool(t *testing.T) {
	setup := func(t *testing.T, content string) (string, *EditTool) {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "edit.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		return dir, NewEditTool(dir)
	}

	t.Run("successful single replacement", func(t *testing.T) {
		dir, tool := setup(t, "hello world\n")
		res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
			"file_path":  "edit.txt",
			"old_string": "hello",
			"new_string": "goodbye",
		}))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Output)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "edit.txt"))
		if !strings.Contains(string(data), "goodbye") {
			t.Errorf("file content %q missing replacement", string(data))
		}
	})

	t.Run("old_string not found", func(t *testing.T) {
		_, tool := setup(t, "hello world\n")
		res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
			"file_path":  "edit.txt",
			"old_string": "nonexistent",
			"new_string": "x",
		}))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !res.IsError {
			t.Errorf("expected error when old_string not found")
		}
	})

	t.Run("ambiguous match without replace_all", func(t *testing.T) {
		_, tool := setup(t, "foo foo\n")
		res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
			"file_path":  "edit.txt",
			"old_string": "foo",
			"new_string": "bar",
		}))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !res.IsError {
			t.Errorf("expected error for ambiguous match")
		}
	})

	t.Run("replace_all replaces all occurrences", func(t *testing.T) {
		dir, tool := setup(t, "foo foo foo\n")
		res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
			"file_path":   "edit.txt",
			"old_string":  "foo",
			"new_string":  "bar",
			"replace_all": true,
		}))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Output)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "edit.txt"))
		if strings.Contains(string(data), "foo") {
			t.Errorf("file still contains 'foo' after replace_all: %q", string(data))
		}
		if !strings.Contains(string(data), "bar") {
			t.Errorf("file missing 'bar' after replace_all: %q", string(data))
		}
	})

	t.Run("invalid params", func(t *testing.T) {
		_, tool := setup(t, "hello\n")
		res, err := tool.Execute(context.Background(), json.RawMessage(`not-json`))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !res.IsError {
			t.Errorf("expected error for invalid params")
		}
	})

	t.Run("path traversal blocked", func(t *testing.T) {
		_, tool := setup(t, "hello\n")
		res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
			"file_path":  "../escape",
			"old_string": "x",
			"new_string": "y",
		}))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !res.IsError {
			t.Errorf("expected error for path traversal")
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		dir := t.TempDir()
		tool := NewEditTool(dir)
		res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
			"file_path":  "nosuchfile.txt",
			"old_string": "x",
			"new_string": "y",
		}))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !res.IsError {
			t.Errorf("expected error for nonexistent file")
		}
	})
}

// ---------------------------------------------------------------------------
// ReadTool edge cases
// ---------------------------------------------------------------------------

func TestReadToolBinaryFile(t *testing.T) {
	dir := t.TempDir()
	data := []byte("hello\x00world")
	if err := os.WriteFile(filepath.Join(dir, "bin.bin"), data, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := NewReadTool(dir)
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"file_path": "bin.bin",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error for binary file, got: %s", res.Output)
	}
}

func TestReadToolOffset(t *testing.T) {
	dir := t.TempDir()
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filepath.Join(dir, "lines.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := NewReadTool(dir)
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"file_path": "lines.txt",
		"offset":    2,
		"limit":     1,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "line2") {
		t.Errorf("output %q should contain line2", res.Output)
	}
	if strings.Contains(res.Output, "line1") {
		t.Errorf("output %q should not contain line1 when offset=2", res.Output)
	}
}

func TestResolvePathTraversal(t *testing.T) {
	base := t.TempDir()

	_, err := resolvePath(base, "../outside")
	if err == nil {
		t.Error("expected error for ../outside traversal")
	}

	abs, err := resolvePath(base, "subdir/file.txt")
	if err != nil {
		t.Errorf("unexpected error for safe path: %v", err)
	}
	if !strings.HasPrefix(abs, base) {
		t.Errorf("resolved path %q does not start with base %q", abs, base)
	}
}

// ---------------------------------------------------------------------------
// WriteTool edge cases
// ---------------------------------------------------------------------------

func TestWriteToolNestedDir(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)

	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"file_path": "a/b/c/nested.txt",
		"content":   "nested content",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}

	data, err := os.ReadFile(filepath.Join(dir, "a", "b", "c", "nested.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "nested content" {
		t.Errorf("content = %q, want %q", string(data), "nested content")
	}
}

func TestWriteToolPathTraversal(t *testing.T) {
	tool := NewWriteTool(t.TempDir())

	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"file_path": "../escape.txt",
		"content":   "bad",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error for path traversal")
	}
}

// ---------------------------------------------------------------------------
// GlobTool edge cases
// ---------------------------------------------------------------------------

func TestGlobRecursive(t *testing.T) {
	dir := t.TempDir()
	sub1 := filepath.Join(dir, "sub1")
	sub2 := filepath.Join(dir, "sub2")
	if err := os.MkdirAll(sub1, 0o755); err != nil {
		t.Fatalf("mkdir sub1: %v", err)
	}
	if err := os.MkdirAll(sub2, 0o755); err != nil {
		t.Fatalf("mkdir sub2: %v", err)
	}
	for _, f := range []string{
		filepath.Join(sub1, "a.go"),
		filepath.Join(sub2, "b.go"),
		filepath.Join(sub1, "c.txt"),
	} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	tool := NewGlobTool(dir)
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"pattern": "**/*.go",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "a.go") {
		t.Errorf("output should contain a.go:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "b.go") {
		t.Errorf("output should contain b.go:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "c.txt") {
		t.Errorf("output should not contain c.txt:\n%s", res.Output)
	}
}

// ---------------------------------------------------------------------------
// GrepTool edge cases
// ---------------------------------------------------------------------------

func TestGrepToolIncludeFilter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("setup go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("package of tea\n"), 0o644); err != nil {
		t.Fatalf("setup txt: %v", err)
	}

	tool := NewGrepTool(dir)
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"pattern": "package",
		"include": "*.go",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "main.go") {
		t.Errorf("output should contain main.go:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "notes.txt") {
		t.Errorf("output should not contain notes.txt:\n%s", res.Output)
	}
}

func TestGrepToolInvalidPattern(t *testing.T) {
	tool := NewGrepTool(t.TempDir())

	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"pattern": "[invalid",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error for invalid regex, got: %s", res.Output)
	}
}

func TestGrepToolNoMatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := NewGrepTool(dir)
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"pattern": "zzznomatch",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "(no matches)") {
		t.Errorf("output should be (no matches), got: %s", res.Output)
	}
}
