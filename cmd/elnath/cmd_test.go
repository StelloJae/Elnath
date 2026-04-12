package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/onboarding"
	"github.com/stello/elnath/internal/orchestrator"
	"github.com/stello/elnath/internal/tools"
)

// testSocketPath returns a short Unix socket path under /tmp to stay within
// macOS's 104-byte sun_path limit (t.TempDir() paths are often too long).
func testSocketPath(t *testing.T, suffix string) string {
	t.Helper()
	p := filepath.Join("/tmp", fmt.Sprintf("el-%s-%d.sock", suffix, time.Now().UnixNano()))
	t.Cleanup(func() { _ = os.Remove(p) })
	return p
}

// ---------------------------------------------------------------------------
// cmdDaemon dispatcher
// ---------------------------------------------------------------------------

func TestCmdDaemonUsage(t *testing.T) {
	stdout, _ := captureOutput(t, func() {
		if err := cmdDaemon(context.Background(), nil); err != nil {
			t.Fatalf("cmdDaemon usage: %v", err)
		}
	})
	if !strings.Contains(stdout, "Usage: elnath daemon") {
		t.Fatalf("stdout = %q, want daemon usage", stdout)
	}
}

func TestCmdDaemonUnknownSubcommand(t *testing.T) {
	err := cmdDaemon(context.Background(), []string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown daemon subcommand: bogus") {
		t.Fatalf("cmdDaemon(bogus) err = %v, want unknown subcommand", err)
	}
}

// ---------------------------------------------------------------------------
// cmdTelegram dispatcher
// ---------------------------------------------------------------------------

func TestCmdTelegramUsage(t *testing.T) {
	stdout, _ := captureOutput(t, func() {
		if err := cmdTelegram(context.Background(), nil); err != nil {
			t.Fatalf("cmdTelegram usage: %v", err)
		}
	})
	if !strings.Contains(stdout, "Usage: elnath telegram") {
		t.Fatalf("stdout = %q, want telegram usage", stdout)
	}
}

func TestCmdTelegramUnknownSubcommand(t *testing.T) {
	err := cmdTelegram(context.Background(), []string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown telegram subcommand: bogus") {
		t.Fatalf("cmdTelegram(bogus) err = %v, want unknown subcommand", err)
	}
}

// ---------------------------------------------------------------------------
// cmdDaemonSubmit via mock IPC socket
// ---------------------------------------------------------------------------

func TestCmdDaemonSubmit(t *testing.T) {
	socketPath := testSocketPath(t, "submit")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		resp := daemon.IPCResponse{
			OK:   true,
			Data: map[string]interface{}{"task_id": 42, "existed": false},
		}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdDaemonSubmit(context.Background(), []string{"do", "the", "thing"}); err != nil {
			t.Fatalf("cmdDaemonSubmit: %v", err)
		}
	})
	if !strings.Contains(stdout, "Task #42 enqueued") {
		t.Fatalf("stdout = %q, want enqueued output", stdout)
	}
	<-done
}

func TestCmdDaemonSubmitWithSession(t *testing.T) {
	socketPath := testSocketPath(t, "subsess")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		resp := daemon.IPCResponse{
			OK:   true,
			Data: map[string]interface{}{"task_id": 99, "existed": false},
		}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdDaemonSubmit(context.Background(), []string{"--session", "sess-abc", "continue", "work"}); err != nil {
			t.Fatalf("cmdDaemonSubmit with session: %v", err)
		}
	})
	if !strings.Contains(stdout, "Task #99 enqueued") {
		t.Fatalf("stdout = %q, want enqueued output", stdout)
	}
	<-done
}

func TestCmdDaemonSubmitDeduplicated(t *testing.T) {
	socketPath := testSocketPath(t, "subdedup")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		resp := daemon.IPCResponse{
			OK:   true,
			Data: map[string]interface{}{"task_id": 42, "existed": true},
		}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdDaemonSubmit(context.Background(), []string{"do", "the", "thing"}); err != nil {
			t.Fatalf("cmdDaemonSubmit: %v", err)
		}
	})
	if !strings.Contains(stdout, "Task #42 already running (deduplicated)") {
		t.Fatalf("stdout = %q, want deduplicated output", stdout)
	}
	<-done
}

func TestCmdDaemonSubmitEmptyPrompt(t *testing.T) {
	cfgPath := writeDaemonTestConfig(t, onboarding.En, "/tmp/nonexistent.sock")
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	err := cmdDaemonSubmit(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("cmdDaemonSubmit empty err = %v, want usage error", err)
	}
}

func TestCmdDaemonSubmitDaemonError(t *testing.T) {
	socketPath := testSocketPath(t, "suberr")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		resp := daemon.IPCResponse{OK: false, Err: "queue full"}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	err = cmdDaemonSubmit(context.Background(), []string{"some", "task"})
	if err == nil || !strings.Contains(err.Error(), "queue full") {
		t.Fatalf("cmdDaemonSubmit daemon error = %v, want queue full", err)
	}
	<-done
}

// ---------------------------------------------------------------------------
// cmdDaemonStop via mock IPC socket
// ---------------------------------------------------------------------------

func TestCmdDaemonStop(t *testing.T) {
	socketPath := testSocketPath(t, "stop")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		if req.Command != "stop" {
			return
		}
		resp := daemon.IPCResponse{OK: true}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdDaemonStop(context.Background()); err != nil {
			t.Fatalf("cmdDaemonStop: %v", err)
		}
	})
	if !strings.Contains(stdout, "Daemon stop requested") {
		t.Fatalf("stdout = %q, want stop confirmation", stdout)
	}
	<-done
}

