package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	ProcessStartToolName   = "process_start"
	ProcessMonitorToolName = "process_monitor"
	ProcessStopToolName    = "process_stop"

	processDefaultTimeout = 10 * time.Minute
	processMaxTimeout     = 60 * time.Minute
	processKillGrace      = 2 * time.Second
	processDefaultTail    = 4000
	processMaxTail        = 20000
	processOutputCap      = bashOutputCapPerStream
	processPreviewRunes   = 240
)

type ProcessManager struct {
	guard     *PathGuard
	killGrace time.Duration

	mu        sync.RWMutex
	nextID    int64
	processes map[int64]*managedProcess
	closed    bool
}

func NewProcessManager(guard *PathGuard) *ProcessManager {
	return &ProcessManager{
		guard:     guard,
		killGrace: processKillGrace,
		processes: make(map[int64]*managedProcess),
	}
}

func (m *ProcessManager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.closed = true
	processes := make([]*managedProcess, 0, len(m.processes))
	for _, proc := range m.processes {
		processes = append(processes, proc)
	}
	m.mu.Unlock()

	var firstErr error
	for _, proc := range processes {
		if err := proc.stop(ctx, m.killGrace, "manager close"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *ProcessManager) start(ctx context.Context, input processStartInput) (*managedProcess, error) {
	if m == nil || m.guard == nil {
		return nil, errors.New("process_start: manager unavailable")
	}
	command := strings.TrimSpace(input.Command)
	if command == "" {
		return nil, errors.New("process_start: command is required")
	}
	if dangerous, reason := AnalyzeCommandSafety(command); dangerous {
		return nil, fmt.Errorf("process_start: command blocked: %s", reason)
	}

	timeout := normalizeProcessTimeout(input.TimeoutMS)
	sessionDir, err := SessionWorkDirFromContext(ctx, m.guard)
	if err != nil {
		return nil, fmt.Errorf("process_start: session workspace: %w", err)
	}
	sessionDir, err = filepath.EvalSymlinks(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("process_start: resolve session root: %w", err)
	}
	workDir := sessionDir
	if strings.TrimSpace(input.WorkingDir) != "" {
		resolved, err := m.guard.ResolveWorkingDir(sessionDir, input.WorkingDir)
		if err != nil {
			return nil, fmt.Errorf("process_start: invalid working_dir: %w", err)
		}
		workDir = resolved
	}
	displayCWD := displayCWD(sessionDir, workDir)
	tmpDir := filepath.Join(sessionDir, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return nil, fmt.Errorf("process_start: prepare session tmp: %w", err)
	}

	procCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.Command(resolveBashShell(), "-c", command)
	cmd.Dir = workDir
	cmd.Env = cleanBashEnv(os.Environ(), sessionDir, workDir, "")
	configureProcessCleanup(cmd)

	proc := &managedProcess{
		command:     command,
		description: strings.TrimSpace(input.Description),
		cwd:         displayCWD,
		status:      processStatusRunning,
		timeout:     timeout,
		startedAt:   time.Now(),
		cmd:         cmd,
		cancel:      cancel,
		ctx:         procCtx,
		stdout:      newProcessOutputBuffer(processOutputCap),
		stderr:      newProcessOutputBuffer(processOutputCap),
	}
	cmd.Stdout = proc.stdout
	cmd.Stderr = proc.stderr

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		return nil, errors.New("process_start: manager closed")
	}
	m.nextID++
	id := m.nextID
	proc.id = id
	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("process_start: start failed: %w", err)
	}
	m.processes[id] = proc
	m.mu.Unlock()

	go proc.wait(timeout, m.killGrace)
	return proc, nil
}

func (m *ProcessManager) get(id int64) (*managedProcess, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	proc, ok := m.processes[id]
	return proc, ok
}

type processStatus string

const (
	processStatusRunning   processStatus = "running"
	processStatusCompleted processStatus = "completed"
	processStatusFailed    processStatus = "failed"
	processStatusTimeout   processStatus = "timeout"
	processStatusStopped   processStatus = "stopped"
)

type managedProcess struct {
	mu sync.RWMutex

	id          int64
	command     string
	description string
	cwd         string
	status      processStatus
	exitCode    *int
	timedOut    bool
	stopped     bool
	stopReason  string
	timeout     time.Duration
	startedAt   time.Time
	completedAt time.Time

	cmd    *exec.Cmd
	cancel context.CancelFunc
	ctx    context.Context
	stdout *processOutputBuffer
	stderr *processOutputBuffer
}

