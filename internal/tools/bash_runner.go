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
// ViolationDropCount carries the per-Run deny-Decision drop count
// from the boundedDecisionBuffer (netproxy_drain.go). Populated by
// substrate runners (Seatbelt + bwrap) when more than decisionDenyCap
// deny events arrive in a single Run; surplus events are dropped FIFO
// and counted here. v42-2 wired what v42-1a declared.
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

// proxySurfaces is the single-snapshot result of one drain-slice
// consume from boundedDecisionBuffer. The shape is partner-locked at
// 3 fields per v42-1b architect addendum §2; ViolationDropCount is
// returned via tuple from collectProxyDecisions rather than expanding
// this struct.
//
// v42-2 (priority-aware bounded event drain).
type proxySurfaces struct {
	Violations         []SandboxViolation
	Permitted          []SandboxAuditRecord
	PermittedDropCount int
}

// projectAuditRecords is preserved as a thin wrapper around
// projectAuditRecordsFromAllowOnly + cap enforcement so v42-1b unit
// and substrate parity tests stay byte-identical. Production substrate
// paths now route through the boundedDecisionBuffer (netproxy_drain.go)
// and call projectAuditRecordsFromAllowOnly directly with allow-only,
// already-capped slices.
//
// v42-2: kept for test compatibility; production allocates allow-only
// slices upstream so this wrapper sees no production traffic.
func projectAuditRecords(decisions []Decision, maxRetained int) ([]SandboxAuditRecord, int) {
	if maxRetained < 0 {
		maxRetained = 0
	}
	var totalAllow int
	var allows []Decision
	for _, d := range decisions {
		if !d.Allow {
			continue
		}
		totalAllow++
		if maxRetained == 0 {
			continue
		}
		if len(allows) < maxRetained {
			allows = append(allows, d)
		}
	}
	if totalAllow == 0 {
		return nil, 0
	}
	if maxRetained == 0 {
		return nil, totalAllow
	}
	drop := totalAllow - len(allows)
	return projectAuditRecordsFromAllowOnly(allows), drop
}
