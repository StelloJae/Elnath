package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/onboarding"
)

func TestSandboxPrintStarterAllowlist_RequiresExplicitGroupForActiveSnippet(t *testing.T) {
	stdout, stderr, err := runSandboxCommand(t, "print-starter-allowlist")
	if err != nil {
		t.Fatalf("sandbox print-starter-allowlist error = %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Available starter allowlist groups") {
		t.Fatalf("stdout = %q, want group catalog", stdout)
	}
	if strings.Contains(stdout, "sandbox:\n") || strings.Contains(stdout, "network_allowlist:") {
		t.Fatalf("stdout printed active YAML without --group:\n%s", stdout)
	}
	if strings.Contains(stdout, "registry-1.docker.io:443") || strings.Contains(stdout, "auth.docker.io:443") {
		t.Fatalf("stdout exposed containers endpoints without explicit containers group:\n%s", stdout)
	}
}

func TestSandboxPrintStarterAllowlist_PrintsDeterministicYAML(t *testing.T) {
	stdout, stderr, err := runSandboxCommand(t, "print-starter-allowlist", "--mode", "seatbelt", "--group", "git-hosting,go")
	if err != nil {
		t.Fatalf("sandbox print-starter-allowlist error = %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	want := `sandbox:
  mode: seatbelt
  network_allowlist:
    # git-hosting
    - github.com:443
    - gitlab.com:443
    - bitbucket.org:443
    # go
    - proxy.golang.org:443
    - sum.golang.org:443

# Notes:
# - Network allowlist changes require Elnath restart.
# - UDP and QUIC egress are blocked in this sandbox version.
# - DNS rebinding is still not fully defended. Sustained DNS hijack or malicious DNS responses at policy-resolution time remain in scope. If hostile DNS is in scope, enforce egress at a lower layer.
`
	if stdout != want {
		t.Fatalf("stdout mismatch\nwant:\n%s\ngot:\n%s", want, stdout)
	}
}

func TestSandboxPrintStarterAllowlist_RequiresModeForCopyPasteSnippet(t *testing.T) {
	_, _, err := runSandboxCommand(t, "print-starter-allowlist", "--group", "git-hosting")
	if err == nil || !strings.Contains(err.Error(), "requires --mode seatbelt or --mode bwrap when --group is provided") {
		t.Fatalf("error = %v, want missing mode error", err)
	}
}

func TestSandboxPrintStarterAllowlist_RejectsUnknownGroup(t *testing.T) {
	_, _, err := runSandboxCommand(t, "print-starter-allowlist", "--mode", "seatbelt", "--group", "ferret")
	if err == nil || !strings.Contains(err.Error(), "unknown starter allowlist group") {
		t.Fatalf("error = %v, want unknown group error", err)
	}
}

func TestSandboxPrintStarterAllowlist_RejectsUnknownMode(t *testing.T) {
	_, _, err := runSandboxCommand(t, "print-starter-allowlist", "--mode", "direct", "--group", "git-hosting")
	if err == nil || !strings.Contains(err.Error(), "unknown sandbox mode") {
		t.Fatalf("error = %v, want unknown mode error", err)
	}
}

func TestSandboxPrintStarterAllowlist_DeduplicatesStableOrder(t *testing.T) {
	stdout, _, err := runSandboxCommand(t, "print-starter-allowlist", "--mode", "bwrap", "--group", "go,git-hosting,go")
	if err != nil {
		t.Fatalf("sandbox print-starter-allowlist error = %v", err)
	}
	wantOrder := []string{"# go", "- proxy.golang.org:443", "- sum.golang.org:443", "# git-hosting", "- github.com:443"}
	last := -1
	for _, needle := range wantOrder {
		idx := strings.Index(stdout, needle)
		if idx <= last {
			t.Fatalf("%q order mismatch in output:\n%s", needle, stdout)
		}
		last = idx
	}
	if strings.Count(stdout, "# go") != 1 || strings.Count(stdout, "proxy.golang.org:443") != 1 {
		t.Fatalf("stdout did not de-duplicate repeated go group:\n%s", stdout)
	}
}

func TestSandboxPrintStarterAllowlist_DeduplicatesEntriesStableOrder(t *testing.T) {
	stdout, stderr := captureOutput(t, func() {
		printStarterAllowlistYAML("seatbelt", []starterAllowlistGroup{
			{name: "alpha", entries: []string{"shared.example.com:443", "alpha.example.com:443"}},
			{name: "bravo", entries: []string{"shared.example.com:443", "bravo.example.com:443"}},
		})
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if strings.Count(stdout, "shared.example.com:443") != 1 {
		t.Fatalf("shared entry was not de-duplicated:\n%s", stdout)
	}
	wantOrder := []string{"# alpha", "- shared.example.com:443", "- alpha.example.com:443", "# bravo", "- bravo.example.com:443"}
	last := -1
	for _, needle := range wantOrder {
		idx := strings.Index(stdout, needle)
		if idx <= last {
			t.Fatalf("%q order mismatch in output:\n%s", needle, stdout)
		}
		last = idx
	}
}

func TestSandboxPrintStarterAllowlist_ContainersOnlyWhenRequested(t *testing.T) {
	stdout, _, err := runSandboxCommand(t, "print-starter-allowlist", "--mode", "seatbelt", "--group", "node")
	if err != nil {
		t.Fatalf("sandbox print-starter-allowlist node error = %v", err)
	}
	if strings.Contains(stdout, "registry-1.docker.io:443") || strings.Contains(stdout, "auth.docker.io:443") {
		t.Fatalf("containers entries appeared without explicit containers group:\n%s", stdout)
	}

	stdout, _, err = runSandboxCommand(t, "print-starter-allowlist", "--mode", "seatbelt", "--group", "containers")
	if err != nil {
		t.Fatalf("sandbox print-starter-allowlist containers error = %v", err)
	}
	if !strings.Contains(stdout, "registry-1.docker.io:443") || !strings.Contains(stdout, "auth.docker.io:443") {
		t.Fatalf("containers entries missing when explicitly requested:\n%s", stdout)
	}
}

func TestSandboxPrintStarterAllowlist_DoesNotWriteConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".elnath")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	original := []byte("data_dir: /tmp/elnath-data\nsandbox:\n  mode: seatbelt\n")
	if err := os.WriteFile(cfgPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := runSandboxCommand(t, "print-starter-allowlist", "--mode", "seatbelt", "--group", "python")
	if err != nil {
		t.Fatalf("sandbox print-starter-allowlist error = %v", err)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("config changed\nwant:\n%s\ngot:\n%s", original, after)
	}
}

func TestSandboxPrintStarterAllowlist_NoProxyEnabled(t *testing.T) {
	stdout, _, err := runSandboxCommand(t, "print-starter-allowlist", "--mode", "seatbelt", "--group", "git-hosting,python,node,go,rust,containers")
	if err != nil {
		t.Fatalf("sandbox print-starter-allowlist error = %v", err)
	}
	if strings.Contains(stdout, "ProxyEnabled") || strings.Contains(stdout, "proxy_enabled") {
		t.Fatalf("stdout contains ProxyEnabled-shaped field:\n%s", stdout)
	}
}

func TestSandboxPrintStarterAllowlist_IncludesDNSCaveat(t *testing.T) {
	stdout, _, err := runSandboxCommand(t, "print-starter-allowlist", "--mode", "bwrap", "--group", "rust")
	if err != nil {
		t.Fatalf("sandbox print-starter-allowlist error = %v", err)
	}
	for _, want := range []string{
		"DNS rebinding is still not fully defended",
		"Sustained DNS hijack or malicious DNS responses at policy-resolution time remain in scope",
		"If hostile DNS is in scope, enforce egress at a lower layer",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestSandboxCommand_RegisteredAndHelped(t *testing.T) {
	if _, ok := commandRegistry()["sandbox"]; !ok {
		t.Fatal("commandRegistry missing sandbox")
	}
	stdout, stderr := captureOutput(t, func() {
		if err := printCommandHelp("sandbox"); err != nil {
			t.Fatalf("printCommandHelp(sandbox) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	for _, want := range []string{"print-starter-allowlist", "--list-groups", "--group", "--mode"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("sandbox help missing %q:\n%s", want, stdout)
		}
	}
}

func TestSandboxCommand_KoreanHelpShowsSandboxHelp(t *testing.T) {
	cfgPath := writeTestConfig(t, onboarding.Ko)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, stderr := captureOutput(t, func() {
		if err := printCommandHelp("sandbox"); err != nil {
			t.Fatalf("printCommandHelp(sandbox) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "print-starter-allowlist") || !strings.Contains(stdout, "--list-groups") {
		t.Fatalf("Korean locale sandbox help did not show sandbox command help:\n%s", stdout)
	}
	if strings.Contains(stdout, "사용법: elnath <명령>") {
		t.Fatalf("Korean locale sandbox help fell back to root help:\n%s", stdout)
	}
}

func runSandboxCommand(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var runErr error
	stdout, stderr := captureOutput(t, func() {
		runErr = executeCommand(context.Background(), "sandbox", args)
	})
	return stdout, stderr, runErr
}