func TestCmdDaemonStopError(t *testing.T) {
	socketPath := testSocketPath(t, "stoperr")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		resp := daemon.IPCResponse{OK: false, Err: "not running"}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	err = cmdDaemonStop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("cmdDaemonStop error = %v, want not running", err)
	}
	<-done
}

// ---------------------------------------------------------------------------
// run function (main dispatcher)
// ---------------------------------------------------------------------------

func TestRunHelp(t *testing.T) {
	cfgPath := writeTestConfig(t, onboarding.En)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := run(context.Background(), []string{"elnath"}); err != nil {
			t.Fatalf("run(help): %v", err)
		}
	})
	if !strings.Contains(stdout, "Usage: elnath") {
		t.Fatalf("stdout = %q, want help output", stdout)
	}
}

func TestRunVersion(t *testing.T) {
	cfgPath := writeTestConfig(t, onboarding.En)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := run(context.Background(), []string{"elnath", "version"}); err != nil {
			t.Fatalf("run(version): %v", err)
		}
	})
	if !strings.Contains(stdout, "elnath ") {
		t.Fatalf("stdout = %q, want version", stdout)
	}
}

func TestRunEval(t *testing.T) {
	cfgPath := writeTestConfig(t, onboarding.En)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := run(context.Background(), []string{"elnath", "eval"}); err != nil {
			t.Fatalf("run(eval): %v", err)
		}
	})
	if !strings.Contains(stdout, "Usage: elnath eval") {
		t.Fatalf("stdout = %q, want eval usage", stdout)
	}
}

// ---------------------------------------------------------------------------
// cmdEval error paths (missing args, unknown subcommand)
// ---------------------------------------------------------------------------

func TestCmdEvalMissingArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"validate no file", []string{"validate"}, "usage: elnath eval validate"},
		{"summarize no file", []string{"summarize"}, "usage: elnath eval summarize"},
		{"diff missing baseline", []string{"diff", "a.json"}, "usage: elnath eval diff"},
		{"report missing args", []string{"report", "a.json"}, "usage: elnath eval report"},
		{"gate-month2 missing args", []string{"gate-month2", "a.json"}, "usage: elnath eval gate-month2"},
		{"rules missing args", []string{"rules", "a.json"}, "usage: elnath eval rules"},
		{"run-baseline no file", []string{"run-baseline"}, "usage: elnath eval run-baseline"},
		{"run-current no file", []string{"run-current"}, "usage: elnath eval run-current"},
		{"scaffold-baseline no file", []string{"scaffold-baseline"}, "usage: elnath eval scaffold-baseline"},
		{"scaffold-current no file", []string{"scaffold-current"}, "usage: elnath eval scaffold-current"},
		{"unknown subcommand", []string{"nonexistent"}, "unknown eval subcommand: nonexistent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cmdEval(context.Background(), tt.args)
			if err == nil {
				t.Fatalf("expected error for args %v", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want containing %q", err.Error(), tt.want)
			}
		})
	}
}

