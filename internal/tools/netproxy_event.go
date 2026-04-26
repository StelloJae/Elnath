// Package tools — netproxy_event.go
//
// v41 / B3b-4-0 proxy core. Self-contained library used by the
// macOS Seatbelt and Linux bwrap substrate lanes (B3b-4-2, B3b-4-3)
// to enforce domain + IP allowlists for outbound TCP traffic. NOT
// wired into BashRunner in this lane.
//
// Partner-locked pins observed here:
//   - DNS rebinding is not fully defended (cite Codex
//     network-proxy/README.md:217-219).
//   - No allowLocalBinding boolean. Local services are reached only
//     via explicit per-port entries.
//   - Forked-child self-exec proxy model. No in-process goroutine
//     proxy.
//   - Source enum is fixed at four values.
//   - No ProxyEnabled config flag — substrate lanes infer proxy need
//     from allowlist shape.

package tools

import (
	"errors"
	"fmt"
)

// ProxySource identifies which subsystem authored a network policy
// decision. The value space is fixed at four options per the v41
// partner-locked pin C5; new values cannot be added without a partner
// re-decision because downstream telemetry, output rendering, and the
// authoritative-vs-heuristic distinction all key off this enum.
type ProxySource string

const (
	// SourceNetworkProxy marks a decision authored by the in-tree
	// netproxy proxy listeners (HTTP CONNECT or SOCKS5 TCP). These
	// decisions are authoritative — the proxy actually accepted or
	// rejected the connection.
	SourceNetworkProxy ProxySource = "network_proxy"
	// SourceSandboxSubstrate marks a structured decision authored by
	// the kernel-level substrate (Seatbelt or bwrap) when it surfaces
	// a violation through a structured channel. Reserved for future
	// substrate wiring; B3b-4-0 does not emit this value.
	SourceSandboxSubstrate ProxySource = "sandbox_substrate"
	// SourceSandboxSubstrateHeuristic marks a low-confidence decision
	// inferred from substrate stderr substring matching (the legacy
	// detectSeatbeltViolations / detectBwrapViolations path). Output
	// rendering must mark these as low-confidence. B3b-4-0 does not
	// emit this value.
	SourceSandboxSubstrateHeuristic ProxySource = "sandbox_substrate_heuristic"
	// SourceDNSResolver marks a decision authored by the proxy-side
	// DNS resolver step (e.g., resolution refused or returned a
	// blocked IP). Reserved for B3b-4-0 dns helper to populate when
	// a hostname resolves to a denied address.
	SourceDNSResolver ProxySource = "dns_resolver"
)

// IsValid reports whether the Source is one of the four partner-locked
// enum values. Empty Source is invalid; any value outside the four
// pinned constants is invalid. Decision construction MUST validate.
func (s ProxySource) IsValid() bool {
	switch s {
	case SourceNetworkProxy, SourceSandboxSubstrate, SourceSandboxSubstrateHeuristic, SourceDNSResolver:
		return true
	default:
		return false
	}
}

// String renders the enum value as its wire representation. The wire
// format is the snake_case string Codex uses in
// `NetworkDecisionSource::as_str` so cross-tool log readers see the
// same vocabulary on both stacks.
func (s ProxySource) String() string { return string(s) }

// ProxyProtocol identifies the L7/L4 framing of an inspected
// connection. Three values are supported in B3b-4-0; UDP / QUIC are
// blocked at the substrate layer per the v41 partner verdict and have
// no protocol value here.
type ProxyProtocol string

const (
	// ProtocolTCP marks a raw TCP connection inspected at the
	// substrate layer (no proxy framing). Used when the substrate
	// itself reports a TCP-level deny event without going through the
	// HTTP CONNECT or SOCKS5 listener.
	ProtocolTCP ProxyProtocol = "tcp"
	// ProtocolHTTPSConnect marks a connection inspected via the HTTP
	// CONNECT proxy listener. Includes both HTTPS CONNECT tunnels and
	// any plain CONNECT-tunneling client.
	ProtocolHTTPSConnect ProxyProtocol = "https_connect"
	// ProtocolSOCKS5TCP marks a connection inspected via the SOCKS5
	// TCP CONNECT (cmd 0x01) listener. SOCKS5 UDP ASSOCIATE (0x03)
	// and BIND (0x02) are not supported and never produce this value.
	ProtocolSOCKS5TCP ProxyProtocol = "socks5_tcp"
)

