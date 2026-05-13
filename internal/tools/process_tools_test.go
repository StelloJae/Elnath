package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"
)

type processStartTestOutput struct {
	ProcessID int64  `json:"process_id"`
	Status    string `json:"status"`
	CWD       string `json:"cwd"`
	Receipt   struct {
		Tool            string `json:"tool"`
		Action          string `json:"action"`
		ReadOnly        bool   `json:"read_only"`
		Persistent      bool   `json:"persistent"`
		ExecutionPolicy string `json:"execution_policy"`
		ProcessID       int64  `json:"process_id"`
		Status          string `json:"status"`
		Terminal        bool   `json:"terminal"`
		TimeoutMS       int    `json:"timeout_ms"`
		CWD             string `json:"cwd"`
		FollowupTool    string `json:"followup_tool"`
	} `json:"receipt"`
}

type processMonitorTestOutput struct {
	ProcessID       int64  `json:"process_id"`
	Found           bool   `json:"found"`
	Status          string `json:"status"`
	Terminal        bool   `json:"terminal"`
	ExitCode        *int   `json:"exit_code"`
	StdoutTail      string `json:"stdout_tail"`
	StderrTail      string `json:"stderr_tail"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	Receipt         struct {
		Tool            string `json:"tool"`
		Action          string `json:"action"`
		ReadOnly        bool   `json:"read_only"`
		Persistent      bool   `json:"persistent"`
		ExecutionPolicy string `json:"execution_policy"`
		ProcessID       int64  `json:"process_id"`
		Status          string `json:"status"`
		Terminal        bool   `json:"terminal"`
		ExitCode        *int   `json:"exit_code"`
		Found           bool   `json:"found"`
		TailBytes       int    `json:"tail_bytes"`
		StdoutRawBytes  int64  `json:"stdout_raw_bytes"`
		StderrRawBytes  int64  `json:"stderr_raw_bytes"`
		StdoutTruncated bool   `json:"stdout_truncated"`
		StderrTruncated bool   `json:"stderr_truncated"`
		FollowupTool    string `json:"followup_tool"`
	} `json:"receipt"`
}

type processStopTestOutput struct {
	ProcessID int64  `json:"process_id"`
	Found     bool   `json:"found"`
	Stopped   bool   `json:"stopped"`
	Status    string `json:"status"`
	Terminal  bool   `json:"terminal"`
	Receipt   struct {
		Tool            string `json:"tool"`
		Action          string `json:"action"`
		ReadOnly        bool   `json:"read_only"`
		Persistent      bool   `json:"persistent"`
		ExecutionPolicy string `json:"execution_policy"`
		ProcessID       int64  `json:"process_id"`
		Status          string `json:"status"`
		Terminal        bool   `json:"terminal"`
		Found           bool   `json:"found"`
		StopSignal      string `json:"stop_signal"`
		FollowupTool    string `json:"followup_tool"`
	} `json:"receipt"`
}

func executeProcessStart(t *testing.T, tool *ProcessStartTool, input map[string]any) processStartTestOutput {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error: %s", res.Output)
	}
	var out processStartTestOutput
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	return out
}

func executeProcessMonitor(t *testing.T, tool *ProcessMonitorTool, id int64, maxChars int) processMonitorTestOutput {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"process_id": id, "max_chars": maxChars})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error: %s", res.Output)
	}
	var out processMonitorTestOutput
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	return out
}

func waitProcessTerminal(t *testing.T, tool *ProcessMonitorTool, id int64) processMonitorTestOutput {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var out processMonitorTestOutput
	for time.Now().Before(deadline) {
		out = executeProcessMonitor(t, tool, id, 2000)
		if out.Terminal {
			return out
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process %d did not become terminal; last=%+v", id, out)
	return out
}

func TestProcessToolsStartMonitorTerminalReceipt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group cleanup not implemented on windows")
	}
	mgr := NewProcessManager(NewPathGuard(t.TempDir(), nil))
	t.Cleanup(func() { _ = mgr.Close(context.Background()) })

	start := executeProcessStart(t, NewProcessStartTool(mgr), map[string]any{
		"command":    "printf hello; printf warn >&2",
		"timeout_ms": 2000,
	})
	if start.ProcessID <= 0 || start.Status != "running" {
		t.Fatalf("start = %+v, want running process id", start)
	}
	if start.Receipt.Tool != ProcessStartToolName || start.Receipt.ExecutionPolicy != "session_process_start" {
		t.Fatalf("start receipt = %+v", start.Receipt)
	}
	if start.Receipt.ReadOnly || !start.Receipt.Persistent || start.Receipt.Terminal {
		t.Fatalf("start receipt flags = %+v", start.Receipt)
	}
	if start.Receipt.FollowupTool != ProcessMonitorToolName {
		t.Fatalf("start followup tool = %q, want %q", start.Receipt.FollowupTool, ProcessMonitorToolName)
	}

	mon := waitProcessTerminal(t, NewProcessMonitorTool(mgr), start.ProcessID)
	if mon.Status != "completed" || !mon.Terminal {
		t.Fatalf("monitor = %+v, want completed terminal", mon)
	}
	if mon.ExitCode == nil || *mon.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", mon.ExitCode)
	}
	if !strings.Contains(mon.StdoutTail, "hello") || !strings.Contains(mon.StderrTail, "warn") {
		t.Fatalf("tails stdout=%q stderr=%q", mon.StdoutTail, mon.StderrTail)
	}
	if mon.Receipt.Tool != ProcessMonitorToolName || mon.Receipt.ExecutionPolicy != "session_process_observation" {
		t.Fatalf("monitor receipt = %+v", mon.Receipt)
	}
	if !mon.Receipt.ReadOnly || mon.Receipt.Persistent || !mon.Receipt.Found || !mon.Receipt.Terminal {
		t.Fatalf("monitor receipt flags = %+v", mon.Receipt)
	}
	if mon.Receipt.ExitCode == nil || *mon.Receipt.ExitCode != 0 || mon.Receipt.StdoutRawBytes == 0 || mon.Receipt.StderrRawBytes == 0 {
		t.Fatalf("monitor receipt completion/output metadata = %+v", mon.Receipt)
	}
	if mon.Receipt.FollowupTool != "" {
		t.Fatalf("terminal monitor followup tool = %q, want empty", mon.Receipt.FollowupTool)
	}
}

func TestProcessToolsReportRunningMonitorFollowup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group cleanup not implemented on windows")
	}
	mgr := NewProcessManager(NewPathGuard(t.TempDir(), nil))
	t.Cleanup(func() { _ = mgr.Close(context.Background()) })

	start := executeProcessStart(t, NewProcessStartTool(mgr), map[string]any{
		"command":    "sleep 10",
		"timeout_ms": 10000,
	})
	mon := executeProcessMonitor(t, NewProcessMonitorTool(mgr), start.ProcessID, 1000)
	if !mon.Found || mon.Status != "running" || mon.Terminal {
		t.Fatalf("monitor = %+v, want running non-terminal process", mon)
	}
	if mon.Receipt.FollowupTool != ProcessMonitorToolName {
		t.Fatalf("running monitor followup tool = %q, want %q", mon.Receipt.FollowupTool, ProcessMonitorToolName)
	}

	raw, err := json.Marshal(map[string]any{"process_id": start.ProcessID, "reason": "cleanup"})
	if err != nil {
		t.Fatalf("marshal stop: %v", err)
	}
	if res, err := NewProcessStopTool(mgr).Execute(context.Background(), raw); err != nil || res.IsError {
		if err != nil {
			t.Fatalf("cleanup stop error = %v", err)
		}
		t.Fatalf("cleanup stop returned error: %s", res.Output)
	}
}

func TestProcessStopTerminatesRunningProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group cleanup not implemented on windows")
	}
	mgr := NewProcessManager(NewPathGuard(t.TempDir(), nil))
	t.Cleanup(func() { _ = mgr.Close(context.Background()) })

	start := executeProcessStart(t, NewProcessStartTool(mgr), map[string]any{
		"command":    "sleep 10",
		"timeout_ms": 10000,
	})
	raw, err := json.Marshal(map[string]any{"process_id": start.ProcessID, "reason": "test stop"})
	if err != nil {
		t.Fatalf("marshal stop: %v", err)
	}
	res, err := NewProcessStopTool(mgr).Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("stop Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("stop returned error: %s", res.Output)
	}
	var stop processStopTestOutput
	if err := json.Unmarshal([]byte(res.Output), &stop); err != nil {
		t.Fatalf("stop output is not JSON: %v\n%s", err, res.Output)
	}
	if !stop.Found || !stop.Stopped || stop.Status != "stopped" || !stop.Terminal {
		t.Fatalf("stop = %+v, want stopped terminal", stop)
	}
	if stop.Receipt.Tool != ProcessStopToolName || stop.Receipt.ExecutionPolicy != "session_process_stop" || stop.Receipt.StopSignal != "SIGTERM" {
		t.Fatalf("stop receipt = %+v", stop.Receipt)
	}
	if stop.Receipt.FollowupTool != ProcessMonitorToolName {
		t.Fatalf("stop followup tool = %q, want %q", stop.Receipt.FollowupTool, ProcessMonitorToolName)
	}
}

func TestProcessManagerCloseStopsRunningChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group cleanup not implemented on windows")
	}
	mgr := NewProcessManager(NewPathGuard(t.TempDir(), nil))

	start := executeProcessStart(t, NewProcessStartTool(mgr), map[string]any{
		"command":    "sleep 10",
		"timeout_ms": 10000,
	})
	if err := mgr.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	mon := executeProcessMonitor(t, NewProcessMonitorTool(mgr), start.ProcessID, 1000)
	if !mon.Found || mon.Status != "stopped" || !mon.Terminal {
		t.Fatalf("monitor after close = %+v, want stopped terminal", mon)
	}
}

func TestProcessToolsAreDeferredInToolSearch(t *testing.T) {
	mgr := NewProcessManager(NewPathGuard(t.TempDir(), nil))
	t.Cleanup(func() { _ = mgr.Close(context.Background()) })
	reg := NewRegistry()
	reg.Register(NewProcessStartTool(mgr))
	reg.Register(NewProcessMonitorTool(mgr))
	reg.Register(NewProcessStopTool(mgr))
	search := NewToolSearchTool(reg)
	reg.Register(search)

	out := executeToolSearch(t, search, `{"query":"process background monitor","max_results":10}`)
	seen := map[string]bool{}
	for _, match := range out.Matches {
		if strings.HasPrefix(match.Name, "process_") {
			seen[match.Name] = true
			if !match.Deferred || match.DeferReason != "tool_declared_deferred" {
				t.Fatalf("process tool match = %+v, want declared deferred", match)
			}
		}
	}
	for _, name := range []string{ProcessStartToolName, ProcessMonitorToolName, ProcessStopToolName} {
		if !seen[name] {
			t.Fatalf("ToolSearch did not return %s; matches=%+v", name, out.Matches)
		}
	}
}