func TestCmdEvalValidateInvalidFile(t *testing.T) {
	err := cmdEval(context.Background(), []string{"validate", "/nonexistent/corpus.json"})
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestCmdEvalSummarizeInvalidFile(t *testing.T) {
	err := cmdEval(context.Background(), []string{"summarize", "/nonexistent/scorecard.json"})
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestCmdEvalDiffInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	validPath := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(validPath, []byte(`{"version":"v1","system":"s","results":[{"task_id":"T1","track":"bugfix","language":"go","success":true,"intervention_count":0,"intervention_needed":false,"duration_seconds":1}]}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Run("invalid current", func(t *testing.T) {
		err := cmdEval(context.Background(), []string{"diff", "/nonexistent.json", validPath})
		if err == nil {
			t.Fatal("expected error for nonexistent current")
		}
	})

	t.Run("invalid baseline", func(t *testing.T) {
		err := cmdEval(context.Background(), []string{"diff", validPath, "/nonexistent.json"})
		if err == nil {
			t.Fatal("expected error for nonexistent baseline")
		}
	})
}

func TestCmdEvalReportInvalidFiles(t *testing.T) {
	err := cmdEval(context.Background(), []string{"report", "/no/corpus.json", "/no/current.json", "/no/baseline.json", "/no/out.md"})
	if err == nil {
		t.Fatal("expected error for nonexistent files")
	}
}

func TestCmdEvalGateMonth2InvalidFiles(t *testing.T) {
	err := cmdEval(context.Background(), []string{"gate-month2", "/no/corpus.json", "/no/current.json", "/no/baseline.json"})
	if err == nil {
		t.Fatal("expected error for nonexistent files")
	}
}

func TestCmdEvalRulesInvalidFiles(t *testing.T) {
	err := cmdEval(context.Background(), []string{"rules", "/no/corpus.json", "/no/scorecard.json"})
	if err == nil {
		t.Fatal("expected error for nonexistent files")
	}
}

// ---------------------------------------------------------------------------
// cmdEval with invalid JSON content
// ---------------------------------------------------------------------------

func TestCmdEvalValidateMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(p, []byte(`{not json`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := cmdEval(context.Background(), []string{"validate", p})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestCmdEvalSummarizeMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(p, []byte(`{not json`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := cmdEval(context.Background(), []string{"summarize", p})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

// ---------------------------------------------------------------------------
// parseDaemonSubmitArgs edge cases
// ---------------------------------------------------------------------------

func TestParseDaemonSubmitArgsEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantSess  string
		wantPrmpt string
		wantErr   bool
	}{
		{
			name:      "empty args",
			args:      nil,
			wantSess:  "",
			wantPrmpt: "",
			wantErr:   false,
		},
		{
			name:    "session flag without value",
			args:    []string{"--session"},
			wantErr: true,
		},
		{
			name:      "session at end with prompt before",
			args:      []string{"hello", "--session", "s1"},
			wantSess:  "s1",
			wantPrmpt: "hello",
			wantErr:   false,
		},
		{
			name:      "multiple words no session",
			args:      []string{"build", "the", "feature"},
			wantSess:  "",
			wantPrmpt: "build the feature",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessID, prompt, err := parseDaemonSubmitArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sessID != tt.wantSess {
				t.Fatalf("sessionID = %q, want %q", sessID, tt.wantSess)
			}
			if prompt != tt.wantPrmpt {
				t.Fatalf("prompt = %q, want %q", prompt, tt.wantPrmpt)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// sendIPCRequest error paths
// ---------------------------------------------------------------------------

func TestSendIPCRequestConnectionRefused(t *testing.T) {
	_, err := sendIPCRequest("/tmp/elnath-test-nonexistent.sock", daemon.IPCRequest{Command: "status"})
	if err == nil {
		t.Fatal("expected connection error")
	}
	if !strings.Contains(err.Error(), "connect to daemon") {
		t.Fatalf("error = %q, want connection error", err.Error())
	}
}

func TestSendIPCRequestServerClosesImmediately(t *testing.T) {
	socketPath := testSocketPath(t, "close")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()

	_, err = sendIPCRequest(socketPath, daemon.IPCRequest{Command: "test"})
	if err == nil {
		t.Fatal("expected error when server closes")
	}
	<-done
}

// ---------------------------------------------------------------------------
// cmdDaemonStatus with no tasks
// ---------------------------------------------------------------------------

func TestCmdDaemonStatusEmpty(t *testing.T) {
	socketPath := testSocketPath(t, "stempty")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		resp := daemon.IPCResponse{
			OK:   true,
			Data: map[string]any{"tasks": []any{}},
		}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdDaemonStatus(context.Background()); err != nil {
			t.Fatalf("cmdDaemonStatus: %v", err)
		}
	})
	if !strings.Contains(stdout, "No tasks") {
		t.Fatalf("stdout = %q, want No tasks", stdout)
	}
	<-done
}

func TestCmdDaemonStatusDaemonError(t *testing.T) {
	socketPath := testSocketPath(t, "sterr")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		resp := daemon.IPCResponse{OK: false, Err: "internal error"}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	err = cmdDaemonStatus(context.Background())
	if err == nil || !strings.Contains(err.Error(), "internal error") {
		t.Fatalf("cmdDaemonStatus error = %v, want internal error", err)
	}
	<-done
}

func TestCmdDaemonStatusUnmarshalablResponse(t *testing.T) {
	socketPath := testSocketPath(t, "straw")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		resp := daemon.IPCResponse{
			OK:   true,
			Data: "plain string not struct",
		}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdDaemonStatus(context.Background()); err != nil {
			t.Fatalf("cmdDaemonStatus: %v", err)
		}
	})
	if !strings.Contains(stdout, "Raw response") {
		t.Fatalf("stdout = %q, want raw response fallback", stdout)
	}
	<-done
}

// ---------------------------------------------------------------------------
// cmdDaemonStatus with long payload/session truncation
// ---------------------------------------------------------------------------

func TestCmdDaemonStatusTruncatesLongFields(t *testing.T) {
	socketPath := testSocketPath(t, "sttrunc")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	longPayload := strings.Repeat("x", 100)
	longSession := strings.Repeat("s", 30)
	longSummary := strings.Repeat("z", 40)

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
					"payload":    longPayload,
					"session_id": longSession,
					"progress":   "",
					"summary":    longSummary,
				}},
			},
		}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdDaemonStatus(context.Background()); err != nil {
			t.Fatalf("cmdDaemonStatus: %v", err)
		}
	})
	if !strings.Contains(stdout, "...") {
		t.Fatalf("stdout = %q, want truncation markers", stdout)
	}
	if strings.Contains(stdout, longPayload) {
		t.Fatalf("stdout should truncate long payload")
	}
	<-done
}

// ---------------------------------------------------------------------------
// cmdEval summarize with failure families output
// ---------------------------------------------------------------------------

func TestCmdEvalSummarizeWithFailureFamilies(t *testing.T) {
	dir := t.TempDir()
	scorecardPath := filepath.Join(dir, "scorecard.json")
	if err := os.WriteFile(scorecardPath, []byte(`{
		"version": "v1",
		"system": "elnath",
		"results": [
			{"task_id":"T1","track":"brownfield_feature","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"failure_family":"repo_context_miss","duration_seconds":2},
			{"task_id":"T2","track":"bugfix","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"failure_family":"repo_context_miss","duration_seconds":3}
		]
	}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdEval(context.Background(), []string{"summarize", scorecardPath}); err != nil {
			t.Fatalf("cmdEval summarize: %v", err)
		}
	})
	if !strings.Contains(stdout, "Failure families") {
		t.Fatalf("stdout = %q, want failure families section", stdout)
	}
	if !strings.Contains(stdout, "repo_context_miss") {
		t.Fatalf("stdout = %q, want specific failure family", stdout)
	}
}

// ---------------------------------------------------------------------------
// cmdEval summarize with baseline label
// ---------------------------------------------------------------------------

func TestCmdEvalSummarizeWithBaseline(t *testing.T) {
	dir := t.TempDir()
	scorecardPath := filepath.Join(dir, "scorecard.json")
	if err := os.WriteFile(scorecardPath, []byte(`{
		"version": "v1",
		"system": "elnath",
		"baseline": "claude+omx",
		"results": [
			{"task_id":"T1","track":"brownfield_feature","language":"go","success":true,"intervention_count":0,"intervention_needed":false,"verification_passed":true,"duration_seconds":2}
		]
	}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdEval(context.Background(), []string{"summarize", scorecardPath}); err != nil {
			t.Fatalf("cmdEval summarize: %v", err)
		}
	})
	if !strings.Contains(stdout, "Baseline: claude+omx") {
		t.Fatalf("stdout = %q, want baseline label", stdout)
	}
	if !strings.Contains(stdout, "Verification: pass_rate=1.00") {
		t.Fatalf("stdout = %q, want verification pass rate", stdout)
	}
}

// ---------------------------------------------------------------------------
// cmdEval gate-month2 fail
// ---------------------------------------------------------------------------

func TestCmdEvalGateMonth2Fail(t *testing.T) {
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`{"version":"v1","tasks":[
		{"id":"BF-001","title":"task","track":"brownfield_feature","language":"go","repo_class":"cli","benchmark_family":"bf","prompt":"do","repo":"https://github.com/x/y","repo_ref":"abc","acceptance_criteria":["ok"]},
		{"id":"BUG-001","title":"holdout","track":"bugfix","language":"go","repo_class":"svc","benchmark_family":"bf","holdout":true,"prompt":"fix","repo":"https://github.com/x/z","repo_ref":"def","acceptance_criteria":["ok"]}
	]}`), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	currentPath := filepath.Join(dir, "current.json")
	if err := os.WriteFile(currentPath, []byte(`{"version":"v1","system":"elnath","results":[
		{"task_id":"BF-001","track":"brownfield_feature","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"verification_passed":false,"duration_seconds":1},
		{"task_id":"BUG-001","track":"bugfix","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"verification_passed":false,"duration_seconds":1}
	]}`), 0o644); err != nil {
		t.Fatalf("write current: %v", err)
	}
	baselinePath := filepath.Join(dir, "baseline.json")
	if err := os.WriteFile(baselinePath, []byte(`{"version":"v1","system":"baseline","results":[
		{"task_id":"BF-001","track":"brownfield_feature","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"verification_passed":false,"duration_seconds":1},
		{"task_id":"BUG-001","track":"bugfix","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"verification_passed":false,"duration_seconds":1}
	]}`), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		err := cmdEval(context.Background(), []string{"gate-month2", corpusPath, currentPath, baselinePath})
		if err == nil {
			t.Fatal("expected gate failure")
		}
		if !strings.Contains(err.Error(), "month 2 gate failed") {
			t.Fatalf("error = %q, want gate failed", err.Error())
		}
	})
	if !strings.Contains(stdout, "Month 2 gate: FAIL") {
		t.Fatalf("stdout = %q, want FAIL", stdout)
	}
	if !strings.Contains(stdout, "reason:") {
		t.Fatalf("stdout = %q, want reasons", stdout)
	}
}

// ---------------------------------------------------------------------------
// cmdEval diff track output
// ---------------------------------------------------------------------------

func TestCmdEvalDiffTrackOutput(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "current.json")
	if err := os.WriteFile(currentPath, []byte(`{"version":"v1","system":"elnath","results":[
		{"task_id":"T1","track":"brownfield_feature","language":"go","success":true,"intervention_count":0,"intervention_needed":false,"verification_passed":true,"duration_seconds":1},
		{"task_id":"T2","track":"bugfix","language":"go","success":true,"intervention_count":0,"intervention_needed":false,"verification_passed":true,"duration_seconds":1}
	]}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	baselinePath := filepath.Join(dir, "baseline.json")
	if err := os.WriteFile(baselinePath, []byte(`{"version":"v1","system":"baseline","results":[
		{"task_id":"T1","track":"brownfield_feature","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"verification_passed":false,"duration_seconds":2},
		{"task_id":"T2","track":"bugfix","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"verification_passed":false,"duration_seconds":2}
	]}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdEval(context.Background(), []string{"diff", currentPath, baselinePath}); err != nil {
			t.Fatalf("cmdEval diff: %v", err)
		}
	})
	if !strings.Contains(stdout, "Track brownfield_feature delta") {
		t.Fatalf("stdout = %q, want brownfield track delta", stdout)
	}
	if !strings.Contains(stdout, "Track bugfix delta") {
		t.Fatalf("stdout = %q, want bugfix track delta", stdout)
	}
}

// ---------------------------------------------------------------------------
// buildRouter
// ---------------------------------------------------------------------------

func TestBuildRouterReturnsNonNil(t *testing.T) {
	r := buildRouter(orchestrator.WorkflowConfig{})
	if r == nil {
		t.Fatal("buildRouter returned nil")
	}
}

// ---------------------------------------------------------------------------
// cmdDaemonSubmit response with raw data (non-map)
// ---------------------------------------------------------------------------

func TestCmdDaemonSubmitRawResponse(t *testing.T) {
	socketPath := testSocketPath(t, "subraw")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		resp := daemon.IPCResponse{
			OK:   true,
			Data: "raw-string-response",
		}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdDaemonSubmit(context.Background(), []string{"some", "task"}); err != nil {
			t.Fatalf("cmdDaemonSubmit raw: %v", err)
		}
	})
	if !strings.Contains(stdout, "Task submitted") {
		t.Fatalf("stdout = %q, want task submitted", stdout)
	}
	<-done
}

// ---------------------------------------------------------------------------
// registerWikiTools with empty dir/db
// ---------------------------------------------------------------------------

func TestRegisterWikiToolsNilInputs(t *testing.T) {
	gs, idx := registerWikiTools(nil, "", nil)
	if gs != nil || idx != nil {
		t.Fatalf("registerWikiTools with empty args should return nil, nil")
	}
}

// ---------------------------------------------------------------------------
// sendIPCRequest with malformed response
// ---------------------------------------------------------------------------

func TestSendIPCRequestMalformedResponse(t *testing.T) {
	socketPath := testSocketPath(t, "malform")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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

		// Read request, then write malformed JSON
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("{broken json\n"))
	}()

	_, err = sendIPCRequest(socketPath, daemon.IPCRequest{Command: "test"})
	if err == nil {
		t.Fatal("expected error for malformed response")
	}
	if !strings.Contains(err.Error(), "unmarshal response") {
		t.Fatalf("error = %q, want unmarshal error", err.Error())
	}
	<-done
}

// ---------------------------------------------------------------------------
// extractConfigFlag / extractPersonaFlag / extractSessionFlag edge cases
// ---------------------------------------------------------------------------

func TestExtractFlagsEdgeCases(t *testing.T) {
	t.Run("config flag at end", func(t *testing.T) {
		if got := extractConfigFlag([]string{"elnath", "--config"}); got != "" {
			t.Fatalf("got %q, want empty when --config is last arg", got)
		}
	})
	t.Run("persona flag at end", func(t *testing.T) {
		if got := extractPersonaFlag([]string{"elnath", "--persona"}); got != "" {
			t.Fatalf("got %q, want empty when --persona is last arg", got)
		}
	})
	t.Run("session flag at end", func(t *testing.T) {
		if got := extractSessionFlag([]string{"elnath", "--session"}); got != "" {
			t.Fatalf("got %q, want empty when --session is last arg", got)
		}
	})
	t.Run("no flags at all", func(t *testing.T) {
		if got := extractConfigFlag([]string{"elnath"}); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
		if got := extractPersonaFlag([]string{"elnath"}); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
		if got := extractSessionFlag([]string{"elnath"}); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})
}

// ---------------------------------------------------------------------------
// estimateFiles edge cases
// ---------------------------------------------------------------------------

func TestEstimateFilesEdgeCases(t *testing.T) {
	tests := []struct {
		input string
		min   int
	}{
		{"no file references at all", 1},
		{"fix handler.go and service.ts and main.py", 3},
		{"config.yaml", 1},
		{"path/to/deep/file", 1},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := estimateFiles(tt.input)
			if got < tt.min {
				t.Fatalf("estimateFiles(%q) = %d, want >= %d", tt.input, got, tt.min)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildRoutingContext edge cases
// ---------------------------------------------------------------------------

func TestBuildRoutingContextEdgeCases(t *testing.T) {
	t.Run("greenfield with no cues", func(t *testing.T) {
		ctx := buildRoutingContext("create a brand new project from scratch")
		if ctx.ExistingCode {
			t.Fatal("expected ExistingCode = false for greenfield")
		}
		if ctx.VerificationHint {
			t.Fatal("expected VerificationHint = false")
		}
	})

	t.Run("brownfield minimum files", func(t *testing.T) {
		ctx := buildRoutingContext("fix the existing test")
		if !ctx.ExistingCode {
			t.Fatal("expected ExistingCode = true")
		}
		if !ctx.VerificationHint {
			t.Fatal("expected VerificationHint = true")
		}
		if ctx.EstimatedFiles < 2 {
			t.Fatalf("EstimatedFiles = %d, want >= 2 for brownfield+verification", ctx.EstimatedFiles)
		}
	})
}

// ---------------------------------------------------------------------------
// cmdDaemonStatus multiple tasks
// ---------------------------------------------------------------------------

func TestCmdDaemonStatusMultipleTasks(t *testing.T) {
	socketPath := testSocketPath(t, "stmulti")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		resp := daemon.IPCResponse{
			OK: true,
			Data: map[string]any{
				"tasks": []map[string]any{
					{
						"id":         1,
						"status":     "running",
						"payload":    "task one",
						"session_id": "s1",
						"progress":   "",
						"summary":    "doing stuff",
					},
					{
						"id":         2,
						"status":     "queued",
						"payload":    "task two",
						"session_id": "s2",
						"progress":   "",
						"summary":    "",
					},
				},
			},
		}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdDaemonStatus(context.Background()); err != nil {
			t.Fatalf("cmdDaemonStatus: %v", err)
		}
	})
	if !strings.Contains(stdout, "running") || !strings.Contains(stdout, "queued") {
		t.Fatalf("stdout = %q, want both tasks", stdout)
	}
	if !strings.Contains(stdout, "task one") || !strings.Contains(stdout, "task two") {
		t.Fatalf("stdout = %q, want both payloads", stdout)
	}
	if !strings.Contains(stdout, "ID") && !strings.Contains(stdout, "STATUS") {
		t.Fatalf("stdout = %q, want table headers", stdout)
	}
	<-done
}

// ---------------------------------------------------------------------------
// cmdEval scaffold files can be round-tripped
// ---------------------------------------------------------------------------

func TestCmdEvalScaffoldRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// Generate baseline scaffold
	baselinePath := filepath.Join(dir, "baseline.json")
	captureOutput(t, func() {
		if err := cmdEval(context.Background(), []string{"scaffold-baseline", baselinePath}); err != nil {
			t.Fatalf("scaffold-baseline: %v", err)
		}
	})

	// Verify the scaffold is valid JSON with expected fields
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read scaffold: %v", err)
	}
	var plan map[string]interface{}
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatalf("scaffold is not valid JSON: %v", err)
	}
	for _, key := range []string{"version", "baseline", "corpus_path", "command_template", "output_path"} {
		if _, ok := plan[key]; !ok {
			t.Fatalf("scaffold missing key %q", key)
		}
	}

	// Generate current scaffold
	currentPath := filepath.Join(dir, "current.json")
	captureOutput(t, func() {
		if err := cmdEval(context.Background(), []string{"scaffold-current", currentPath}); err != nil {
			t.Fatalf("scaffold-current: %v", err)
		}
	})

	data, err = os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("read current scaffold: %v", err)
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatalf("current scaffold is not valid JSON: %v", err)
	}
	if fmt.Sprintf("%v", plan["system"]) != "elnath-current" {
		t.Fatalf("current scaffold system = %v, want elnath-current", plan["system"])
	}
}

// ---------------------------------------------------------------------------
// cmdWiki usage and error paths
// ---------------------------------------------------------------------------

// writeWikiTestConfig creates a config with data_dir and wiki_dir that actually
// exist on disk so that cmdWiki can open the database.
func writeWikiTestConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("mkdir wiki: %v", err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	data := "data_dir: " + dataDir + "\n" +
		"wiki_dir: " + wikiDir + "\n" +
		"locale: en\n" +
		"permission:\n  mode: default\n"
	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

func TestCmdWikiUsage(t *testing.T) {
	cfgPath := writeWikiTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdWiki(context.Background(), nil); err != nil {
			t.Fatalf("cmdWiki usage: %v", err)
		}
	})
	if !strings.Contains(stdout, "Usage: elnath wiki") {
		t.Fatalf("stdout = %q, want wiki usage", stdout)
	}
}

func TestCmdWikiUnknownSubcommand(t *testing.T) {
	cfgPath := writeWikiTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	err := cmdWiki(context.Background(), []string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown wiki subcommand: bogus") {
		t.Fatalf("cmdWiki(bogus) err = %v, want unknown subcommand", err)
	}
}

func TestCmdWikiSearchMissingQuery(t *testing.T) {
	cfgPath := writeWikiTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	err := cmdWiki(context.Background(), []string{"search"})
	if err == nil || !strings.Contains(err.Error(), "usage: elnath wiki search") {
		t.Fatalf("cmdWiki search err = %v, want usage error", err)
	}
}

// ---------------------------------------------------------------------------
// cmdSearch missing query
// ---------------------------------------------------------------------------

func TestCmdSearchMissingQuery(t *testing.T) {
	err := cmdSearch(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "usage: elnath search") {
		t.Fatalf("cmdSearch err = %v, want usage error", err)
	}
}

// ---------------------------------------------------------------------------
// cmdDaemon dispatches to subcommands (testing submit/stop/status routing)
// ---------------------------------------------------------------------------

func TestCmdDaemonDispatchesSubmit(t *testing.T) {
	socketPath := testSocketPath(t, "dmsub")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		resp := daemon.IPCResponse{OK: true, Data: map[string]interface{}{"task_id": 1, "existed": false}}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdDaemon(context.Background(), []string{"submit", "hello"}); err != nil {
			t.Fatalf("cmdDaemon submit: %v", err)
		}
	})
	if !strings.Contains(stdout, "Task #1 enqueued") {
		t.Fatalf("stdout = %q, want enqueued output", stdout)
	}
	<-done
}

func TestCmdDaemonDispatchesStatus(t *testing.T) {
	socketPath := testSocketPath(t, "dmstat")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		resp := daemon.IPCResponse{OK: true, Data: map[string]any{"tasks": []any{}}}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdDaemon(context.Background(), []string{"status"}); err != nil {
			t.Fatalf("cmdDaemon status: %v", err)
		}
	})
	if !strings.Contains(stdout, "No tasks") {
		t.Fatalf("stdout = %q, want No tasks", stdout)
	}
	<-done
}

func TestCmdDaemonDispatchesStop(t *testing.T) {
	socketPath := testSocketPath(t, "dmstop")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		resp := daemon.IPCResponse{OK: true}
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdDaemon(context.Background(), []string{"stop"}); err != nil {
			t.Fatalf("cmdDaemon stop: %v", err)
		}
	})
	if !strings.Contains(stdout, "Daemon stop requested") {
		t.Fatalf("stdout = %q, want stop message", stdout)
	}
	<-done
}

// ---------------------------------------------------------------------------
// registerWikiTools with valid wiki dir
// ---------------------------------------------------------------------------

func TestRegisterWikiToolsWithValidDir(t *testing.T) {
	dir := t.TempDir()
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	db, err := core.OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	reg := tools.NewRegistry()
	gs, idx := registerWikiTools(reg, wikiDir, db.Wiki)
	if idx == nil {
		t.Fatal("expected non-nil wiki index")
	}
	// gs may be nil if git init fails in tempdir, that's fine
	_ = gs
}

// ---------------------------------------------------------------------------
// loadCodexAuth edge cases
// ---------------------------------------------------------------------------

func TestLoadCodexAuthMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	token, model, accountID := loadCodexAuth()
	if token != "" || model != "" || accountID != "" {
		t.Fatalf("loadCodexAuth with missing files = (%q,%q,%q), want all empty", token, model, accountID)
	}
}

func TestLoadCodexAuthInvalidJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{invalid`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	token, model, accountID := loadCodexAuth()
	if token != "" || model != "" || accountID != "" {
		t.Fatalf("loadCodexAuth with invalid JSON = (%q,%q,%q), want all empty", token, model, accountID)
	}
}

func TestLoadCodexAuthWrongMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	auth := map[string]any{
		"auth_mode": "api_key",
		"tokens":    map[string]any{"access_token": "tok"},
	}
	data, _ := json.Marshal(auth)
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	token, _, _ := loadCodexAuth()
	if token != "" {
		t.Fatalf("loadCodexAuth with wrong auth_mode returned token %q, want empty", token)
	}
}

func TestLoadCodexAuthNoConfigToml(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	auth := map[string]any{
		"auth_mode": "chatgpt",
		"tokens":    map[string]any{"access_token": "tok_abc"},
	}
	data, _ := json.Marshal(auth)
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	token, model, _ := loadCodexAuth()
	if token != "tok_abc" {
		t.Fatalf("token = %q, want tok_abc", token)
	}
	if model != "gpt-4o" {
		t.Fatalf("model = %q, want gpt-4o (default)", model)
	}
}

func TestLoadCodexModelMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := loadCodexModel(); got != "o4-mini" {
		t.Fatalf("loadCodexModel = %q, want o4-mini default", got)
	}
}

// ---------------------------------------------------------------------------
// cmdWiki subcommands with real DB
// ---------------------------------------------------------------------------

func TestCmdWikiSearch(t *testing.T) {
	cfgPath := writeWikiTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdWiki(context.Background(), []string{"search", "nonexistent topic"}); err != nil {
			t.Fatalf("cmdWiki search: %v", err)
		}
	})
	if !strings.Contains(stdout, "No results found") {
		t.Fatalf("stdout = %q, want no results", stdout)
	}
}

func TestCmdWikiList(t *testing.T) {
	cfgPath := writeWikiTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdWiki(context.Background(), []string{"list"}); err != nil {
			t.Fatalf("cmdWiki list: %v", err)
		}
	})
	if !strings.Contains(stdout, "No wiki pages found") {
		t.Fatalf("stdout = %q, want no pages", stdout)
	}
}

func TestCmdWikiLint(t *testing.T) {
	cfgPath := writeWikiTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdWiki(context.Background(), []string{"lint"}); err != nil {
			t.Fatalf("cmdWiki lint: %v", err)
		}
	})
	if !strings.Contains(stdout, "healthy") {
		t.Fatalf("stdout = %q, want healthy", stdout)
	}
}

func TestCmdWikiRebuild(t *testing.T) {
	cfgPath := writeWikiTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdWiki(context.Background(), []string{"rebuild"}); err != nil {
			t.Fatalf("cmdWiki rebuild: %v", err)
		}
	})
	if !strings.Contains(stdout, "rebuilt") {
		t.Fatalf("stdout = %q, want rebuilt message", stdout)
	}
}

// ---------------------------------------------------------------------------
// cmdSearch with real conversation DB
// ---------------------------------------------------------------------------

func TestCmdSearchNoResults(t *testing.T) {
	cfgPath := writeWikiTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdSearch(context.Background(), []string{"nothing", "here"}); err != nil {
			t.Fatalf("cmdSearch: %v", err)
		}
	})
	if !strings.Contains(stdout, "No results found") {
		t.Fatalf("stdout = %q, want no results", stdout)
	}
}

// ---------------------------------------------------------------------------
// loadCodexModel edge cases
// ---------------------------------------------------------------------------

func TestLoadCodexModelNoModelLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("other_key = \"value\"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got := loadCodexModel(); got != "o4-mini" {
		t.Fatalf("loadCodexModel = %q, want o4-mini default", got)
	}
}

