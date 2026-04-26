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
//
// AuditRecords is the parallel observability surface for permitted
// (allow) connections. Substrate runners that surface allow Decisions
// project them through projectAuditRecords during the same
// snapshot-and-clear of the per-Run Decision buffer that produces
// Violations. The split keeps Violations focused on blocked actions
// (the agent's actionable signal) while audit records flow into
// telemetry-grade observability without bloating the BASH RESULT body.
// AuditRecordDropCount mirrors ViolationDropCount: when the retention
// cap is exceeded inside projectAuditRecords, surplus records are
// dropped and the count surfaces here so operators can detect a
// retention shortfall.
type BashRunResult struct {
	Output               string
	IsError              bool
	ExitCode             *int
	Duration             time.Duration
	CWD                  string
	TimedOut             bool
	Canceled             bool
	StdoutRawBytes       int64
	StderrRawBytes       int64
	StdoutTruncated      bool
	StderrTruncated      bool
	Classification       string
	Violations           []SandboxViolation
	ViolationDropCount   int
	AuditRecords         []SandboxAuditRecord
	AuditRecordDropCount int
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

// SandboxAuditRecord is the parallel-observability projection of a
// permitted (allow) network proxy Decision. It carries only the five
// retention-policy-approved fields {Host, Port, Protocol, Source,
// Decision}; URL paths, query strings, headers, and cookies are
// structurally absent so the type itself enforces the N6 retention
// boundary.
//
// Source is always "network_proxy" (the only producer of audit records
// today); Decision is always "allow" (the type's purpose). Both fields
// are kept rather than implied so structured telemetry consumers and
// future record sources stay self-describing.
type SandboxAuditRecord struct {
	Host     string `json:"host"`
	Port     uint16 `json:"port"`
	Protocol string `json:"protocol"`
	Source   string `json:"source"`
	Decision string `json:"decision"`
}

// auditRecordRetentionDefault caps how many SandboxAuditRecord entries
// each Run retains before dropping the surplus. Substrate runners pass
// this directly to projectAuditRecords. Kept as a package-level
// constant so the cross-platform projection lane stays
// platform-agnostic (architect lock).
const auditRecordRetentionDefault = 200

// projectAuditRecords iterates allow Decisions in arrival order,
// retaining at most maxRetained as SandboxAuditRecord values and
// returning the drop count for any overflow. Drops are FIFO: the first
// maxRetained allow Decisions are kept and any later allow Decisions
// are counted toward dropCount. This matches the partner-locked
// "operators see the early connections" preference over reservoir
// sampling.
//
// When maxRetained == 0, no records are retained but dropCount still
// reflects the total allow-Decision volume so operators can monitor
// "audit disabled but counted". Negative maxRetained is treated as 0.
//
// Reason is intentionally excluded from allow audit records (forward-compat: producer-side reason enum is v42-2 scope).
func projectAuditRecords(decisions []Decision, maxRetained int) ([]SandboxAuditRecord, int) {
	if maxRetained < 0 {
		maxRetained = 0
	}
	var totalAllow int
	for _, d := range decisions {
		if d.Allow {
			totalAllow++
		}
	}
	if totalAllow == 0 {
		return nil, 0
	}
	if maxRetained == 0 {
		return nil, totalAllow
	}
	keep := totalAllow
	dropCount := 0
	if keep > maxRetained {
		keep = maxRetained
		dropCount = totalAllow - maxRetained
	}
	out := make([]SandboxAuditRecord, 0, keep)
	for _, d := range decisions {
		if !d.Allow {
			continue
		}
		if len(out) == maxRetained {
			break
		}
		out = append(out, SandboxAuditRecord{
			Host:     sanitizeViolationField(d.Host),
			Port:     uint16(d.Port),
			Protocol: sanitizeViolationField(string(d.Protocol)),
			Source:   string(d.Source),
			Decision: "allow",
		})
	}
	return out, dropCount
}
