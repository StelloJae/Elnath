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

// terminateProcessTree sends a direct kill to the bash child.
// Grandchildren are not reached — see the configureProcessCleanup
// TODO. The grace parameter is unused today; the future Job Object
// implementation will honor it.
func terminateProcessTree(cmd *exec.Cmd, grace time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = grace
}