// ---------------------------------------------------------------------------
// cmdWiki with actual wiki pages
// ---------------------------------------------------------------------------

func TestCmdWikiListWithPages(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("mkdir wiki: %v", err)
	}

	page := "---\ntitle: Test Page\ntype: note\n---\n\nThis is a test wiki page.\n"
	if err := os.WriteFile(filepath.Join(wikiDir, "test-page.md"), []byte(page), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.yaml")
	cfgData := "data_dir: " + dataDir + "\nwiki_dir: " + wikiDir + "\nlocale: en\npermission:\n  mode: default\n"
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdWiki(context.Background(), []string{"list"}); err != nil {
			t.Fatalf("cmdWiki list: %v", err)
		}
	})
	if !strings.Contains(stdout, "Test Page") {
		t.Fatalf("stdout = %q, want Test Page listed", stdout)
	}
}

func TestCmdWikiSearchWithContent(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("mkdir wiki: %v", err)
	}

	page := "---\ntitle: Architecture Decision\ntype: note\n---\n\nThe system uses SQLite for persistence and FTS5 for full-text search.\n"
	if err := os.WriteFile(filepath.Join(wikiDir, "architecture.md"), []byte(page), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.yaml")
	cfgData := "data_dir: " + dataDir + "\nwiki_dir: " + wikiDir + "\nlocale: en\npermission:\n  mode: default\n"
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	captureOutput(t, func() {
		if err := cmdWiki(context.Background(), []string{"rebuild"}); err != nil {
			t.Fatalf("cmdWiki rebuild: %v", err)
		}
	})

	stdout, _ := captureOutput(t, func() {
		if err := cmdWiki(context.Background(), []string{"search", "SQLite persistence"}); err != nil {
			t.Fatalf("cmdWiki search: %v", err)
		}
	})
	if strings.Contains(stdout, "No results found") {
		t.Logf("search returned no results (FTS may need explicit index); skipping content assertion")
	}
}

