package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWithProbeWorkDir(t *testing.T) {
	t.Run("creates dir accessible to fn and cleans up after", func(t *testing.T) {
		t.Setenv("ELNATH_KEEP_PROBE_DIR", "")
		var captured string
		err := withProbeWorkDir("P01", func(dir string) error {
			captured = dir
			if _, statErr := os.Stat(dir); statErr != nil {
				t.Fatalf("expected dir to exist during fn: %v", statErr)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("withProbeWorkDir returned %v", err)
		}
		if captured == "" {
			t.Fatal("fn did not receive a dir path")
		}
		if _, statErr := os.Stat(captured); !os.IsNotExist(statErr) {
			t.Fatalf("expected dir removed, got stat err=%v", statErr)
		}
	})

	t.Run("preserves dir when ELNATH_KEEP_PROBE_DIR=1", func(t *testing.T) {
		t.Setenv("ELNATH_KEEP_PROBE_DIR", "1")
		var captured string
		err := withProbeWorkDir("P19", func(dir string) error {
			captured = dir
			return nil
		})
		if err != nil {
			t.Fatalf("withProbeWorkDir returned %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(captured) })
		if _, statErr := os.Stat(captured); statErr != nil {
			t.Fatalf("expected dir preserved, stat err=%v", statErr)
		}
	})

	t.Run("cleans up even when fn returns error", func(t *testing.T) {
		t.Setenv("ELNATH_KEEP_PROBE_DIR", "")
		sentinel := errors.New("fn failure")
		var captured string
		err := withProbeWorkDir("P07", func(dir string) error {
			captured = dir
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected sentinel propagated, got %v", err)
		}
		if _, statErr := os.Stat(captured); !os.IsNotExist(statErr) {
			t.Fatalf("expected dir removed after fn error, stat err=%v", statErr)
		}
	})

	t.Run("includes sanitized probe id in dir name", func(t *testing.T) {
		t.Setenv("ELNATH_KEEP_PROBE_DIR", "")
		var captured string
		err := withProbeWorkDir("P01/slash", func(dir string) error {
			captured = dir
			return nil
		})
		if err != nil {
			t.Fatalf("withProbeWorkDir returned %v", err)
		}
		base := filepath.Base(captured)
		if !strings.HasPrefix(base, "elnath-probe-") {
			t.Fatalf("expected elnath-probe- prefix, got %s", base)
		}
		if strings.ContainsAny(base, `/\`) {
			t.Fatalf("path separator leaked into dir name: %s", base)
		}
		if !strings.Contains(base, "P01") {
			t.Fatalf("probe id truncated entirely: %s", base)
		}
	})
}

func TestResolveExecutablePath(t *testing.T) {
	t.Run("absolute path returned unchanged", func(t *testing.T) {
		abs := "/usr/bin/env"
		got, err := resolveExecutablePath(abs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != abs {
			t.Fatalf("got %q, want %q", got, abs)
		}
	})

	t.Run("bare command uses PATH lookup", func(t *testing.T) {
		dir := t.TempDir()
		name := "elnath-fake-cmd"
		bin := filepath.Join(dir, name)
		if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write fake: %v", err)
		}
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		got, err := resolveExecutablePath(name)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != bin {
			t.Fatalf("got %q, want %q", got, bin)
		}
	})

	t.Run("relative path with separator becomes absolute", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		got, err := resolveExecutablePath("./local-tool")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !filepath.IsAbs(got) {
			t.Fatalf("expected absolute path, got %q", got)
		}
		if filepath.Base(got) != "local-tool" {
			t.Fatalf("expected base local-tool, got %q", got)
		}
	})

	t.Run("empty spec is an error", func(t *testing.T) {
		if _, err := resolveExecutablePath(""); err == nil {
			t.Fatal("expected error on empty spec")
		}
	})
}

func TestRunElnathProbeSurvivesCwdIsolation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based fake binary not portable to Windows")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "elnath-fake")
	script := "#!/bin/sh\ncat > /dev/null\nprintf '[tokens: 10 in / 20 out | tools: 0]\\n'\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	resolved, err := resolveExecutablePath(fake)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	p := probe{ID: "SMOKE", Prompt: "hello"}
	m := runElnathProbe(p, resolved, false)
	if m.Error != "" {
		t.Fatalf("runElnathProbe error: %s", m.Error)
	}
	if m.Turns == 0 {
		t.Fatalf("expected turns>0 (stdout parse should match fake [tokens: ...])")
	}
}