// IsValid reports whether the protocol is one of the three supported
// B3b-4-0 enum values. Empty is invalid; any other value is invalid.
func (p ProxyProtocol) IsValid() bool {
	switch p {
	case ProtocolTCP, ProtocolHTTPSConnect, ProtocolSOCKS5TCP:
		return true
	default:
		return false
	}
}

// String renders the protocol's wire representation. Matches Codex's
// `NetworkProtocol::as_policy_protocol` snake_case strings so logs
// stay comparable across stacks.
func (p ProxyProtocol) String() string { return string(p) }

// ProxyReason classifies why a connection was denied. The seven
// values cover the partner-locked deny taxonomy. Allow decisions do
// NOT carry a Reason — Decision.Reason is empty when Decision.Allow
// is true.
type ProxyReason string

const (
	// ReasonNotInAllowlist denies a connection because the host did
	// not match any allowlist entry and no other rule applied. This
	// is the default deny path for allowlist-only configs.
	ReasonNotInAllowlist ProxyReason = "not_in_allowlist"
	// ReasonDeniedByRule denies a connection because the host
	// matched an explicit denylist entry. Denylist always wins over
	// allowlist (Codex network-proxy/README.md:199).
	ReasonDeniedByRule ProxyReason = "denied_by_rule"
	// ReasonDNSResolutionBlocked denies a connection because the
	// proxy-side resolver could not resolve the hostname or the
	// resolved address violated policy. Used by the dns helper.
	ReasonDNSResolutionBlocked ProxyReason = "dns_resolution_blocked"
	// ReasonLocalBindingDisabled denies a connection to a loopback
	// or private IP that was not on the explicit allowlist. This is
	// the partner-locked replacement for an `allowLocalBinding`
	// boolean — the only way to permit a local service is an explicit
	// per-port entry like `127.0.0.1:5432`.
	ReasonLocalBindingDisabled ProxyReason = "local_binding_disabled"
	// ReasonModeGuard denies a connection because the request type
	// is not permitted in the active mode (e.g., a non-CONNECT HTTP
	// method on the CONNECT listener).
	ReasonModeGuard ProxyReason = "mode_guard"
	// ReasonProtocolUnsupported denies a connection because the
	// requested protocol is out of scope for the proxy (e.g., SOCKS5
	// UDP ASSOCIATE, BIND command).
	ReasonProtocolUnsupported ProxyReason = "protocol_unsupported"
	// ReasonInvalidConfig denies a connection because the policy
	// config is internally invalid (e.g., scoped IPv6 literal). The
	// proxy refuses the connection rather than silently degrading.
	ReasonInvalidConfig ProxyReason = "invalid_config"
)

// IsValid reports whether the reason is one of the seven supported
// values. Empty is invalid for a deny Decision; for an Allow
// Decision, Reason MUST be empty.
func (r ProxyReason) IsValid() bool {
	switch r {
	case ReasonNotInAllowlist,
		ReasonDeniedByRule,
		ReasonDNSResolutionBlocked,
		ReasonLocalBindingDisabled,
		ReasonModeGuard,
		ReasonProtocolUnsupported,
		ReasonInvalidConfig:
		return true
	default:
		return false
	}
}

// String renders the reason's wire representation.
func (r ProxyReason) String() string { return string(r) }

