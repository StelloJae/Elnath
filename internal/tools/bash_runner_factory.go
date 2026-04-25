package tools

import (
	"context"
	"fmt"
	"net"
	"strconv"
)

// SandboxConfig captures the user-facing sandbox/runner mode for BashTool.
// Phase 1 supports "direct" (DirectRunner host-process backend) and
// "seatbelt" (macOS Seatbelt substrate). "bwrap" is reserved for the
// B3b-3 Linux lane and currently returns a clear unsupported error
// rather than silently degrading to DirectRunner — silent fallback
// would let "sandbox=on" requests run unsandboxed without notice.
//
// NetworkAllowlist is the B3b-2.5 default-deny + explicit IP:port
// allowlist. Each entry must be "<ipv4|ipv6>:<port>"; domain matching
// is deferred to B3b-4 with a real network proxy. An empty allowlist
// blocks all outbound network when the substrate runner enforces
// network policy. Domain entries are rejected at construction so the
// caller cannot ship config that silently does nothing.
//
// LLM/tool-param input MUST NOT populate this struct. Per the v41 partner
// verdict, only user-side configuration (config file, CLI flag, or
// interactive approval) constructs SandboxConfig. Bash command parameters
// have no field that influences the runner backend.
type SandboxConfig struct {
	// Mode selects the runner backend. Empty string is treated as "direct".
	Mode string

	// NetworkAllowlist holds explicit "host:port" entries the substrate
	// permits for outbound TCP/UDP. Each entry must use an IP address;
	// domain names are rejected. Only the substrate runners read this
	// field — DirectRunner ignores it (no policy to enforce).
	NetworkAllowlist []string
}

// NewBashRunnerForConfig returns a BashRunner for the given config or an
// error describing why the requested mode is unavailable. Callers MUST
// surface the error to the user instead of substituting DirectRunner —
// silent fallback would defeat the purpose of asking for a sandbox.
//
// For substrate modes (seatbelt, bwrap) the factory probes the runner at
// construction and refuses to return one whose Probe reports
// Available=false. The probe message becomes part of the returned error
// so the user sees the concrete reason (wrong platform, missing binary,
// etc.) rather than a generic "unsupported".
func NewBashRunnerForConfig(cfg SandboxConfig) (BashRunner, error) {
	switch cfg.Mode {
	case "", "direct":
		return NewDirectRunner(), nil
	case "seatbelt":
		r, err := NewSeatbeltRunnerWithAllowlist(cfg.NetworkAllowlist)
		if err != nil {
			return nil, fmt.Errorf("sandbox mode %q: %w", cfg.Mode, err)
		}
		p := r.Probe(context.Background())
		if !p.Available {
			return nil, fmt.Errorf("sandbox mode %q unavailable: %s", cfg.Mode, p.Message)
		}
		return r, nil
	case "bwrap":
		// B3b-3 ships default-deny network only; userspace IP/domain
		// allowlist on Linux requires the B3b-4 network proxy lane.
		// A non-empty allowlist with bwrap mode is rejected at the
		// factory so a config that expected allowlist semantics
		// cannot silently degrade to "no network".
		if len(cfg.NetworkAllowlist) > 0 {
			return nil, fmt.Errorf("sandbox mode %q does not yet support a network allowlist; bwrap is default-deny only in B3b-3 (allowlist support is the B3b-4 network proxy lane)", cfg.Mode)
		}
		r := NewBwrapRunner()
		p := r.Probe(context.Background())
		if !p.Available {
			return nil, fmt.Errorf("sandbox mode %q unavailable: %s", cfg.Mode, p.Message)
		}
		return r, nil
	default:
		return nil, fmt.Errorf("unknown sandbox mode %q", cfg.Mode)
	}
}

// validateNetworkAllowlist checks each entry is "<IP>:<port>" with a
// valid loopback IP and a non-zero port < 65536. Domain names AND
// non-loopback IPs are rejected with an explicit B3b-4 deferral
// message so config that expected broader allowlist semantics cannot
// silently degrade to "no network".
//
// Phase 1 supports only loopback (127.0.0.1, ::1) because Seatbelt's
// SBPL `(remote ip ...)` filter accepts only "*" or "localhost" as the
// host portion — arbitrary IPv4/IPv6 host filtering requires a
// userspace network proxy (the B3b-4 lane). The validator surfaces
// this constraint at construction so a caller asking for
// "10.0.0.5:5000" gets a clear error instead of a silently-broken
// profile.
//
// Returns a defensive copy so callers cannot mutate the live policy
// after the runner captures it.
func validateNetworkAllowlist(allowlist []string) ([]string, error) {
	if len(allowlist) == 0 {
		return nil, nil
	}
	cleaned := make([]string, 0, len(allowlist))
	for _, entry := range allowlist {
		host, portStr, err := net.SplitHostPort(entry)
		if err != nil {
			return nil, fmt.Errorf("network allowlist entry %q must be IP:port: %w", entry, err)
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return nil, fmt.Errorf("network allowlist entry %q must use an IP address; domain matching is deferred to B3b-4", entry)
		}
		if !ip.IsLoopback() {
			return nil, fmt.Errorf("network allowlist entry %q must target loopback (127.0.0.1 or ::1); non-loopback allowlists require the network proxy substrate (B3b-4)", entry)
		}
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil || port == 0 {
			return nil, fmt.Errorf("network allowlist entry %q has invalid port", entry)
		}
		cleaned = append(cleaned, entry)
	}
	return cleaned, nil
}
