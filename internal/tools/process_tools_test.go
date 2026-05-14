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
		CommandIntent   string `json:"command_intent"`
		IntentSource    string `json:"intent_source"`
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
	CommandIntent   string `json:"command_intent"`
	IntentSource    string `json:"intent_source"`
	TimedOut        bool   `json:"timed_out"`
	TimeoutMS       int    `json:"timeout_ms"`
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
		CommandIntent   string `json:"command_intent"`
		IntentSource    string `json:"intent_source"`
		ProcessID       int64  `json:"process_id"`
		Status          string `json:"status"`
		Terminal        bool   `json:"terminal"`
		TimedOut        bool   `json:"timed_out"`
		TimeoutMS       int    `json:"timeout_ms"`
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

type processWaitTestOutput struct {
	ProcessID     int64  `json:"process_id"`
	Found         bool   `json:"found"`
	Status        string `json:"status"`
	Terminal      bool   `json:"terminal"`
	CommandIntent string `json:"command_intent"`
	IntentSource  string `json:"intent_source"`
	TimedOut      bool   `json:"timed_out"`
	TimeoutMS     int    `json:"timeout_ms"`
	ExitCode      *int   `json:"exit_code"`
	StdoutTail    string `json:"stdout_tail"`
	StderrTail    string `json:"stderr_tail"`
	WaitMS        int    `json:"wait_ms"`
	WaitElapsedMS int    `json:"wait_elapsed_ms"`
	WaitTimedOut  bool   `json:"wait_timed_out"`
	Receipt       struct {
		Tool            string `json:"tool"`
		Action          string `json:"action"`
		ReadOnly        bool   `json:"read_only"`
		Persistent      bool   `json:"persistent"`
		ExecutionPolicy string `json:"execution_policy"`
		CommandIntent   string `json:"command_intent"`
		IntentSource    string `json:"intent_source"`
		ProcessID       int64  `json:"process_id"`
		Status          string `json:"status"`
		Terminal        bool   `json:"terminal"`
		TimedOut        bool   `json:"timed_out"`
		TimeoutMS       int    `json:"timeout_ms"`
		ExitCode        *int   `json:"exit_code"`
		Found           bool   `json:"found"`
		TailBytes       int    `json:"tail_bytes"`
		WaitMS          int    `json:"wait_ms"`
		WaitElapsedMS   int    `json:"wait_elapsed_ms"`
		WaitTimedOut    bool   `json:"wait_timed_out"`
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
		CommandIntent   string `json:"command_intent"`
		IntentSource    string `json:"intent_source"`
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

func executeProcessWait(t *testing.T, tool *ProcessWaitTool, id int64, waitMS int, maxChars int) processWaitTestOutput {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"process_id": id, "wait_ms": waitMS, "max_chars": maxChars})
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
	var out processWaitTestOutput
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
		"intent":     "focused_verify",
	})
	if start.ProcessID <= 0 || start.Status != "running" {
		t.Fatalf("start = %+v, want running process id", start)
	}
	if start.Receipt.Tool != ProcessStartToolName || start.Receipt.ExecutionPolicy != "session_process_start" {
		t.Fatalf("start receipt = %+v", start.Receipt)
	}
	if start.Receipt.CommandIntent != "focused_verify" || start.Receipt.IntentSource != "explicit" {
		t.Fatalf("start receipt intent = %+v", start.Receipt)
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
	if mon.CommandIntent != "focused_verify" || mon.IntentSource != "explicit" || mon.Receipt.CommandIntent != "focused_verify" || mon.Receipt.IntentSource != "explicit" {
		t.Fatalf("monitor intent = %+v receipt=%+v", mon, mon.Receipt)
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
	if start.Receipt.CommandIntent != "background" || start.Receipt.IntentSource != "default" {
		t.Fatalf("start receipt intent = %+v, want background/default", start.Receipt)
	}
	mon := executeProcessMonitor(t, NewProcessMonitorTool(mgr), start.ProcessID, 1000)
	if !mon.Found || mon.Status != "running" || mon.Terminal {
		t.Fatalf("monitor = %+v, want running non-terminal process", mon)
	}
	if mon.CommandIntent != "background" || mon.IntentSource != "default" || mon.Receipt.CommandIntent != "background" || mon.Receipt.IntentSource != "default" {
		t.Fatalf("monitor intent = %+v receipt=%+v, want background/default", mon, mon.Receipt)
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

func TestProcessToolsReportTimeoutMonitorReceipt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group cleanup not implemented on windows")
	}
	mgr := NewProcessManager(NewPathGuard(t.TempDir(), nil))
	t.Cleanup(func() { _ = mgr.Close(context.Background()) })

	start := executeProcessStart(t, NewProcessStartTool(mgr), map[string]any{
		"command":    "sleep 10",
		"timeout_ms": 50,
		"intent":     "focused_verify",
	})
	mon := waitProcessTerminal(t, NewProcessMonitorTool(mgr), start.ProcessID)
	if mon.Status != "timeout" || !mon.Terminal || !mon.TimedOut {
		t.Fatalf("monitor = %+v, want terminal timeout with timed_out=true", mon)
	}
	if mon.TimeoutMS != 50 {
		t.Fatalf("monitor timeout_ms = %d, want 50", mon.TimeoutMS)
	}
	if mon.Receipt.Status != "timeout" || !mon.Receipt.Terminal || !mon.Receipt.TimedOut {
		t.Fatalf("monitor receipt = %+v, want terminal timeout with timed_out=true", mon.Receipt)
	}
	if mon.Receipt.TimeoutMS != 50 {
		t.Fatalf("monitor receipt timeout_ms = %d, want 50", mon.Receipt.TimeoutMS)
	}
	if mon.Receipt.FollowupTool != "" {
		t.Fatalf("timeout monitor followup tool = %q, want empty", mon.Receipt.FollowupTool)
	}
}

func TestProcessWaitReturnsTerminalSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group cleanup not implemented on windows")
	}
	mgr := NewProcessManager(NewPathGuard(t.TempDir(), nil))
	t.Cleanup(func() { _ = mgr.Close(context.Background()) })

	start := executeProcessStart(t, NewProcessStartTool(mgr), map[string]any{
		"command":    "printf done",
		"timeout_ms": 2000,
		"intent":     "diagnostic",
	})
	wait := executeProcessWait(t, NewProcessWaitTool(mgr), start.ProcessID, 1000, 1000)
	if !wait.Found || wait.Status != "completed" || !wait.Terminal || wait.WaitTimedOut {
		t.Fatalf("wait = %+v, want completed terminal without wait timeout", wait)
	}
	if wait.ExitCode == nil || *wait.ExitCode != 0 || !strings.Contains(wait.StdoutTail, "done") {
		t.Fatalf("wait exit/output = exit:%v stdout:%q", wait.ExitCode, wait.StdoutTail)
	}
	if wait.CommandIntent != "diagnostic" || wait.IntentSource != "explicit" || wait.Receipt.CommandIntent != "diagnostic" || wait.Receipt.IntentSource != "explicit" {
		t.Fatalf("wait intent = %+v receipt=%+v", wait, wait.Receipt)
	}
	if wait.Receipt.Tool != ProcessWaitToolName || wait.Receipt.Action != "wait" || wait.Receipt.ExecutionPolicy != "session_process_wait" {
		t.Fatalf("wait receipt identity = %+v", wait.Receipt)
	}
	if !wait.Receipt.ReadOnly || wait.Receipt.Persistent || !wait.Receipt.Found || !wait.Receipt.Terminal || wait.Receipt.WaitTimedOut {
		t.Fatalf("wait receipt flags = %+v", wait.Receipt)
	}
	if wait.Receipt.WaitMS != 1000 || wait.Receipt.WaitElapsedMS < 0 || wait.Receipt.FollowupTool != "" {
		t.Fatalf("wait receipt wait/followup = %+v", wait.Receipt)
	}
}

