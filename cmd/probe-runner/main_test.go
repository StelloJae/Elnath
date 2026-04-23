package main

import (
	"errors"
	"os"
	"path/filepath"
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
