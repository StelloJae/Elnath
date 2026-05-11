package main

import (
	"context"
	"encoding/json"
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

func TestCommandCatalog_DefaultHidesInternalCommands(t *testing.T) {
	catalog := commandCatalog(false)
	seen := make(map[string]commandCatalogEntry)
	for _, entry := range catalog {
		if entry.Hidden {
			t.Fatalf("default catalog exposed hidden command %q", entry.Name)
		}
		if entry.Description == "" {
			t.Fatalf("catalog entry %q has empty description", entry.Name)
		}
		seen[entry.Name] = entry
	}

	for _, name := range []string{"run", "skill", "daemon", "eval", "agentic"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("default catalog missing user command %q", name)
		}
	}
	for _, name := range []string{"netproxy", "netproxy-bridge", "netproxy-bridge-spike"} {
		if _, ok := seen[name]; ok {
			t.Fatalf("default catalog includes internal command %q", name)
		}
	}
}

func TestCommandCatalog_IncludeHiddenShowsInternalCommands(t *testing.T) {
	catalog := commandCatalog(true)
	seen := make(map[string]commandCatalogEntry)
	for _, entry := range catalog {
		seen[entry.Name] = entry
	}

	for _, name := range []string{"netproxy", "netproxy-bridge", "netproxy-bridge-spike"} {
		entry, ok := seen[name]
		if !ok {
			t.Fatalf("hidden catalog missing internal command %q", name)
		}
		if !entry.Hidden {
			t.Fatalf("internal command %q Hidden = false, want true", name)
		}
	}
}

func TestExecuteCommand_CommandsJSON(t *testing.T) {
	stdout, stderr := captureOutput(t, func() {
		if err := executeCommand(context.Background(), "commands", []string{"--json"}); err != nil {
			t.Fatalf("executeCommand(commands --json) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	var entries []commandCatalogEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	seen := make(map[string]commandCatalogEntry)
	for _, entry := range entries {
		seen[entry.Name] = entry
		if entry.Hidden {
			t.Fatalf("commands --json exposed hidden command %q", entry.Name)
		}
	}
	for _, name := range []string{"commands", "run", "skill"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("commands --json missing %q", name)
		}
	}
}

func TestCommandRegistryBuiltFromSpecs(t *testing.T) {
	registry := commandRegistry()
	for _, spec := range commandSpecs() {
		if spec.Name == "" {
			t.Fatal("commandSpecs contains empty name")
		}
		if spec.Runner == nil {
			t.Fatalf("command %q has nil runner", spec.Name)
		}
		if _, ok := registry[spec.Name]; !ok {
			t.Fatalf("registry missing command %q", spec.Name)
		}
		for _, alias := range spec.Aliases {
			if _, ok := registry[alias]; !ok {
				t.Fatalf("registry missing alias %q for command %q", alias, spec.Name)
			}
		}
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

func TestPrintCommandHelp_AgenticMatchesDispatcher(t *testing.T) {
	stdout, stderr := captureOutput(t, func() {
		if err := printCommandHelp("agentic"); err != nil {
			t.Fatalf("printCommandHelp(agentic) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	mustContain := []string{
		"USAGE",
		"status",
		"task <id>",
		"task --queue-task-id <id>",
		"lineage <task-id>",
	}
	for _, term := range mustContain {
		if !strings.Contains(stdout, term) {
			t.Fatalf("agentic help missing expected term %q; got:\n%s", term, stdout)
		}
	}

	mustNotContain := []string{"approve <id>", "deny <id>", "enqueue <task>", "execute <tool>"}
	for _, term := range mustNotContain {
		if strings.Contains(stdout, term) {
			t.Fatalf("agentic help advertises non-read-only term %q; got:\n%s", term, stdout)
		}
	}
}
