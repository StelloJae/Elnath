package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortabilityHelp(t *testing.T) {
	stdout, _ := captureOutput(t, func() {
		if err := cmdPortability(context.Background(), nil); err != nil {
			t.Fatalf("cmdPortability help: %v", err)
		}
	})
	if !strings.Contains(stdout, "Usage: elnath portability") {
		t.Fatalf("stdout = %q, want portability usage", stdout)
	}
}

func TestPortabilityUnknownSubcommand(t *testing.T) {
	err := cmdPortability(context.Background(), []string{"unknown"})
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("cmdPortability(unknown) err = %v, want unknown subcommand", err)
	}
}

func TestPortabilityExportIntegration(t *testing.T) {
	root := writePortabilityCommandFixture(t)
	bundlePath := filepath.Join(t.TempDir(), "bundle.eln")
	passFile := writePassphraseFile(t, "strong-passphrase\n")
	withArgs(t, []string{"elnath", "--data-dir", root})

	if err := cmdPortability(context.Background(), []string{"export", "--out", bundlePath, "--passphrase-file", passFile}); err != nil {
		t.Fatalf("cmdPortability export: %v", err)
	}
	if _, err := os.Stat(bundlePath); err != nil {
		t.Fatalf("bundle stat: %v", err)
	}
}

func TestPortabilityImportDryRun(t *testing.T) {
	source := writePortabilityCommandFixture(t)
	bundlePath := filepath.Join(t.TempDir(), "bundle.eln")
	passFile := writePassphraseFile(t, "strong-passphrase\n")
	withArgs(t, []string{"elnath", "--data-dir", source})
	if err := cmdPortability(context.Background(), []string{"export", "--out", bundlePath, "--passphrase-file", passFile}); err != nil {
		t.Fatalf("cmdPortability export: %v", err)
	}

	target := t.TempDir()
	withArgs(t, []string{"elnath", "--data-dir", target})
	stdout, _ := captureOutput(t, func() {
		if err := cmdPortability(context.Background(), []string{"import", bundlePath, "--dry-run", "--passphrase-file", passFile}); err != nil {
			t.Fatalf("cmdPortability import --dry-run: %v", err)
		}
	})
	if !strings.Contains(stdout, "would apply") {
		t.Fatalf("stdout = %q, want dry-run summary", stdout)
	}
}

func TestRunPortabilityWithGlobalDataDirFlag(t *testing.T) {
	root := t.TempDir()
	stdout, _ := captureOutput(t, func() {
		if err := run(context.Background(), []string{"elnath", "--data-dir", root, "portability"}); err != nil {
			t.Fatalf("run(portability): %v", err)
		}
	})
	if !strings.Contains(stdout, "Usage: elnath portability") {
		t.Fatalf("stdout = %q, want portability usage", stdout)
	}
}

func TestPortabilityListSupportsGlobalDataDirAfterCommand(t *testing.T) {
	root := t.TempDir()
	withArgs(t, []string{"elnath", "portability", "--data-dir", root})

	stdout, _ := captureOutput(t, func() {
		if err := cmdPortability(context.Background(), []string{"--data-dir", root, "list"}); err != nil {
			t.Fatalf("cmdPortability(list with global flag): %v", err)
		}
	})
	if !strings.Contains(stdout, "No portability exports found.") {
		t.Fatalf("stdout = %q, want empty-history message", stdout)
	}
}

func writePortabilityCommandFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("permission:\n  mode: default\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "lessons.jsonl"), []byte(`{"id":"1"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write lessons: %v", err)
	}
	return root
}

func writePassphraseFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "passphrase.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write passphrase file: %v", err)
	}
	return path
}
