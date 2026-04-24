//go:build unix

package tools

import (
	"os/exec"
	"syscall"
	"time"
)

// configureProcessCleanup places the command in its own process
// group so the entire tree can be signaled as a unit when the bash
// tool needs to cancel the command. On Windows this helper is a
// no-op and a TODO tracks implementing Job Object cleanup.
func configureProcessCleanup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// terminateProcessTree sends SIGTERM to the command's process group
// and schedules an unconditional SIGKILL after `grace`. The caller
// is expected to already be waiting on cmd.Wait() so the process
// will be reaped as soon as the group exits; the kill escalation
// runs in a background goroutine so the caller does not block on
// grace when SIGTERM is enough.
func terminateProcessTree(cmd *exec.Cmd, grace time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	go func() {
		time.Sleep(grace)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}()
}