func (p *managedProcess) wait(timeout, killGrace time.Duration) {
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var runErr error
	var timedOut, stopped bool
	select {
	case runErr = <-done:
		reapOrphanedProcessGroup(p.cmd, killGrace)
	case <-timer.C:
		timedOut = true
		runErr = terminateAndWaitProcess(p.cmd, done, killGrace)
	case <-p.ctx.Done():
		stopped = true
		runErr = terminateAndWaitProcess(p.cmd, done, killGrace)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	processState := p.cmd.ProcessState
	p.completedAt = time.Now()
	p.cmd = nil
	p.cancel = nil
	p.timedOut = timedOut
	if stopped {
		p.stopped = true
		if p.stopReason == "" {
			p.stopReason = "stop requested"
		}
	}
	switch {
	case timedOut:
		p.status = processStatusTimeout
	case stopped:
		p.status = processStatusStopped
	case runErr != nil:
		p.status = processStatusFailed
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			ec := exitErr.ExitCode()
			p.exitCode = &ec
		}
	default:
		p.status = processStatusCompleted
		ec := 0
		if processState != nil {
			ec = processState.ExitCode()
		}
		p.exitCode = &ec
	}
}

func terminateAndWaitProcess(cmd *exec.Cmd, done <-chan error, grace time.Duration) error {
	_ = terminateProcessGroup(cmd)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		_ = killProcessGroup(cmd)
		return <-done
	}
}

func (p *managedProcess) stop(ctx context.Context, grace time.Duration, reason string) error {
	p.mu.Lock()
	if isTerminalProcessStatus(p.status) {
		p.mu.Unlock()
		return nil
	}
	if strings.TrimSpace(reason) != "" {
		p.stopReason = strings.TrimSpace(reason)
	}
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	deadline := time.NewTimer(grace + 500*time.Millisecond)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		p.mu.RLock()
		terminal := isTerminalProcessStatus(p.status)
		p.mu.RUnlock()
		if terminal {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("process_stop: timeout waiting for process to stop")
		case <-ticker.C:
		}
	}
}

func (p *managedProcess) snapshot(maxChars int) processSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	stdout, stdoutRaw, stdoutTruncated := p.stdout.tail(maxChars)
	stderr, stderrRaw, stderrTruncated := p.stderr.tail(maxChars)
	out := processSnapshot{
		ProcessID:       p.id,
		Found:           true,
		Status:          string(p.status),
		Terminal:        isTerminalProcessStatus(p.status),
		ExitCode:        p.exitCode,
		CWD:             p.cwd,
		CommandPreview:  truncateProcessText(p.command, processPreviewRunes),
		StartedAt:       p.startedAt.Format(time.RFC3339Nano),
		StdoutTail:      stdout,
		StderrTail:      stderr,
		StdoutRawBytes:  stdoutRaw,
		StderrRawBytes:  stderrRaw,
		StdoutTruncated: stdoutTruncated,
		StderrTruncated: stderrTruncated,
	}
	if !p.completedAt.IsZero() {
		out.CompletedAt = p.completedAt.Format(time.RFC3339Nano)
	}
	ref := p.completedAt
	if ref.IsZero() {
		ref = time.Now()
	}
	out.RunningSeconds = int64(ref.Sub(p.startedAt).Seconds())
	return out
}

func isTerminalProcessStatus(status processStatus) bool {
	switch status {
	case processStatusCompleted, processStatusFailed, processStatusTimeout, processStatusStopped:
		return true
	default:
		return false
	}
}

type processOutputBuffer struct {
	mu       sync.Mutex
	capacity int
	raw      int64
	data     []byte
}

func newProcessOutputBuffer(capacity int) *processOutputBuffer {
	return &processOutputBuffer{capacity: capacity}
}

func (b *processOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.raw += int64(len(p))
	b.data = append(b.data, p...)
	if len(b.data) > b.capacity {
		b.data = append([]byte(nil), b.data[len(b.data)-b.capacity:]...)
	}
	return len(p), nil
}

