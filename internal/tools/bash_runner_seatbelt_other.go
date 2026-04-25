//go:build !darwin

package tools

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

// SeatbeltRunner is a stub on non-darwin platforms. The factory must
// surface the unsupported diagnostic via Probe rather than silently
// substituting DirectRunner — silent fallback would defeat the purpose
// of asking for a sandbox.
type SeatbeltRunner struct {
	killGrace        time.Duration
	networkAllowlist []string
}

func NewSeatbeltRunner() *SeatbeltRunner {
	r, _ := NewSeatbeltRunnerWithAllowlist(nil)
	return r
}

func NewSeatbeltRunnerWithAllowlist(allowlist []string) (*SeatbeltRunner, error) {
	cleaned, err := validateNetworkAllowlist(allowlist)
	if err != nil {
		return nil, err
	}
	return &SeatbeltRunner{killGrace: bashKillGrace, networkAllowlist: cleaned}, nil
}

func (r *SeatbeltRunner) Name() string { return "seatbelt" }

func (r *SeatbeltRunner) Close(_ context.Context) error { return nil }

func (r *SeatbeltRunner) Probe(_ context.Context) BashRunnerProbe {
	return BashRunnerProbe{
		Available:          false,
		Name:               r.Name(),
		Platform:           runtime.GOOS,
		Message:            "macos_seatbelt requires darwin",
		ExecutionMode:      "macos_seatbelt",
		PolicyName:         "seatbelt",
		FilesystemEnforced: false,
		NetworkEnforced:    false,
		SandboxEnforced:    false,
	}
}

func (r *SeatbeltRunner) Run(_ context.Context, req BashRunRequest) (BashRunResult, error) {
	return BashRunResult{
		Output:         fmt.Sprintf("seatbelt unavailable on %s", runtime.GOOS),
		IsError:        true,
		Classification: "sandbox_setup_failed",
		CWD:            req.DisplayCWD,
	}, nil
}
