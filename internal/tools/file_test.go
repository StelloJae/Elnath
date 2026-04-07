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