func TestProcessWaitReportsRunningWhenWaitExpires(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group cleanup not implemented on windows")
	}
	mgr := NewProcessManager(NewPathGuard(t.TempDir(), nil))
	t.Cleanup(func() { _ = mgr.Close(context.Background()) })

	start := executeProcessStart(t, NewProcessStartTool(mgr), map[string]any{
		"command":    "sleep 10",
		"timeout_ms": 10000,
		"intent":     "focused_verify",
	})
	wait := executeProcessWait(t, NewProcessWaitTool(mgr), start.ProcessID, 20, 1000)
	if !wait.Found || wait.Status != "running" || wait.Terminal || !wait.WaitTimedOut || wait.TimedOut {
		t.Fatalf("wait = %+v, want running non-terminal wait timeout", wait)
	}
	if wait.Receipt.Tool != ProcessWaitToolName || !wait.Receipt.ReadOnly || wait.Receipt.Persistent || !wait.Receipt.WaitTimedOut {
		t.Fatalf("wait receipt = %+v, want read-only timed-out wait", wait.Receipt)
	}
	if wait.Receipt.FollowupTool != ProcessWaitToolName {
		t.Fatalf("wait followup tool = %q, want %q", wait.Receipt.FollowupTool, ProcessWaitToolName)
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

func TestProcessExecutionPolicySnapshot(t *testing.T) {
	policy := ProcessExecutionPolicySnapshot()
	if policy.DefaultTimeoutMS != 600000 || policy.MaxTimeoutMS != 3600000 || policy.KillGraceMS != 2000 {
		t.Fatalf("timeout policy = %+v, want process default/max/kill-grace milliseconds", policy)
	}
	if policy.DefaultWaitMS != 1000 || policy.MaxWaitMS != 60000 {
		t.Fatalf("wait policy = %+v, want default/max wait milliseconds", policy)
	}
	if policy.DefaultTailBytes != processDefaultTail || policy.MaxTailBytes != processMaxTail {
		t.Fatalf("tail policy = %+v, want default/max tail bytes", policy)
	}
	wantFields := []string{"status", "terminal", "timed_out", "timeout_ms", "followup_tool", "command_intent", "intent_source", "wait_ms", "wait_elapsed_ms", "wait_timed_out"}
	for _, field := range wantFields {
		found := false
		for _, got := range policy.ReceiptFields {
			if got == field {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("receipt_fields = %v, missing %q", policy.ReceiptFields, field)
		}
	}
	if policy.MonitorFollowupTool != ProcessMonitorToolName {
		t.Fatalf("monitor_followup_tool = %q, want %q", policy.MonitorFollowupTool, ProcessMonitorToolName)
	}
	if policy.WaitFollowupTool != ProcessWaitToolName {
		t.Fatalf("wait_followup_tool = %q, want %q", policy.WaitFollowupTool, ProcessWaitToolName)
	}
}

func TestProcessStartRejectsInvalidCommandIntent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group cleanup not implemented on windows")
	}
	mgr := NewProcessManager(NewPathGuard(t.TempDir(), nil))
	t.Cleanup(func() { _ = mgr.Close(context.Background()) })

	raw, err := json.Marshal(map[string]any{
		"command": "echo no",
		"intent":  "surprise",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	res, err := NewProcessStartTool(mgr).Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !res.IsError || !strings.Contains(res.Output, "invalid command intent") {
		t.Fatalf("result = %+v, want invalid command intent error", res)
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
	reg.Register(NewProcessWaitTool(mgr))
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
	for _, name := range []string{ProcessStartToolName, ProcessMonitorToolName, ProcessWaitToolName, ProcessStopToolName} {
		if !seen[name] {
			t.Fatalf("ToolSearch did not return %s; matches=%+v", name, out.Matches)
		}
	}
}
