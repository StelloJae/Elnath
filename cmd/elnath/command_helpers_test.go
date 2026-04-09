package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

func writeDaemonTestConfig(t *testing.T, locale onboarding.Locale, socketPath string) string {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	data := "data_dir: " + filepath.Join(dir, "data") + "\n" +
		"wiki_dir: " + filepath.Join(dir, "wiki") + "\n" +
		"locale: " + string(locale) + "\n" +
		"permission:\n  mode: default\n" +
		"daemon:\n  socket_path: " + socketPath + "\n"
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
	for _, name := range []string{"version", "help", "run", "setup", "daemon", "wiki", "search", "eval"} {
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

	ctx := buildRoutingContext("fix regression in existing handler and add tests for middleware.go")
	if !ctx.ExistingCode {
		t.Fatal("expected ExistingCode = true")
	}
	if !ctx.VerificationHint {
		t.Fatal("expected VerificationHint = true")
	}
	if ctx.EstimatedFiles < 2 {
		t.Fatalf("expected EstimatedFiles >= 2 for brownfield verification task, got %d", ctx.EstimatedFiles)
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

func TestCmdDaemonStatusRendersStructuredProgressEnvelope(t *testing.T) {
	socketPath := filepath.Join("/tmp", fmt.Sprintf("elnath-test-%d.sock", time.Now().UnixNano()))
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()
	t.Cleanup(func() { _ = os.Remove(socketPath) })

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
		resp := daemon.IPCResponse{
			OK: true,
			Data: map[string]any{
				"tasks": []map[string]any{{
					"id":         1,
					"status":     "running",
					"payload":    "analyze project structure",
					"session_id": "sess-1234567890abcdef",
					"progress": daemon.EncodeProgressEvent(daemon.ProgressEvent{
						Kind:     daemon.ProgressKindWorkflow,
						Message:  "question → single",
						Intent:   "question",
						Workflow: "single",
					}),
					"summary": "latest summary",
				}},
			},
		}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, stderr := captureOutput(t, func() {
		if err := cmdDaemonStatus(context.Background()); err != nil {
			t.Fatalf("cmdDaemonStatus: %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "question → single") {
		t.Fatalf("stdout = %q, want rendered progress message", stdout)
	}
	if strings.Contains(stdout, "\"kind\"") || strings.Contains(stdout, "\"version\"") {
		t.Fatalf("stdout = %q, want human-readable progress instead of raw JSON", stdout)
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

func TestCmdEval(t *testing.T) {
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "corpus.json")
	scorecardPath := filepath.Join(dir, "scorecard.json")
	if err := os.WriteFile(scorecardPath, []byte(`{"version":"v1","system":"elnath","baseline":"claude+omx","results":[{"task_id":"BF-001","track":"brownfield_feature","language":"go","success":true,"intervention_count":1,"intervention_needed":true,"intervention_class":"necessary","verification_passed":true,"failure_family":"repo_context_miss","recovery_attempted":true,"recovery_succeeded":true,"duration_seconds":2}]}`), 0o644); err != nil {
		t.Fatalf("write scorecard: %v", err)
	}
	corpusPath = filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`{"version":"v1","tasks":[{"id":"BF-001","title":"task","track":"brownfield_feature","language":"go","repo_class":"cli_dev_tool","benchmark_family":"brownfield_primary","prompt":"do it","repo":"https://github.com/example/repo","repo_ref":"deadbeef","acceptance_criteria":["tests pass"]}]}`), 0o644); err != nil {
		t.Fatalf("rewrite corpus: %v", err)
	}
	scorecardPath = filepath.Join(dir, "scorecard.json")
	if err := os.WriteFile(scorecardPath, []byte(`{"version":"v1","system":"elnath","baseline":"claude+omx","context":"benchmark","runtime_policy":"sandbox=workspace-write, approvals=never","repeated_runs":1,"intervention_notes":true,"results":[{"task_id":"BF-001","track":"brownfield_feature","language":"go","success":true,"intervention_count":1,"intervention_needed":true,"intervention_class":"necessary","verification_passed":true,"failure_family":"repo_context_miss","recovery_attempted":true,"recovery_succeeded":true,"duration_seconds":2}]}`), 0o644); err != nil {
		t.Fatalf("rewrite scorecard: %v", err)
	}

	t.Run("usage", func(t *testing.T) {
		stdout, _ := captureOutput(t, func() {
			if err := cmdEval(context.Background(), nil); err != nil {
				t.Fatalf("cmdEval usage: %v", err)
			}
		})
		if !strings.Contains(stdout, "Usage: elnath eval") {
			t.Fatalf("stdout = %q, want usage", stdout)
		}
	})

	t.Run("validate", func(t *testing.T) {
		stdout, _ := captureOutput(t, func() {
			if err := cmdEval(context.Background(), []string{"validate", corpusPath}); err != nil {
				t.Fatalf("cmdEval validate: %v", err)
			}
		})
		if !strings.Contains(stdout, "Corpus OK") {
			t.Fatalf("stdout = %q, want Corpus OK", stdout)
		}
	})

	t.Run("summarize", func(t *testing.T) {
		stdout, _ := captureOutput(t, func() {
			if err := cmdEval(context.Background(), []string{"summarize", scorecardPath}); err != nil {
				t.Fatalf("cmdEval summarize: %v", err)
			}
		})
		if !strings.Contains(stdout, "Overall: total=1 success=1") {
			t.Fatalf("stdout = %q, want overall summary", stdout)
		}
		if !strings.Contains(stdout, "Track brownfield_feature") {
			t.Fatalf("stdout = %q, want track summary", stdout)
		}
	})

	t.Run("diff", func(t *testing.T) {
		baselinePath := filepath.Join(dir, "baseline.json")
		if err := os.WriteFile(baselinePath, []byte(`{"version":"v1","system":"baseline","results":[{"task_id":"BF-001","track":"brownfield_feature","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"verification_passed":false,"failure_family":"weak_verification_path","recovery_attempted":true,"recovery_succeeded":false,"duration_seconds":3}]}`), 0o644); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
		stdout, _ := captureOutput(t, func() {
			if err := cmdEval(context.Background(), []string{"diff", scorecardPath, baselinePath}); err != nil {
				t.Fatalf("cmdEval diff: %v", err)
			}
		})
		if !strings.Contains(stdout, "Overall delta") || !strings.Contains(stdout, "verification_pass_delta") {
			t.Fatalf("stdout = %q, want diff summary", stdout)
		}
	})

	t.Run("rules ok", func(t *testing.T) {
		stdout, _ := captureOutput(t, func() {
			if err := cmdEval(context.Background(), []string{"rules", corpusPath, scorecardPath}); err != nil {
				t.Fatalf("cmdEval rules: %v", err)
			}
		})
		if !strings.Contains(stdout, "Anti-vanity rules OK") {
			t.Fatalf("stdout = %q, want OK", stdout)
		}
	})

	t.Run("rules fail", func(t *testing.T) {
		badScorecardPath := filepath.Join(dir, "bad-scorecard.json")
		if err := os.WriteFile(badScorecardPath, []byte(`{"version":"v1","system":"elnath","context":"launch","repeated_runs":0,"intervention_notes":false,"results":[{"task_id":"BF-001","track":"brownfield_feature","language":"go","success":true,"intervention_count":1,"intervention_needed":true,"intervention_class":"late","duration_seconds":2}]}`), 0o644); err != nil {
			t.Fatalf("write bad scorecard: %v", err)
		}
		stdout, _ := captureOutput(t, func() {
			err := cmdEval(context.Background(), []string{"rules", corpusPath, badScorecardPath})
			if err == nil {
				t.Fatalf("expected rules failure")
			}
		})
		if !strings.Contains(stdout, "hidden_human_rescue") && !strings.Contains(stdout, "one_shot_launch_claim") && !strings.Contains(stdout, "repeated_runs_required") {
			t.Fatalf("stdout = %q, want rule violations", stdout)
		}
	})

	t.Run("report", func(t *testing.T) {
		baselinePath := filepath.Join(dir, "baseline-report.json")
		if err := os.WriteFile(baselinePath, []byte(`{"version":"v1","system":"baseline","results":[{"task_id":"BF-001","track":"brownfield_feature","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"verification_passed":false,"failure_family":"weak_verification_path","recovery_attempted":true,"recovery_succeeded":false,"duration_seconds":3}]}`), 0o644); err != nil {
			t.Fatalf("write baseline report: %v", err)
		}
		reportPath := filepath.Join(dir, "benchmark-report.md")
		stdout, _ := captureOutput(t, func() {
			if err := cmdEval(context.Background(), []string{"report", corpusPath, scorecardPath, baselinePath, reportPath}); err != nil {
				t.Fatalf("cmdEval report: %v", err)
			}
		})
		if !strings.Contains(stdout, "Benchmark report written") {
			t.Fatalf("stdout = %q, want report message", stdout)
		}
		report, err := os.ReadFile(reportPath)
		if err != nil {
			t.Fatalf("read report: %v", err)
		}
		if !strings.Contains(string(report), "Repo Class Summary") {
			t.Fatalf("unexpected report contents: %s", string(report))
		}
	})

	t.Run("gate-month2 pass", func(t *testing.T) {
		gateCorpusPath := filepath.Join(dir, "gate-corpus.json")
		if err := os.WriteFile(gateCorpusPath, []byte(`{"version":"v1","tasks":[
{"id":"BF-001","title":"task","track":"brownfield_feature","language":"go","repo_class":"cli_dev_tool","benchmark_family":"brownfield_primary","prompt":"do it","repo":"https://github.com/example/repo","repo_ref":"deadbeef","acceptance_criteria":["tests pass"]},
{"id":"BUG-001","title":"holdout","track":"bugfix","language":"go","repo_class":"service_backend","benchmark_family":"brownfield_holdout","holdout":true,"prompt":"fix it","repo":"https://github.com/example/repo2","repo_ref":"feedface","acceptance_criteria":["tests pass"]}
]}`), 0o644); err != nil {
			t.Fatalf("write gate corpus: %v", err)
		}
		baselinePath := filepath.Join(dir, "baseline-gate.json")
		if err := os.WriteFile(baselinePath, []byte(`{"version":"v1","system":"baseline","results":[{"task_id":"BF-001","track":"brownfield_feature","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"verification_passed":false,"duration_seconds":3},{"task_id":"BUG-001","track":"bugfix","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"verification_passed":false,"duration_seconds":1}]}`), 0o644); err != nil {
			t.Fatalf("write baseline gate: %v", err)
		}
		currentGatePath := filepath.Join(dir, "current-gate.json")
		if err := os.WriteFile(currentGatePath, []byte(`{"version":"v1","system":"elnath","results":[{"task_id":"BF-001","track":"brownfield_feature","language":"go","success":true,"intervention_count":1,"intervention_needed":true,"intervention_class":"necessary","verification_passed":true,"failure_family":"repo_context_miss","duration_seconds":2},{"task_id":"BUG-001","track":"bugfix","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"verification_passed":true,"failure_family":"weak_verification_path","duration_seconds":1}]}`), 0o644); err != nil {
			t.Fatalf("write current gate: %v", err)
		}
		stdout, _ := captureOutput(t, func() {
			if err := cmdEval(context.Background(), []string{"gate-month2", gateCorpusPath, currentGatePath, baselinePath}); err != nil {
				t.Fatalf("cmdEval gate-month2: %v", err)
			}
		})
		if !strings.Contains(stdout, "Month 2 gate: PASS") {
			t.Fatalf("stdout = %q, want gate pass", stdout)
		}
	})

	t.Run("scaffold-baseline", func(t *testing.T) {
		outputPath := filepath.Join(dir, "baseline-scaffold.json")
		stdout, _ := captureOutput(t, func() {
			if err := cmdEval(context.Background(), []string{"scaffold-baseline", outputPath}); err != nil {
				t.Fatalf("cmdEval scaffold-baseline: %v", err)
			}
		})
		if !strings.Contains(stdout, "Baseline scaffold written") {
			t.Fatalf("stdout = %q, want scaffold message", stdout)
		}
		data, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("read scaffold: %v", err)
		}
		if !strings.Contains(string(data), `"baseline": "claude-codex-omx-omc"`) || !strings.Contains(string(data), `"runtime_policy": ""`) {
			t.Fatalf("unexpected scaffold content: %s", string(data))
		}
	})

	t.Run("scaffold-current", func(t *testing.T) {
		outputPath := filepath.Join(dir, "current-scaffold.json")
		stdout, _ := captureOutput(t, func() {
			if err := cmdEval(context.Background(), []string{"scaffold-current", outputPath}); err != nil {
				t.Fatalf("cmdEval scaffold-current: %v", err)
			}
		})
		if !strings.Contains(stdout, "Current scaffold written") {
			t.Fatalf("stdout = %q, want scaffold message", stdout)
		}
		data, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("read scaffold: %v", err)
		}
		if !strings.Contains(string(data), `"system": "elnath-current"`) || !strings.Contains(string(data), `"runtime_policy": ""`) {
			t.Fatalf("unexpected current scaffold content: %s", string(data))
		}
	})

	t.Run("run-baseline", func(t *testing.T) {
		wrapperPath := filepath.Join(dir, "baseline-wrapper.sh")
		wrapper := `#!/bin/sh
out="$1"
task_id="$2"
track="$3"
language="$4"
cat > "$out" <<EOF
{"task_id":"$task_id","track":"$track","language":"$language","success":true,"intervention_count":0,"intervention_needed":false,"duration_seconds":1}
EOF
`
		if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
			t.Fatalf("write wrapper: %v", err)
		}
		t.Setenv("BASELINE_BIN", wrapperPath)
		planPath := filepath.Join(dir, "baseline-plan.json")
		if err := os.WriteFile(planPath, []byte(`{
  "version":"v1",
  "baseline":"claude-codex-omx-omc",
  "corpus_path":"`+corpusPath+`",
  "command_template":"\"$BASELINE_BIN\" {{task_output}} {{task_id}} {{task_track}} {{task_language}}",
  "output_path":"`+filepath.Join(dir, "baseline-scorecard.json")+`",
  "context":"benchmark",
  "repeated_runs":2,
  "required_env":["BASELINE_BIN"]
}`), 0o644); err != nil {
			t.Fatalf("write plan: %v", err)
		}
		stdout, _ := captureOutput(t, func() {
			if err := cmdEval(context.Background(), []string{"run-baseline", planPath}); err != nil {
				t.Fatalf("cmdEval run-baseline: %v", err)
			}
		})
		if !strings.Contains(stdout, "Baseline run complete") {
			t.Fatalf("stdout = %q, want run-baseline summary", stdout)
		}
	})

	t.Run("run-current", func(t *testing.T) {
		wrapperPath := filepath.Join(dir, "current-wrapper.sh")
		wrapper := `#!/bin/sh
out="$1"
task_id="$2"
track="$3"
language="$4"
cat > "$out" <<EOF
{"task_id":"$task_id","track":"$track","language":"$language","success":true,"intervention_count":1,"intervention_needed":true,"intervention_class":"necessary","verification_passed":true,"failure_family":"repo_context_miss","recovery_attempted":true,"recovery_succeeded":true,"duration_seconds":1}
EOF
`
		if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
			t.Fatalf("write wrapper: %v", err)
		}
		t.Setenv("CURRENT_BIN", wrapperPath)
		planPath := filepath.Join(dir, "current-plan.json")
		if err := os.WriteFile(planPath, []byte(`{
  "version":"v1",
  "system":"elnath-current",
  "baseline":"self",
  "corpus_path":"`+corpusPath+`",
  "command_template":"\"$CURRENT_BIN\" {{task_output}} {{task_id}} {{task_track}} {{task_language}}",
  "output_path":"`+filepath.Join(dir, "current-scorecard.json")+`",
  "context":"benchmark",
  "runtime_policy":"sandbox=workspace-write, approvals=never",
  "repeated_runs":1,
  "intervention_notes":true,
  "required_env":["CURRENT_BIN"]
}`), 0o644); err != nil {
			t.Fatalf("write current plan: %v", err)
		}
		stdout, _ := captureOutput(t, func() {
			if err := cmdEval(context.Background(), []string{"run-current", planPath}); err != nil {
				t.Fatalf("cmdEval run-current: %v", err)
			}
		})
		if !strings.Contains(stdout, "Current run complete") {
			t.Fatalf("stdout = %q, want run-current summary", stdout)
		}
		data, err := os.ReadFile(filepath.Join(dir, "current-scorecard.json"))
		if err != nil {
			t.Fatalf("read current scorecard: %v", err)
		}
		if !strings.Contains(string(data), `"runtime_policy": "sandbox=workspace-write, approvals=never"`) {
			t.Fatalf("current scorecard missing runtime_policy: %s", string(data))
		}
	})
}