func (b *processOutputBuffer) tail(maxChars int) (string, int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if maxChars <= 0 {
		maxChars = processDefaultTail
	}
	if maxChars > processMaxTail {
		maxChars = processMaxTail
	}
	data := b.data
	truncated := b.raw > int64(len(data))
	if len(data) > maxChars {
		data = data[len(data)-maxChars:]
		truncated = true
	}
	return string(data), b.raw, truncated
}

type ProcessStartTool struct {
	manager *ProcessManager
}

func NewProcessStartTool(manager *ProcessManager) *ProcessStartTool {
	return &ProcessStartTool{manager: manager}
}

func (t *ProcessStartTool) Name() string { return ProcessStartToolName }

func (t *ProcessStartTool) Description() string {
	return "Start a bounded session-local background shell process for later monitoring"
}

func (t *ProcessStartTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"command":     String("Shell command to start in the background."),
		"description": String("Short human description of the process."),
		"timeout_ms":  Int("Maximum runtime in milliseconds. Defaults to 600000 and caps at 3600000."),
		"working_dir": String("Working directory for the command."),
	}, []string{"command"})
}

func (t *ProcessStartTool) IsConcurrencySafe(json.RawMessage) bool { return false }
func (t *ProcessStartTool) Reversible() bool                       { return false }
func (t *ProcessStartTool) Scope(json.RawMessage) ToolScope {
	return ToolScope{Network: true, Persistent: true}
}
func (t *ProcessStartTool) ShouldCancelSiblingsOnError() bool { return false }
func (t *ProcessStartTool) DeferInitialToolSchema() bool      { return true }

type processStartInput struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	TimeoutMS   int    `json:"timeout_ms"`
	WorkingDir  string `json:"working_dir"`
}

type processStartOutput struct {
	ProcessID      int64          `json:"process_id"`
	Status         string         `json:"status"`
	Terminal       bool           `json:"terminal"`
	CWD            string         `json:"cwd"`
	TimeoutMS      int            `json:"timeout_ms"`
	CommandPreview string         `json:"command_preview"`
	Receipt        processReceipt `json:"receipt"`
}

func (t *ProcessStartTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var input processStartInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	proc, err := t.manager.start(ctx, input)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}
	snap := proc.snapshot(processDefaultTail)
	out := processStartOutput{
		ProcessID:      proc.id,
		Status:         snap.Status,
		Terminal:       snap.Terminal,
		CWD:            snap.CWD,
		TimeoutMS:      int(proc.timeout / time.Millisecond),
		CommandPreview: snap.CommandPreview,
		Receipt: processReceipt{
			Tool:            ProcessStartToolName,
			Action:          "start",
			ReadOnly:        false,
			Persistent:      true,
			ExecutionPolicy: "session_process_start",
			ProcessID:       proc.id,
			Status:          snap.Status,
			Terminal:        snap.Terminal,
			TimeoutMS:       int(proc.timeout / time.Millisecond),
			CWD:             snap.CWD,
			FollowupTool:    processFollowupTool(ProcessStartToolName, true, snap.Terminal),
		},
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return ErrorResult(fmt.Sprintf("process_start: marshal output: %v", err)), nil
	}
	return SuccessResult(string(raw)), nil
}

type ProcessMonitorTool struct {
	manager *ProcessManager
}

func NewProcessMonitorTool(manager *ProcessManager) *ProcessMonitorTool {
	return &ProcessMonitorTool{manager: manager}
}

func (t *ProcessMonitorTool) Name() string { return ProcessMonitorToolName }

func (t *ProcessMonitorTool) Description() string {
	return "Read a snapshot and bounded output tails for a session-local background process"
}

func (t *ProcessMonitorTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"process_id": Int("Process id returned by process_start."),
		"max_chars":  Int("Maximum trailing characters per stream. Defaults to 4000 and caps at 20000."),
	}, []string{"process_id"})
}

func (t *ProcessMonitorTool) IsConcurrencySafe(json.RawMessage) bool { return true }
func (t *ProcessMonitorTool) Reversible() bool                       { return true }
func (t *ProcessMonitorTool) Scope(json.RawMessage) ToolScope        { return ToolScope{} }
func (t *ProcessMonitorTool) ShouldCancelSiblingsOnError() bool      { return false }
func (t *ProcessMonitorTool) DeferInitialToolSchema() bool           { return true }