// Decision is the structured event the proxy emits for every
// inspected connection. Allow decisions carry an empty Reason. Deny
// decisions carry a non-empty Reason and a non-empty Source. Host and
// Port are the destination as the client named it (hostname or IP
// literal); Port is 0 for DNS-only or pre-handshake events. Protocol
// names the framing layer the decision was made at.
//
// JSON encoding uses snake_case keys so logs remain readable when
// piped into jq / OTEL exporters / cross-tool log aggregators.
//
// N6 retention policy (B3b-4-1): Host may carry hostnames the user
// never typed when the request arrived through the SOCKS5 DOMAINNAME
// (ATYP=0x03) path or as a CONNECT host header. Downstream telemetry
// MUST NOT log full URL paths, query strings, or HTTP headers; only
// {host, port, protocol, reason, source} from this struct are
// approved for INFO-level structured emission. emitBashTelemetry in
// bash.go enforces the projection on the executor side. Operators
// requiring stricter Host redaction (e.g. for FQDNs containing
// internal route names) MUST gate at a downstream filter rather than
// inside the listener loop, which keeps the proxy decision-emit path
// allocation-free.
type Decision struct {
	Allow    bool          `json:"allow"`
	Source   ProxySource   `json:"source"`
	Reason   ProxyReason   `json:"reason,omitempty"`
	Host     string        `json:"host"`
	Port     int           `json:"port"`
	Protocol ProxyProtocol `json:"protocol"`
}

// NewAllow constructs an Allow Decision. Source MUST be non-empty;
// Host MUST be non-empty; Protocol MUST be valid. Port may be zero
// for protocols where the port is implicit. Returns an error rather
// than panicking so callers can surface invalid construction as a
// proxy-internal event rather than crashing the listener loop.
func NewAllow(source ProxySource, host string, port int, protocol ProxyProtocol) (Decision, error) {
	if !source.IsValid() {
		return Decision{}, fmt.Errorf("netproxy: invalid Source %q for allow decision", source)
	}
	if host == "" {
		return Decision{}, errors.New("netproxy: Host required for allow decision")
	}
	if !protocol.IsValid() {
		return Decision{}, fmt.Errorf("netproxy: invalid Protocol %q for allow decision", protocol)
	}
	if port < 0 || port > 65535 {
		return Decision{}, fmt.Errorf("netproxy: Port %d out of range for allow decision", port)
	}
	return Decision{
		Allow:    true,
		Source:   source,
		Host:     host,
		Port:     port,
		Protocol: protocol,
	}, nil
}

// NewDeny constructs a Deny Decision. Source, Reason, Host, and
// Protocol MUST be non-empty / valid. Returns an error rather than
// panicking.
func NewDeny(source ProxySource, reason ProxyReason, host string, port int, protocol ProxyProtocol) (Decision, error) {
	if !source.IsValid() {
		return Decision{}, fmt.Errorf("netproxy: invalid Source %q for deny decision", source)
	}
	if !reason.IsValid() {
		return Decision{}, fmt.Errorf("netproxy: invalid Reason %q for deny decision", reason)
	}
	if host == "" {
		return Decision{}, errors.New("netproxy: Host required for deny decision")
	}
	if !protocol.IsValid() {
		return Decision{}, fmt.Errorf("netproxy: invalid Protocol %q for deny decision", protocol)
	}
	if port < 0 || port > 65535 {
		return Decision{}, fmt.Errorf("netproxy: Port %d out of range for deny decision", port)
	}
	return Decision{
		Allow:    false,
		Source:   source,
		Reason:   reason,
		Host:     host,
		Port:     port,
		Protocol: protocol,
	}, nil
}

// EventSink receives Decision events from the proxy listeners and
// internal proxy errors that aren't a Decision. The sink MUST NOT
// block — implementations should buffer or drop, not block the
// accept loop. This is the partner-mini-lap N1 carry-forward: errors
// in the production accept loop must be observable, not silently
// swallowed.
//
// EventSink is intentionally tiny (two methods) so test sinks are
// trivial and so production callers can layer ring-buffers, slog
// emitters, or session-keyed channels on top without coupling the
// listener implementation to any particular telemetry shape.
type EventSink interface {
	// EmitDecision records an allow or deny decision for an
	// inspected connection. Called from listener goroutines; MUST be
	// goroutine-safe. MUST NOT block.
	EmitDecision(Decision)
	// EmitError records a proxy-internal error that is not a
	// per-connection Decision (e.g., listener accept error other
	// than "listener closed", invalid SOCKS5 framing). MUST be
	// goroutine-safe and non-blocking.
	EmitError(error)
}
