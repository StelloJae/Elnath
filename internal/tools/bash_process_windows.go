//go:build windows

package tools

import (
	"os/exec"
	"time"
)

// configureProcessCleanup is a no-op on Windows. Proper process-tree
// cleanup requires wiring the child into a Windows Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE; until that lands the bash tool
// only terminates the direct child on cancel.
// TODO(v41): Job Object based tree cleanup.
func configureProcessCleanup(cmd *exec.Cmd) {
	_ = cmd
}

// terminateProcessGroup performs a best-effort direct kill of the
// bash child. Grandchildren are not reached — see the TODO on
// configureProcessCleanup.
func terminateProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// killProcessGroup is identical to terminateProcessGroup on Windows
// today; the future Job Object implementation will distinguish them.
func killProcessGroup(cmd *exec.Cmd) error {
	return terminateProcessGroup(cmd)
}

// processGroupAlive conservatively reports the group as gone once the
// direct child has exited. Without Job Objects we cannot reliably
// enumerate orphaned grandchildren, so callers should not rely on
// this path for true tree cleanup.
func processGroupAlive(cmd *exec.Cmd) bool {
	return false
}

// reapOrphanedProcessGroup is effectively a no-op on Windows because
// processGroupAlive always returns false. The grace parameter is
// retained so the caller's signature remains cross-platform.
func reapOrphanedProcessGroup(cmd *exec.Cmd, grace time.Duration) {
	_ = cmd
	_ = grace
}