type processMonitorInput struct {
	ProcessID int64 `json:"process_id"`
	MaxChars  int   `json:"max_chars"`
}

type processMonitorOutput struct {
	processSnapshot
	Receipt processReceipt `json:"receipt"`
}

func (t *ProcessMonitorTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var input processMonitorInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.ProcessID <= 0 {
		return ErrorResult("process_monitor: process_id must be positive"), nil
	}
	limit := normalizeProcessTail(input.MaxChars)
	proc, found := t.manager.get(input.ProcessID)
	if !found {
		out := processMonitorOutput{
			processSnapshot: processSnapshot{ProcessID: input.ProcessID, Found: false},
			Receipt: processReceipt{
				Tool:            ProcessMonitorToolName,
				Action:          "monitor",
				ReadOnly:        true,
				Persistent:      false,
				ExecutionPolicy: "session_process_observation",
				ProcessID:       input.ProcessID,
				Found:           false,
				TailBytes:       limit,
			},
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return ErrorResult(fmt.Sprintf("process_monitor: marshal output: %v", err)), nil
		}
		return SuccessResult(string(raw)), nil
	}
	snap := proc.snapshot(limit)
	out := processMonitorOutput{
		processSnapshot: snap,
		Receipt: processReceipt{
			Tool:            ProcessMonitorToolName,
			Action:          "monitor",
			ReadOnly:        true,
			Persistent:      false,
			ExecutionPolicy: "session_process_observation",
			ProcessID:       input.ProcessID,
			Status:          snap.Status,
			Terminal:        snap.Terminal,
			ExitCode:        snap.ExitCode,
			Found:           true,
			TailBytes:       limit,
			StdoutRawBytes:  snap.StdoutRawBytes,
			StderrRawBytes:  snap.StderrRawBytes,
			StdoutTruncated: snap.StdoutTruncated,
			StderrTruncated: snap.StderrTruncated,
			CWD:             snap.CWD,
			FollowupTool:    processFollowupTool(ProcessMonitorToolName, true, snap.Terminal),
		},
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return ErrorResult(fmt.Sprintf("process_monitor: marshal output: %v", err)), nil
	}
	return SuccessResult(string(raw)), nil
}

type ProcessStopTool struct {
	manager *ProcessManager
}

func NewProcessStopTool(manager *ProcessManager) *ProcessStopTool {
	return &ProcessStopTool{manager: manager}
}

func (t *ProcessStopTool) Name() string { return ProcessStopToolName }

func (t *ProcessStopTool) Description() string {
	return "Stop a running session-local background process"
}

func (t *ProcessStopTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"process_id": Int("Process id returned by process_start."),
		"reason":     String("Optional stop reason."),
	}, []string{"process_id"})
}

func (t *ProcessStopTool) IsConcurrencySafe(json.RawMessage) bool { return false }
func (t *ProcessStopTool) Reversible() bool                       { return false }
func (t *ProcessStopTool) Scope(json.RawMessage) ToolScope {
	return ToolScope{Persistent: true}
}
func (t *ProcessStopTool) ShouldCancelSiblingsOnError() bool { return false }
func (t *ProcessStopTool) DeferInitialToolSchema() bool      { return true }

type processStopInput struct {
	ProcessID int64  `json:"process_id"`
	Reason    string `json:"reason"`
}

type processStopOutput struct {
	ProcessID int64          `json:"process_id"`
	Found     bool           `json:"found"`
	Stopped   bool           `json:"stopped"`
	Status    string         `json:"status"`
	Terminal  bool           `json:"terminal"`
	Reason    string         `json:"reason,omitempty"`
	Receipt   processReceipt `json:"receipt"`
}

