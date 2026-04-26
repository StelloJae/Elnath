//go:build !linux

package tools

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

// BwrapRunner is a stub on non-linux platforms. The factory must
// surface the unsupported diagnostic via Probe rather than silently
// substituting DirectRunner — silent fallback would defeat the purpose
// of asking for a sandbox.
type BwrapRunner struct {
	killGrace time.Duration
}

func NewBwrapRunner() *BwrapRunner {
	r, _ := NewBwrapRunnerWithAllowlist(nil)
	return r
}

// NewBwrapRunnerWithAllowlist mirrors the linux constructor signature
// so cross-platform factory code stays substrate-agnostic. On
// non-linux the allowlist is ignored and the stub Probe reports
// Available=false.
func NewBwrapRunnerWithAllowlist(_ []string) (*BwrapRunner, error) {
	return &BwrapRunner{killGrace: bashKillGrace}, nil
}

func (r *BwrapRunner) Name() string { return "bwrap" }

func (r *BwrapRunner) Close(_ context.Context) error { return nil }

func (r *BwrapRunner) Probe(_ context.Context) BashRunnerProbe {
	return BashRunnerProbe{
		Available:          false,
		Name:               r.Name(),
		Platform:           runtime.GOOS,
		Message:            "linux_bwrap requires linux",
		ExecutionMode:      "linux_bwrap",
		PolicyName:         "bwrap",
		FilesystemEnforced: false,
		NetworkEnforced:    false,
		SandboxEnforced:    false,
	}
}

func (r *BwrapRunner) Run(_ context.Context, req BashRunRequest) (BashRunResult, error) {
	return BashRunResult{
		Output:         fmt.Sprintf("bwrap unavailable on %s", runtime.GOOS),
		IsError:        true,
		Classification: "sandbox_setup_failed",
		CWD:            req.DisplayCWD,
	}, nil
}
