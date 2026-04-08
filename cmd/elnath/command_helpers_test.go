package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/onboarding"
)

func captureOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	os.Stdout = stdoutW
	os.Stderr = stderrW
	fn()
	_ = stdoutW.Close()
	_ = stderrW.Close()

	stdout, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderr, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return string(stdout), string(stderr)
}

func writeTestConfig(t *testing.T, locale onboarding.Locale) string {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	data := "data_dir: " + filepath.Join(dir, "data") + "\n" +
		"wiki_dir: " + filepath.Join(dir, "wiki") + "\n" +
		"locale: " + string(locale) + "\n" +
		"permission:\n  mode: default\n"
	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

func withArgs(t *testing.T, args []string) {
	t.Helper()
	oldArgs := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = oldArgs })
}

func resetLoadLocaleCache() {
	cachedLocale = ""
	cachedLocaleOnce = sync.Once{}
}

func TestCommandRegistryContainsExpectedCommands(t *testing.T) {
	reg := commandRegistry()
	for _, name := range []string{"version", "help", "run", "setup", "daemon", "wiki", "search"} {
		if _, ok := reg[name]; !ok {
			t.Fatalf("missing command %q", name)
		}
	}
}

func TestExecuteCommandAndHelpPaths(t *testing.T) {
	cfgPath := writeTestConfig(t, onboarding.En)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	t.Run("version", func(t *testing.T) {
		stdout, stderr := captureOutput(t, func() {
			if err := executeCommand(context.Background(), "version", nil); err != nil {
				t.Fatalf("executeCommand(version): %v", err)
			}
		})
		if !strings.Contains(stdout, "elnath ") {
			t.Fatalf("stdout = %q, want version output", stdout)
		}
		if stderr != "" {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
	})

	t.Run("help", func(t *testing.T) {
		stdout, _ := captureOutput(t, func() {
			if err := executeCommand(context.Background(), "help", nil); err != nil {
				t.Fatalf("executeCommand(help): %v", err)
			}
		})
		if !strings.Contains(stdout, "Usage: elnath") {
			t.Fatalf("stdout = %q, want help text", stdout)
		}
	})

	t.Run("unknown command falls back to help", func(t *testing.T) {
		stdout, stderr := captureOutput(t, func() {
			if err := executeCommand(context.Background(), "nope", nil); err != nil {
				t.Fatalf("executeCommand(nope): %v", err)
			}
		})
		if !strings.Contains(stderr, "unknown command: nope") {
			t.Fatalf("stderr = %q, want unknown command error", stderr)
		}
		if !strings.Contains(stdout, "Usage: elnath") {
			t.Fatalf("stdout = %q, want help fallback", stdout)
		}
	})
}

func TestLoadLocaleReadsConfigAndCaches(t *testing.T) {
	cfgPath := writeTestConfig(t, onboarding.Ko)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	if got := loadLocale(); got != onboarding.Ko {
		t.Fatalf("loadLocale() = %q, want %q", got, onboarding.Ko)
	}

	// Rewrite the config after first read; cached result should stay stable.
	if err := os.WriteFile(cfgPath, []byte("data_dir: x\nwiki_dir: y\nlocale: en\npermission:\n  mode: default\n"), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}
	if got := loadLocale(); got != onboarding.Ko {
		t.Fatalf("cached loadLocale() = %q, want cached %q", got, onboarding.Ko)
	}
}

func TestFlagHelpers(t *testing.T) {
	args := []string{"elnath", "--config", "cfg.yaml", "--persona", "builder", "--session", "abc", "--continue"}
	if got := extractConfigFlag(args); got != "cfg.yaml" {
		t.Fatalf("extractConfigFlag = %q", got)
	}
	if got := extractPersonaFlag(args); got != "builder" {
		t.Fatalf("extractPersonaFlag = %q", got)
	}
	if got := extractSessionFlag(args); got != "abc" {
		t.Fatalf("extractSessionFlag = %q", got)
	}
	if !hasFlag(args, "--continue") {
		t.Fatal("hasFlag(--continue) = false, want true")
	}
	if hasFlag(args, "--missing") {
		t.Fatal("hasFlag(--missing) = true, want false")
	}
}

func TestHelperBuilders(t *testing.T) {
	reg := buildToolRegistry(t.TempDir())
	for _, name := range []string{"bash", "read_file", "write_file", "edit_file", "glob", "grep", "git", "web_fetch"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("tool registry missing %q", name)
		}
	}

	if parsePermissionMode("accept_edits") != agent.ModeAcceptEdits {
		t.Fatal("parsePermissionMode(accept_edits) mismatch")
	}
	if parsePermissionMode("plan") != agent.ModePlan {
		t.Fatal("parsePermissionMode(plan) mismatch")
	}
	if parsePermissionMode("bypass") != agent.ModeBypass {
		t.Fatal("parsePermissionMode(bypass) mismatch")
	}
	if parsePermissionMode("unknown") != agent.ModeDefault {
		t.Fatal("parsePermissionMode(default) mismatch")
	}

	prompt := defaultSystemPrompt()
	if !strings.Contains(prompt, "autonomous AI assistant") {
		t.Fatalf("defaultSystemPrompt missing assistant text: %q", prompt)
	}

	if got := estimateFiles("touch a.go and b.py plus notes.txt"); got < 2 {
		t.Fatalf("estimateFiles = %d, want >= 2", got)
	}
}