func (t *ProcessStopTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var input processStopInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.ProcessID <= 0 {
		return ErrorResult("process_stop: process_id must be positive"), nil
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "process_stop requested"
	}
	proc, found := t.manager.get(input.ProcessID)
	if !found {
		out := processStopOutput{
			ProcessID: input.ProcessID,
			Found:     false,
			Receipt: processReceipt{
				Tool:            ProcessStopToolName,
				Action:          "stop",
				ReadOnly:        false,
				Persistent:      true,
				ExecutionPolicy: "session_process_stop",
				ProcessID:       input.ProcessID,
				Found:           false,
			},
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return ErrorResult(fmt.Sprintf("process_stop: marshal output: %v", err)), nil
		}
		return SuccessResult(string(raw)), nil
	}
	alreadyTerminal := proc.snapshot(processDefaultTail).Terminal
	if err := proc.stop(ctx, processKillGrace, reason); err != nil {
		return ErrorResult(err.Error()), nil
	}
	snap := proc.snapshot(processDefaultTail)
	stopSignal := ""
	if !alreadyTerminal {
		stopSignal = "SIGTERM"
	}
	out := processStopOutput{
		ProcessID: input.ProcessID,
		Found:     true,
		Stopped:   !alreadyTerminal,
		Status:    snap.Status,
		Terminal:  snap.Terminal,
		Reason:    reason,
		Receipt: processReceipt{
			Tool:            ProcessStopToolName,
			Action:          "stop",
			ReadOnly:        false,
			Persistent:      true,
			ExecutionPolicy: "session_process_stop",
			ProcessID:       input.ProcessID,
			Status:          snap.Status,
			Terminal:        snap.Terminal,
			Found:           true,
			StopSignal:      stopSignal,
			CWD:             snap.CWD,
			FollowupTool:    processFollowupTool(ProcessStopToolName, true, snap.Terminal),
		},
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return ErrorResult(fmt.Sprintf("process_stop: marshal output: %v", err)), nil
	}
	return SuccessResult(string(raw)), nil
}

type processSnapshot struct {
	ProcessID       int64  `json:"process_id"`
	Found           bool   `json:"found"`
	Status          string `json:"status"`
	Terminal        bool   `json:"terminal"`
	ExitCode        *int   `json:"exit_code,omitempty"`
	CWD             string `json:"cwd,omitempty"`
	CommandPreview  string `json:"command_preview,omitempty"`
	StartedAt       string `json:"started_at,omitempty"`
	CompletedAt     string `json:"completed_at,omitempty"`
	RunningSeconds  int64  `json:"running_seconds"`
	StdoutTail      string `json:"stdout_tail"`
	StderrTail      string `json:"stderr_tail"`
	StdoutRawBytes  int64  `json:"stdout_raw_bytes"`
	StderrRawBytes  int64  `json:"stderr_raw_bytes"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
}

type processReceipt struct {
	Tool            string `json:"tool"`
	Action          string `json:"action"`
	ReadOnly        bool   `json:"read_only"`
	Persistent      bool   `json:"persistent"`
	ExecutionPolicy string `json:"execution_policy"`
	ProcessID       int64  `json:"process_id,omitempty"`
	Status          string `json:"status,omitempty"`
	Terminal        bool   `json:"terminal,omitempty"`
	ExitCode        *int   `json:"exit_code,omitempty"`
	TimeoutMS       int    `json:"timeout_ms,omitempty"`
	CWD             string `json:"cwd,omitempty"`
	TailBytes       int    `json:"tail_bytes,omitempty"`
	StdoutRawBytes  int64  `json:"stdout_raw_bytes,omitempty"`
	StderrRawBytes  int64  `json:"stderr_raw_bytes,omitempty"`
	StdoutTruncated bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated bool   `json:"stderr_truncated,omitempty"`
	StopSignal      string `json:"stop_signal,omitempty"`
	Found           bool   `json:"found,omitempty"`
	FollowupTool    string `json:"followup_tool,omitempty"`
}

func processFollowupTool(tool string, found bool, terminal bool) string {
	switch tool {
	case ProcessStartToolName:
		return ProcessMonitorToolName
	case ProcessMonitorToolName:
		if found && !terminal {
			return ProcessMonitorToolName
		}
	case ProcessStopToolName:
		if found {
			return ProcessMonitorToolName
		}
	}
	return ""
}

func normalizeProcessTimeout(ms int) time.Duration {
	if ms <= 0 {
		return processDefaultTimeout
	}
	d := time.Duration(ms) * time.Millisecond
	if d > processMaxTimeout {
		return processMaxTimeout
	}
	return d
}

func normalizeProcessTail(maxChars int) int {
	if maxChars <= 0 {
		return processDefaultTail
	}
	if maxChars > processMaxTail {
		return processMaxTail
	}
	return maxChars
}

func truncateProcessText(s string, max int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= max {
		return string(rs)
	}
	if max <= 3 {
		return string(rs[:max])
	}
	return string(rs[:max-3]) + "..."
}
