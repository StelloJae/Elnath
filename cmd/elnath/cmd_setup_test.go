package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCmdSetupQuickstart_WritesConfigAndMetric(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	oldStdin := os.Stdin
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer func() {
		os.Stdin = oldStdin
		_ = stdinR.Close()
	}()
	os.Stdin = stdinR

	go func() {
		_, _ = stdinW.WriteString("\n")
		_ = stdinW.Close()
	}()

	cfgPath := filepath.Join(home, ".elnath", "config.yaml")
	stdout, stderr := captureOutput(t, func() {
		if err := cmdSetupQuickstart(context.Background(), cfgPath); err != nil {
			t.Fatalf("cmdSetupQuickstart error = %v", err)
		}
	})
	if stdout == "" {
		t.Fatal("stdout is empty, expected quickstart output")
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".elnath", "data", "onboarding_metric.json")); err != nil {
		t.Fatalf("metric file missing: %v", err)
	}
	if stderr == "" {
		t.Fatal("stderr is empty, expected demo skip message without provider")
	}
}
