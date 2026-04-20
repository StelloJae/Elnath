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

// TestPrintCommandHelp_DaemonMatchesDispatcher guards against help-text drift.
// cmdDaemon accepts start/submit/status/stop/install; the extended help must
// not advertise subcommands the dispatcher rejects (like "task submit",
// "task list", "task cancel") or the user hits "unknown daemon subcommand:
// task" when copy-pasting from help.
func TestPrintCommandHelp_DaemonMatchesDispatcher(t *testing.T) {
	stdout, stderr := captureOutput(t, func() {
		if err := printCommandHelp("daemon"); err != nil {
			t.Fatalf("printCommandHelp(daemon) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	mustContain := []string{"USAGE", "start", "submit", "status", "stop", "install"}
	for _, term := range mustContain {
		if !strings.Contains(stdout, term) {
			t.Fatalf("daemon help missing expected subcommand %q; got:\n%s", term, stdout)
		}
	}

	mustNotContain := []string{"task submit", "task list", "task cancel"}
	for _, term := range mustNotContain {
		if strings.Contains(stdout, term) {
			t.Fatalf("daemon help advertises drifted term %q that cmdDaemon rejects; got:\n%s", term, stdout)
		}
	}
}
