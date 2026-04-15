package main

import (
	"context"
	"strings"
	"testing"
)

func TestPrintCommandHelp_Run(t *testing.T) {
	stdout, stderr := captureOutput(t, func() {
		if err := printCommandHelp("run"); err != nil {
			t.Fatalf("printCommandHelp(run) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "USAGE") || !strings.Contains(stdout, "SEE ALSO") {
		t.Fatalf("stdout = %q, want man-page help text", stdout)
	}
}

func TestExecuteCommand_ErrorsRegistered(t *testing.T) {
	stdout, stderr := captureOutput(t, func() {
		if err := executeCommand(context.Background(), "errors", []string{"list"}); err != nil {
			t.Fatalf("executeCommand(errors list) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "ELN-001") {
		t.Fatalf("stdout = %q, want error catalog", stdout)
	}
}

func TestExecuteCommand_HelpAfterOtherFlags(t *testing.T) {
	stdout, stderr := captureOutput(t, func() {
		if err := executeCommand(context.Background(), "setup", []string{"--quickstart", "--help"}); err != nil {
			t.Fatalf("executeCommand(setup ... --help) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "--quickstart") || !strings.Contains(stdout, "USAGE") {
		t.Fatalf("stdout = %q, want setup help text", stdout)
	}
}

func TestExecuteCommand_ErrorsHelp(t *testing.T) {
	stdout, stderr := captureOutput(t, func() {
		if err := executeCommand(context.Background(), "errors", []string{"--help"}); err != nil {
			t.Fatalf("executeCommand(errors --help) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Usage: elnath errors <code|list>") {
		t.Fatalf("stdout = %q, want errors help text", stdout)
	}
}
