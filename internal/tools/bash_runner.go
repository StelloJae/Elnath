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
// ExecutionMode / SandboxEnforced / FilesystemEnforced / NetworkEnforced /
// PolicyName populate the structured slog telemetry fields emitted on
// every Run. They are static per runner instance so telemetry can name
// the active backend without reaching into runner-specific state on each
// invocation. SandboxEnforced is reserved for the case where BOTH
// filesystem and network isolation are enforced — partial substrates
// (e.g., Seatbelt B3b-2 filesystem-only prototype) report
// FilesystemEnforced=true with SandboxEnforced still false.
type BashRunnerProbe struct {
	Available          bool
	Name               string
	Platform           string
	Message            string
	ExecutionMode      string // "direct_host_guarded" | "macos_seatbelt_fs" | "macos_seatbelt" | "linux_bwrap"
	SandboxEnforced    bool   // true only when BOTH filesystem and network are enforced
	FilesystemEnforced bool
	NetworkEnforced    bool
	PolicyName         string // "direct" | "seatbelt-fs" | "seatbelt" | "bwrap"
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
//
// ViolationDropCount carries the netproxy ChannelEventSink drop count
// associated with this invocation (events that the sink discarded
// because the buffer was full). The substrate runners populate it in
// B3b-4-2 / B3b-4-3 when the proxy is wired through; B3b-4-1 only
// exposes the field so emitBashTelemetry can surface non-zero drops
// to operators (N4 closure).
type BashRunResult struct {
	Output             string
	IsError            bool
	ExitCode           *int
	Duration           time.Duration
	CWD                string
	TimedOut           bool
	Canceled           bool
	StdoutRawBytes     int64
	StderrRawBytes     int64
	StdoutTruncated    bool
	StderrTruncated    bool
	Classification     string
	Violations         []SandboxViolation
	ViolationDropCount int
}

// SandboxViolation is populated by substrate-aware runners when a command
// is blocked or restricted by sandbox policy. Empty for DirectRunner since
// the host-process backend has no policy enforcement to violate.
//
// The struct carries two parallel shapes:
//
//   - Filesystem-style violation: only Kind/Path/Message are populated
//     (Host/Port/Protocol/Reason/Source remain at zero value). This is
//     the legacy shape produced by the substrate stderr heuristics.
//   - Network-style violation: Source MUST be one of the four
//     partner-locked values listed below; Host (hostname or IP literal),
//     Port, Protocol, and Reason describe the deny event. Kind/Path may
//     also be populated for backward compatibility but are not required.
//
// Source enum values (mirrors netproxy_event.go ProxySource):
//
//   - "network_proxy"               — authoritative decision from the
//     in-tree netproxy listener (HTTP CONNECT or SOCKS5 TCP)
//   - "sandbox_substrate"           — authoritative decision surfaced
//     by Seatbelt/bwrap through a structured channel (B3b-4-2 onward)
//   - "sandbox_substrate_heuristic" — low-confidence decision inferred
//     from substrate stderr substring matching (legacy detector path)
//   - "dns_resolver"                — proxy-side DNS resolver refused
//     resolution or the resolved IP violated policy
//
// Protocol values mirror netproxy_event.go ProxyProtocol: "tcp",
// "https_connect", "socks5_tcp". The struct uses the bare string type
// to keep the package boundary thin; IsValidSandboxViolationSource
// validates Source values for new network-shaped entries.
type SandboxViolation struct {
	Kind     string
	Path     string
	Message  string
	Host     string
	Port     uint16
	Protocol string
	Reason   string
	Source   string
}

// IsValidSandboxViolationSource reports whether s is one of the four
// partner-locked Source enum values. Empty string is invalid; any
// value outside the four pinned constants is invalid. Substrate
// runners populating the new network-shaped fields MUST validate with
// this helper before emitting so downstream telemetry and output
// rendering keep a stable vocabulary across stacks.
func IsValidSandboxViolationSource(s string) bool {
	switch s {
	case string(SourceNetworkProxy),
		string(SourceSandboxSubstrate),
		string(SourceSandboxSubstrateHeuristic),
		string(SourceDNSResolver):
		return true
	default:
		return false
	}
}