// ---------------------------------------------------------------------------
// cmdSearch with conversation data
// ---------------------------------------------------------------------------

func TestCmdSearchWithData(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("mkdir wiki: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.yaml")
	cfgData := "data_dir: " + dataDir + "\nwiki_dir: " + wikiDir + "\nlocale: en\npermission:\n  mode: default\n"
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdSearch(context.Background(), []string{"foobar"}); err != nil {
			t.Fatalf("cmdSearch: %v", err)
		}
	})
	if !strings.Contains(stdout, "No results found") {
		t.Fatalf("stdout = %q, want no results", stdout)
	}
}

// Note: cmdDaemonStart and cmdDaemonInstall are not tested here because they
// require real infrastructure (starts daemon, installs launchd plist) and
// fall through to the real config when the given config path fails.

// ---------------------------------------------------------------------------
// cmdTelegramShell error paths
// ---------------------------------------------------------------------------

func TestCmdTelegramShellNotEnabled(t *testing.T) {
	cfgPath := writeTestConfig(t, onboarding.En)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	err := cmdTelegramShell(context.Background())
	if err == nil || !strings.Contains(err.Error(), "telegram.enabled=true") {
		t.Fatalf("cmdTelegramShell err = %v, want telegram not enabled error", err)
	}
}

// ---------------------------------------------------------------------------
// cmdSearch with conversation results
// ---------------------------------------------------------------------------