func TestOnboardingResultToConfig(t *testing.T) {
	result := &onboarding.Result{
		APIKey:         "sk-test",
		Locale:         onboarding.Ko,
		DataDir:        "/tmp/data",
		WikiDir:        "/tmp/wiki",
		PermissionMode: "bypass",
		MCPServers: []onboarding.MCPSelection{
			{Name: "Fetch", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-fetch"}},
		},
	}

	cfg := onboardingResultToConfig(result)
	if cfg.Locale != "ko" || cfg.APIKey != "sk-test" || cfg.PermissionMode != "bypass" {
		t.Fatalf("unexpected onboarding config result: %+v", cfg)
	}
	if len(cfg.MCPServers) != 1 || cfg.MCPServers[0].Name != "Fetch" {
		t.Fatalf("unexpected MCP server mapping: %+v", cfg.MCPServers)
	}
}

func TestLoadCodexAuthAndModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}

	auth := map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token": "tok_123",
			"account_id":   "acct_456",
		},
	}
	authRaw, _ := json.Marshal(auth)
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), authRaw, 0o644); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("model = \"gpt-5.4\"\n"), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	token, model, accountID := loadCodexAuth()
	if token != "tok_123" || model != "gpt-5.4" || accountID != "acct_456" {
		t.Fatalf("loadCodexAuth = (%q,%q,%q)", token, model, accountID)
	}
	if got := loadCodexModel(); got != "gpt-5.4" {
		t.Fatalf("loadCodexModel = %q, want gpt-5.4", got)
	}
}

func TestSendIPCRequest(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "ipc.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		var req daemon.IPCRequest
		dec := json.NewDecoder(conn)
		if err := dec.Decode(&req); err != nil {
			return
		}
		resp := daemon.IPCResponse{OK: true, Data: map[string]string{"echo": req.Command}}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	resp, err := sendIPCRequest(socketPath, daemon.IPCRequest{Command: "status"})
	if err != nil {
		t.Fatalf("sendIPCRequest: %v", err)
	}
	if !resp.OK {
		t.Fatalf("response not OK: %+v", resp)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok || data["echo"] != "status" {
		t.Fatalf("unexpected response data: %#v", resp.Data)
	}
	<-done
}

func TestCLIOrchestrationOutputPrints(t *testing.T) {
	stdout, _ := captureOutput(t, func() {
		out := cliOrchestrationOutput()
		out.OnWorkflow(conversation.IntentQuestion, "single")
		out.OnText("hello")
		out.OnUsage("usage summary")
	})
	if !strings.Contains(stdout, "[question → single]") {
		t.Fatalf("stdout missing workflow marker: %q", stdout)
	}
	if !strings.Contains(stdout, "hello") || !strings.Contains(stdout, "usage summary") {
		t.Fatalf("stdout missing text/usage: %q", stdout)
	}
}
