package tools

import "fmt"

// SandboxConfig captures the user-facing sandbox/runner mode for BashTool.
// Phase 1 supports only "direct" (the DirectRunner host-process backend).
// "seatbelt" and "bwrap" are reserved for B3b-2 / B3b-3 substrate lanes
// and currently return a clear unsupported error rather than silently
// degrading to DirectRunner — silent fallback would let "sandbox=on"
// requests run unsandboxed without notice.
//
// LLM/tool-param input MUST NOT populate this struct. Per the v41 partner
// verdict, only user-side configuration (config file, CLI flag, or
// interactive approval) constructs SandboxConfig. Bash command parameters
// have no field that influences the runner backend.
type SandboxConfig struct {
	// Mode selects the runner backend. Empty string is treated as "direct".
	Mode string
}

// NewBashRunnerForConfig returns a BashRunner for the given config or an
// error describing why the requested mode is unavailable. Callers MUST
// surface the error to the user instead of substituting DirectRunner —
// silent fallback would defeat the purpose of asking for a sandbox.
func NewBashRunnerForConfig(cfg SandboxConfig) (BashRunner, error) {
	switch cfg.Mode {
	case "", "direct":
		return NewDirectRunner(), nil
	case "seatbelt":
		return nil, fmt.Errorf("sandbox mode %q not yet implemented (B3b-2 macOS Seatbelt lane pending)", cfg.Mode)
	case "bwrap":
		return nil, fmt.Errorf("sandbox mode %q not yet implemented (B3b-3 Linux bwrap lane pending)", cfg.Mode)
	default:
		return nil, fmt.Errorf("unknown sandbox mode %q", cfg.Mode)
	}
}
