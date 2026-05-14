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
		if entry.Category == "" {
			t.Fatalf("catalog entry %q has empty category", entry.Name)
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

func TestCmdHelpReflectsCommandCatalog(t *testing.T) {
	stdout, stderr := captureOutput(t, func() {
		if err := executeCommand(context.Background(), "help", nil); err != nil {
			t.Fatalf("executeCommand(help) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Usage: elnath <command> [args]") {
		t.Fatalf("help missing usage header:\n%s", stdout)
	}
	for _, entry := range commandCatalog(false) {
		needle := "  " + entry.Name
		if !strings.Contains(stdout, needle) {
			t.Fatalf("help missing registered command %q; got:\n%s", entry.Name, stdout)
		}
	}
	for _, hidden := range []string{"netproxy", "netproxy-bridge", "netproxy-bridge-spike"} {
		if strings.Contains(stdout, "  "+hidden) {
			t.Fatalf("help exposed hidden command %q; got:\n%s", hidden, stdout)
		}
	}
}

func TestExecuteCommand_CommandSpecificHelp(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		want      string
		wantNot   string
		wantError bool
	}{
		{name: "task", command: "task", want: "Usage: elnath task <subcommand>", wantNot: "Run `elnath <command> --help`"},
		{name: "explain", command: "explain", want: "Usage: elnath explain <subcommand>", wantNot: "Run `elnath <command> --help`"},
		{name: "provider", command: "provider", want: "Usage: elnath provider", wantNot: "Run `elnath <command> --help`"},
		{name: "generated fallback", command: "version", want: "Usage: elnath version", wantNot: "Commands:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr := captureOutput(t, func() {
				err := executeCommand(context.Background(), tt.command, []string{"--help"})
				if tt.wantError {
					if err == nil {
						t.Fatalf("executeCommand(%s --help) error = nil, want error", tt.command)
					}
					return
				}
				if err != nil {
					t.Fatalf("executeCommand(%s --help) error = %v", tt.command, err)
				}
			})
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
			if !strings.Contains(stdout, tt.want) {
				t.Fatalf("stdout missing %q:\n%s", tt.want, stdout)
			}
			if tt.wantNot != "" && strings.Contains(stdout, tt.wantNot) {
				t.Fatalf("stdout contains %q, want command-specific help:\n%s", tt.wantNot, stdout)
			}
		})
	}
}

func TestExecuteCommand_SubcommandHelpCoverage(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{name: "eval", command: "eval", want: []string{"Usage: elnath eval <subcommand>", "validate <corpus.json>", "run-current <plan.json>"}},
		{name: "skill", command: "skill", want: []string{"Usage: elnath skill <subcommand>", "list [--json]", "create <name>"}},
		{name: "profile", command: "profile", want: []string{"Usage: elnath profile <subcommand>", "list", "show <name>"}},
		{name: "telegram", command: "telegram", want: []string{"Usage: elnath telegram <subcommand>", "shell"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr := captureOutput(t, func() {
				if err := executeCommand(context.Background(), tt.command, []string{"--help"}); err != nil {
					t.Fatalf("executeCommand(%s --help) error = %v", tt.command, err)
				}
			})
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
			for _, want := range tt.want {
				if !strings.Contains(stdout, want) {
					t.Fatalf("stdout missing %q:\n%s", want, stdout)
				}
			}
			if strings.Contains(stdout, "Run `elnath <command> --help`") {
				t.Fatalf("stdout fell back to top-level help:\n%s", stdout)
			}
		})
	}
}

func TestVisibleCommandsHaveCommandSpecificHelp(t *testing.T) {
	for _, entry := range commandCatalog(false) {
		t.Run(entry.Name, func(t *testing.T) {
			stdout, stderr := captureOutput(t, func() {
				if err := executeCommand(context.Background(), entry.Name, []string{"--help"}); err != nil {
					t.Fatalf("executeCommand(%s --help) error = %v", entry.Name, err)
				}
			})
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
			if strings.Contains(stdout, "Run `elnath <command> --help`") {
				t.Fatalf("help for %q fell back to top-level help:\n%s", entry.Name, stdout)
			}
			if !strings.Contains(stdout, "Usage: elnath "+entry.Name) && !strings.Contains(stdout, "USAGE") {
				t.Fatalf("help for %q missing command-specific usage:\n%s", entry.Name, stdout)
			}
		})
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

func TestPrintCommandHelp_DoctorMatchesDispatcher(t *testing.T) {
	stdout, stderr := captureOutput(t, func() {
		if err := printCommandHelp("doctor"); err != nil {
			t.Fatalf("printCommandHelp(doctor) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "elnath doctor [--json]") {
		t.Fatalf("stdout = %q, want doctor usage", stdout)
	}
}
