package tools

import (
	"context"
	"time"
)

// BashRunner abstracts shell command execution behind a swappable backend.
// DirectRunner (default) executes commands as host processes with the B3a
// guardrails (clean env, bounded output, process group cleanup). Future
// substrate runners (Seatbelt on macOS, bwrap on Linux) implement this same
// interface to provide OS-enforced isolation.
//
// Per-invocation cleanup is the runner's internal responsibility inside
// Run; Close handles runner-lifetime resources only. Sandbox violations
// live in BashRunResult rather than a runner-global accessor so concurrent
// invocations cannot race on shared state.
type BashRunner interface {
	Name() string
	Probe(ctx context.Context) BashRunnerProbe
	Run(ctx context.Context, req BashRunRequest) (BashRunResult, error)
	Close(ctx context.Context) error
}

// BashRunnerProbe reports whether a runner's substrate dependencies are
// available on the current platform, plus static identity used by
// per-invocation telemetry. Surfaced once at session init and cached by
// callers — Probe is NOT consulted per-command. Available=false indicates
// the substrate cannot run here; callers MUST surface a clear diagnostic
// rather than silently falling back to a different runner.
//
// ExecutionMode / SandboxEnforced / PolicyName populate the structured
// slog telemetry fields emitted on every Run. They are static per runner
// instance so telemetry can name the active backend without reaching into
// runner-specific state on each invocation.
type BashRunnerProbe struct {
	Available       bool
	Name            string
	Platform        string
	Message         string
	ExecutionMode   string // "direct_host_guarded" | "macos_seatbelt" | "linux_bwrap"
	SandboxEnforced bool
	PolicyName      string // "direct" | "seatbelt" | "bwrap"
}

// BashRunRequest is the input to BashRunner.Run. Paths are absolute, real
// (symlink-resolved), and verified to lie within the session workspace by
// the caller — runners trust these inputs and do not re-validate.
type BashRunRequest struct {
	Command    string
	WorkDir    string
	SessionDir string
	DisplayCWD string
}

// BashRunResult mirrors the public Result envelope (Output, IsError) and
// also carries the structured fields a runner produced. Output is the
// LLM-facing string already rendered by the runner; callers wrap it in
// tools.Result without further transformation.
type BashRunResult struct {
	Output          string
	IsError         bool
	ExitCode        *int
	Duration        time.Duration
	CWD             string
	TimedOut        bool
	Canceled        bool
	StdoutRawBytes  int64
	StderrRawBytes  int64
	StdoutTruncated bool
	StderrTruncated bool
	Classification  string
	Violations      []SandboxViolation
}

// SandboxViolation is populated by substrate-aware runners when a command
// is blocked or restricted by sandbox policy. Empty for DirectRunner since
// the host-process backend has no policy enforcement to violate.
type SandboxViolation struct {
	Kind    string
	Path    string
	Message string
}