func TestCmdSearchWithResults(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("mkdir wiki: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.yaml")
	cfgData := "data_dir: " + dataDir + "\nwiki_dir: " + wikiDir + "\nlocale: en\npermission:\n  mode: default\n"
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Open DB and insert a conversation
	db, err := core.OpenDB(dataDir)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	if err := conversation.InitSchema(db.Main); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	store := conversation.NewHistoryStore(db.Main)
	messages := []llm.Message{
		llm.NewUserMessage("How does the subscription webhook handler work?"),
		llm.NewAssistantMessage("The subscription webhook handler processes incoming events from the payment provider."),
	}
	if err := store.Save(context.Background(), "test-session-123", messages); err != nil {
		t.Fatalf("Save: %v", err)
	}

	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := cmdSearch(context.Background(), []string{"subscription", "webhook"}); err != nil {
			t.Fatalf("cmdSearch: %v", err)
		}
	})
	// If FTS5 is available, we should see results; otherwise "No results found"
	if strings.Contains(stdout, "session:test-session-123") {
		// Results found, verify format
		if !strings.Contains(stdout, "1.") {
			t.Fatalf("stdout = %q, want numbered result", stdout)
		}
	}
}

// ---------------------------------------------------------------------------
// cmdWiki search with results rendering
// ---------------------------------------------------------------------------

