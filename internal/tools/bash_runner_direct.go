package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// DirectRunner executes shell commands as host processes with the B3a
// guardrails (clean env, bounded output, process group lifecycle). It is
// the default BashRunner backend and the baseline for substrate
// implementations to compare against. No OS-enforced isolation is applied;
// the term "sandbox" must not be used to describe DirectRunner.
type DirectRunner struct {
	killGrace time.Duration
}

// NewDirectRunner constructs a DirectRunner with the standard B3a process
// group kill grace (SIGTERM → wait → SIGKILL).
func NewDirectRunner() *DirectRunner {
	return &DirectRunner{killGrace: bashKillGrace}
}

// Name returns the stable runner identifier used in telemetry slog fields.
func (r *DirectRunner) Name() string { return "direct" }

// Probe always reports Available=true: DirectRunner has no external
// substrate dependency. The message documents the lack of sandboxing so
// callers cannot misread the runner as a security boundary.
func (r *DirectRunner) Probe(_ context.Context) BashRunnerProbe {
	return BashRunnerProbe{
		Available: true,
		Name:      r.Name(),
		Platform:  runtime.GOOS,
		Message:   "host-process command runner with B3a guardrails (no sandbox)",
	}
}

// Close is a no-op for DirectRunner: no temp profiles, no helper processes,
// no namespaces to tear down. Substrate runners override this with their
// own cleanup of runner-lifetime resources.
func (r *DirectRunner) Close(_ context.Context) error { return nil }

// Run executes req.Command and returns a fully-rendered BashRunResult.
// The implementation is the legacy BashTool.Execute body, extracted so the
// substrate insertion point is isolated to a single Runner.
func (r *DirectRunner) Run(ctx context.Context, req BashRunRequest) (BashRunResult, error) {
	cmd := exec.Command(resolveBashShell(), "-c", req.Command)
	cmd.Dir = req.WorkDir
	cmd.Env = cleanBashEnv(os.Environ(), req.SessionDir, req.WorkDir)
	configureProcessCleanup(cmd)

	stdout := newCappedOutput(bashOutputCapPerStream)
	stderr := newCappedOutput(bashOutputCapPerStream)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	start := time.Now()

	if startErr := cmd.Start(); startErr != nil {
		stderr.Write([]byte(fmt.Sprintf("bash start failed: %v", startErr)))
		return r.buildResult(req, time.Since(start), stdout, stderr,
			"error", "unknown_nonzero", nil, false, false), nil
	}

	// Buffered so the Wait goroutine exits cleanly even when the
	// select below returns early on ctx.Done — the value is simply
	// dropped if no one consumes it.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var (
		timedOut    bool
		canceledRun bool
		runErr      error
	)
	select {
	case runErr = <-done:
		// Normal exit. The parent bash is reaped, but a detached
		// background child (`foo &` without `wait`) may still hold
		// the process group. Clean up if anything survived.
		reapOrphanedProcessGroup(cmd, r.killGrace)
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			timedOut = true
		} else {
			canceledRun = true
		}
		_ = terminateProcessGroup(cmd)

		// Wait for cooperative exit up to killGrace before escalating
		// to SIGKILL. Unlike a detached cleanup goroutine this never
		// fires SIGKILL after the group has already exited, so a
		// recycled PGID cannot receive stray signals.
		timer := time.NewTimer(r.killGrace)
		select {
		case runErr = <-done:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			_ = killProcessGroup(cmd)
			runErr = <-done
		}
	}

	duration := time.Since(start)

	// Surface exec-level failures from a normal exit path that produced no
	// stream output (e.g. "bash not found") so the agent has something
	// actionable. Timeout / cancel paths already indicate the cause via
	// metadata, so leave their stderr untouched to preserve partial output.
	if runErr != nil && !timedOut && !canceledRun &&
		stdout.RawBytes() == 0 && stderr.RawBytes() == 0 {
		stderr.Write([]byte(runErr.Error()))
	}

	var (
		status         string
		classification string
		exitCode       *int
	)
	switch {
	case timedOut:
		status, classification = "timeout", "timeout"
	case canceledRun:
		status, classification = "canceled", "canceled"
	case runErr != nil:
		status = "error"
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			ec := exitErr.ExitCode()
			exitCode = &ec
			classification = classifyExitCode(ec)
		} else {
			classification = "unknown_nonzero"
		}
	default:
		status, classification = "success", "success"
		ec := 0
		if cmd.ProcessState != nil {
			ec = cmd.ProcessState.ExitCode()
		}
		exitCode = &ec
	}

	return r.buildResult(req, duration, stdout, stderr,
		status, classification, exitCode, timedOut, canceledRun), nil
}

func (r *DirectRunner) buildResult(
	req BashRunRequest,
	duration time.Duration,
	stdout, stderr *cappedOutput,
	status, classification string,
	exitCode *int,
	timedOut, canceledRun bool,
) BashRunResult {
	isError := status != "success"
	meta := bashResultMeta{
		Status:           status,
		ExitCode:         exitCode,
		Duration:         duration,
		CWD:              req.DisplayCWD,
		TimedOut:         timedOut,
		Canceled:         canceledRun,
		StdoutRawBytes:   stdout.RawBytes(),
		StdoutShownBytes: int64(stdout.Kept()),
		StdoutTruncated:  stdout.Truncated(),
		StderrRawBytes:   stderr.RawBytes(),
		StderrShownBytes: int64(stderr.Kept()),
		StderrTruncated:  stderr.Truncated(),
		Classification:   classification,
	}
	return BashRunResult{
		Output:          formatBashResult(meta, stdout, stderr),
		IsError:         isError,
		ExitCode:        exitCode,
		Duration:        duration,
		CWD:             req.DisplayCWD,
		TimedOut:        timedOut,
		Canceled:        canceledRun,
		StdoutRawBytes:  stdout.RawBytes(),
		StderrRawBytes:  stderr.RawBytes(),
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
		Classification:  classification,
		Violations:      nil,
	}
}
