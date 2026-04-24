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

// terminateProcessGroup sends SIGTERM to the command's process group.
// ESRCH (group already gone) is swallowed because the caller cannot
// act on it — any other error is returned for diagnostic logging.
func terminateProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

// killProcessGroup sends SIGKILL to the command's process group.
// ESRCH is treated the same as in terminateProcessGroup.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

// processGroupAlive probes the process group via signal 0. It returns
// false once every member of the group has exited (ESRCH) and true
// while at least one remains. Used both to avoid spurious SIGKILL
// delivery after the group is gone and to detect background children
// that survived a normal parent exit.
func processGroupAlive(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}
	return syscall.Kill(-cmd.Process.Pid, 0) == nil
}

// reapOrphanedProcessGroup terminates any background members of the
// command's process group after a normal parent exit. If the group is
// already empty this is a no-op. Otherwise SIGTERM is sent, the group
// is polled for up to `grace` to let cooperative children shut down,
// and any survivors are SIGKILLed. All ESRCH responses are benign.
func reapOrphanedProcessGroup(cmd *exec.Cmd, grace time.Duration) {
	if !processGroupAlive(cmd) {
		return
	}
	_ = terminateProcessGroup(cmd)

	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !processGroupAlive(cmd) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = killProcessGroup(cmd)
}