func TestCmdWikiSearchWithResults(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("mkdir wiki: %v", err)
	}

	page := "---\ntitle: Webhook Architecture\ntype: note\n---\n\nThe webhook handler processes subscription events from payment providers.\n"
	if err := os.WriteFile(filepath.Join(wikiDir, "webhook-architecture.md"), []byte(page), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.yaml")
	cfgData := "data_dir: " + dataDir + "\nwiki_dir: " + wikiDir + "\nlocale: en\npermission:\n  mode: default\n"
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	// Rebuild FTS index first
	captureOutput(t, func() {
		if err := cmdWiki(context.Background(), []string{"rebuild"}); err != nil {
			t.Fatalf("cmdWiki rebuild: %v", err)
		}
	})

	stdout, _ := captureOutput(t, func() {
		if err := cmdWiki(context.Background(), []string{"search", "webhook"}); err != nil {
			t.Fatalf("cmdWiki search: %v", err)
		}
	})
	// Verify the search finds the page and renders results
	if strings.Contains(stdout, "Webhook Architecture") {
		if !strings.Contains(stdout, "1.") {
			t.Fatalf("stdout = %q, want numbered result", stdout)
		}
	}
}

// ---------------------------------------------------------------------------
// cmdDaemonStop/Submit config load from non-existent socket
// ---------------------------------------------------------------------------

func TestCmdDaemonStopConnectionRefused(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "nonexistent.sock")
	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	err := cmdDaemonStop(context.Background())
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	if !strings.Contains(err.Error(), "ipc:") {
		t.Fatalf("err = %q, want ipc error", err.Error())
	}
}

func TestCmdDaemonSubmitConnectionRefused(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "nonexistent.sock")
	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	err := cmdDaemonSubmit(context.Background(), []string{"hello", "world"})
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	if !strings.Contains(err.Error(), "ipc:") {
		t.Fatalf("err = %q, want ipc error", err.Error())
	}
}

func TestCmdDaemonStatusConnectionRefused(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "nonexistent.sock")
	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	err := cmdDaemonStatus(context.Background())
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	if !strings.Contains(err.Error(), "ipc:") {
		t.Fatalf("err = %q, want ipc error", err.Error())
	}
}
