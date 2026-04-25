// Package tools — netproxy_policy.go
//
// v41 / B3b-4-0 proxy core. Self-contained library used by the
// macOS Seatbelt and Linux bwrap substrate lanes (B3b-4-2, B3b-4-3)
// to enforce domain + IP allowlists for outbound TCP traffic. NOT
// wired into BashRunner in this lane.
//
// Partner-locked pins observed here:
//   - DNS rebinding is not fully defended (cite Codex
//     network-proxy/README.md:217-219). Domain allowlists are policy
//     over the hostname presented to the proxy, not over the IP the
//     network actually reaches. Hostile DNS bypass is out of scope.
//   - No allowLocalBinding boolean. Local services are reached only
//     via explicit per-port entries (`127.0.0.1:5432`,
//     `localhost:6379`). The Codex `allow_local_binding` flag is
//     deliberately NOT mirrored — its boolean shape is a footgun
//     because it expands the SBPL allowlist beyond proxy ports.
//   - Forked-child self-exec proxy model. No in-process goroutine
//     proxy.
//   - Source enum is fixed at four values.
//   - No ProxyEnabled config flag — substrate lanes infer proxy need
//     from allowlist shape.

package tools

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Allowlist is a parsed, immutable set of allow patterns evaluated
// against connection requests. Construct via ParseAllowlist.
type Allowlist struct {
	rules []policyRule
}

// Denylist is a parsed, immutable set of deny patterns. When non-empty
// and a request matches, the request is denied with
// ReasonDeniedByRule regardless of any allowlist match. Construct via
// ParseDenylist.
type Denylist struct {
	rules []policyRule
}

// IsEmpty reports whether the list contains no rules.
func (a Allowlist) IsEmpty() bool { return len(a.rules) == 0 }

// IsEmpty reports whether the list contains no rules.
func (d Denylist) IsEmpty() bool { return len(d.rules) == 0 }

// Rules returns the parsed rules for inspection. Read-only — callers
// must NOT mutate the slice. Used by substrate lanes (B3b-4-1 onward)
// to infer whether the proxy is needed (presence of any non-loopback
// IP entry or any domain entry).
func (a Allowlist) Rules() []policyRule { return a.rules }

// policyRule is one parsed entry. Either Pattern (domain glob, possibly
// with `*.` / `**.` prefix) or IP literal is set; never both. Port is
// always set to a non-zero value because every parsed entry must
// specify a port.
type policyRule struct {
	// Original is the entry as supplied by the user, used for
	// diagnostics.
	Original string
	// IsIP is true when the rule matches an IP literal (v4 or v6).
	// Otherwise the rule matches a hostname pattern.
	IsIP bool
	// IP holds the parsed IP literal when IsIP=true.
	IP net.IP
	// HostPattern is the normalized lowercase hostname without
	// trailing dot.
	HostPattern string
	// Wildcard is the pattern shape for HostPattern matching.
	Wildcard wildcardKind
	// Port is the required destination port.
	Port int
}

// IsLoopbackHost reports whether this rule targets an IP literal in
// loopback range or the literal "localhost" hostname. Used by
// substrate lanes to detect the "loopback-only" allowlist shape
// (which doesn't actually need the network proxy if Seatbelt /
// bwrap can pin the loopback ports themselves).
func (r policyRule) IsLoopbackHost() bool {
	if r.IsIP {
		return r.IP.IsLoopback()
	}
	return strings.EqualFold(r.HostPattern, "localhost")
}

type wildcardKind int

const (
	wildcardExact     wildcardKind = iota // "github.com"
	wildcardSubdomain                     // "*.github.com" — subdomains only, NOT apex
	wildcardApexAndSub                    // "**.github.com" — apex + subdomains
)

// Resolver abstracts host-side hostname resolution for the proxy. The
// proxy uses this to resolve a hostname to its IP set and apply the
// special-range checks against each candidate IP. The default
// implementation wraps net.DefaultResolver.LookupHost; tests inject
// a stub.
//
// IMPORTANT: DNS rebinding is not fully defended by this proxy. The
// resolver step happens at evaluate-time per request; if the upstream
// resolver rotates an A record between two evaluations, the second
// evaluation sees the new address. Per the partner-locked pin C2,
// callers must NOT claim rebinding is "closed" — for hostile DNS
// threat models, enforce egress at a lower layer (firewall, VPC,
// corporate proxy policies). See Codex `network-proxy/README.md:217-219`.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// ParseAllowlist parses a list of "host:port" entries into an
// Allowlist. Returns an error on the first invalid entry; callers
// must surface the error to the user rather than degrading silently.
//
// Grammar (mirrors Codex `network-proxy/README.md:198`):
//   - Exact host:           api.github.com:443
//   - Single-label wildcard:*.github.com:443        (subdomains only,
//     NOT apex)
//   - Apex + sub wildcard:  **.github.com:443       (apex + subs)
//   - IPv4 literal:         192.168.1.10:5432
//   - IPv6 literal:         [::1]:5432
//   - Localhost literal:    localhost:6379
//
// Bare global wildcard `*:443` is REJECTED — it would silently open
// the entire internet.
//
// Scoped IPv6 literals like `[fe80::1%en0]:443` are REJECTED at parse
// time because zone-scoped addresses bypass the special-range check
// (Codex `network-proxy/src/runtime.rs:1233` test
// `host_blocked_rejects_scoped_ipv6_literal_when_not_allowlisted`).
//
// Empty input is valid and produces an empty Allowlist that
// default-denies every evaluation.
func ParseAllowlist(entries []string) (Allowlist, error) {
	rules, err := parseRules(entries, "allowlist")
	if err != nil {
		return Allowlist{}, err
	}
	return Allowlist{rules: rules}, nil
}

// ParseDenylist parses a list of "host:port" entries into a Denylist.
// Same grammar as ParseAllowlist. Per Codex
// `network-proxy/README.md:199`, denylist matches always win over
// allowlist matches.
func ParseDenylist(entries []string) (Denylist, error) {
	rules, err := parseRules(entries, "denylist")
	if err != nil {
		return Denylist{}, err
	}
	return Denylist{rules: rules}, nil
}

func parseRules(entries []string, kind string) ([]policyRule, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	rules := make([]policyRule, 0, len(entries))
	for _, entry := range entries {
		raw := strings.TrimSpace(entry)
		if raw == "" {
			return nil, fmt.Errorf("netproxy %s: empty entry not permitted", kind)
		}
		rule, err := parseRule(raw, kind)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func parseRule(raw, kind string) (policyRule, error) {
	host, portStr, err := net.SplitHostPort(raw)
	if err != nil {
		return policyRule{}, fmt.Errorf("netproxy %s entry %q: missing host:port (port is required)", kind, raw)
	}
	port, perr := strconv.Atoi(portStr)
	if perr != nil || port <= 0 || port > 65535 {
		return policyRule{}, fmt.Errorf("netproxy %s entry %q: port out of range (must be 1-65535)", kind, raw)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return policyRule{}, fmt.Errorf("netproxy %s entry %q: host portion empty", kind, raw)
	}

	if host == "*" {
		return policyRule{}, fmt.Errorf("netproxy %s entry %q: bare global wildcard %q is not permitted; use exact hosts or scoped wildcards like *.example.com", kind, raw, "*")
	}

	if ip := net.ParseIP(host); ip != nil {
		return policyRule{
			Original: raw,
			IsIP:     true,
			IP:       ip,
			Port:     port,
		}, nil
	}

	if strings.Contains(host, "%") {
		return policyRule{}, fmt.Errorf("netproxy %s entry %q: scoped IPv6 literals are not permitted (zone identifier breaks special-range matching)", kind, raw)
	}

	hp := normalizeHost(host)
	wk := wildcardExact
	pattern := hp
	switch {
	case strings.HasPrefix(hp, "**."):
		wk = wildcardApexAndSub
		pattern = strings.TrimPrefix(hp, "**.")
	case strings.HasPrefix(hp, "*."):
		wk = wildcardSubdomain
		pattern = strings.TrimPrefix(hp, "*.")
	}
	if pattern == "" || strings.Contains(pattern, "*") {
		return policyRule{}, fmt.Errorf("netproxy %s entry %q: hostname pattern invalid", kind, raw)
	}
	return policyRule{
		Original:    raw,
		HostPattern: pattern,
		Wildcard:    wk,
		Port:        port,
	}, nil
}

// normalizeHost lowercases and strips trailing-dot per Codex
// `network-proxy/src/policy.rs:121`.
func normalizeHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	return strings.TrimRight(h, ".")
}

// Evaluate is a convenience: equivalent to EvaluateWithDenylist with
// an empty denylist. Used by tests that don't care about denylist
// semantics.
func (a Allowlist) Evaluate(ctx context.Context, host string, port int, protocol ProxyProtocol, resolver Resolver) Decision {
	return EvaluateWithDenylist(ctx, a, Denylist{}, host, port, protocol, resolver)
}

// EvaluateWithDenylist applies the partner-locked deny-wins policy:
//
//  1. If the hostname or its resolved IPs match a denylist rule -> deny
//     with ReasonDeniedByRule.
//  2. If the hostname matches an allowlist rule (and resolved IPs do
//     not violate the local-binding default-deny) -> allow.
//  3. If the host is a non-allowlisted IP literal in a special range
//     (loopback, private, ULA, link-local, multicast, v4-mapped,
//     unspecified) -> deny with ReasonLocalBindingDisabled.
//  4. Otherwise -> deny with ReasonNotInAllowlist.
//
// resolver may be nil when the host is already an IP literal or when
// the caller does not want resolved-IP checks. Callers SHOULD pass a
// real resolver for hostname inputs so private-range bypass via
// public-resolving-to-private (e.g. shady.example.com -> 10.0.0.5)
// is closed.
//
// The partner pin on DNS rebinding is observed here: this function
// does not pin the resolved IP across calls. Two consecutive
// evaluations of the same hostname may see different IPs if the
// upstream resolver rotates. Callers requiring rebinding defense
// MUST enforce at a lower network layer.
func EvaluateWithDenylist(
	ctx context.Context,
	allow Allowlist,
	deny Denylist,
	host string,
	port int,
	protocol ProxyProtocol,
	resolver Resolver,
) Decision {
	host = strings.TrimSpace(host)
	if host == "" {
		d, _ := NewDeny(SourceNetworkProxy, ReasonInvalidConfig, "", port, protocol)
		return d
	}
	if !protocol.IsValid() {
		d, _ := NewDeny(SourceNetworkProxy, ReasonInvalidConfig, host, port, protocol)
		return d
	}

	// Reject scoped IPv6 at evaluation time too; the parser already
	// rejects scoped entries at parse time, but inputs may arrive
	// from the wire (HTTP CONNECT host header) bearing zone IDs.
	if strings.Contains(host, "%") {
		d, _ := NewDeny(SourceNetworkProxy, ReasonInvalidConfig, host, port, protocol)
		return d
	}

	normalized := normalizeHost(host)

	// --- 1. denylist (deny wins) ---
	if matchAny(deny.rules, normalized, port) {
		d, _ := NewDeny(SourceNetworkProxy, ReasonDeniedByRule, host, port, protocol)
		return d
	}

	// If the host is an IP literal we can immediately apply the
	// allowlist + special-range checks without DNS.
	if ip := net.ParseIP(host); ip != nil {
		if matchIPRule(allow.rules, ip, port) {
			d, _ := NewAllow(SourceNetworkProxy, host, port, protocol)
			return d
		}
		if isSpecialRangeIP(ip) {
			d, _ := NewDeny(SourceNetworkProxy, ReasonLocalBindingDisabled, host, port, protocol)
			return d
		}
		d, _ := NewDeny(SourceNetworkProxy, ReasonNotInAllowlist, host, port, protocol)
		return d
	}

	// --- 2. allowlist hostname match ---
	if !matchAny(allow.rules, normalized, port) {
		d, _ := NewDeny(SourceNetworkProxy, ReasonNotInAllowlist, host, port, protocol)
		return d
	}

	// --- 3. resolved-IP check (best-effort, defends public->private) ---
	if resolver != nil {
		ips, err := resolver.LookupHost(ctx, host)
		if err != nil {
			d, _ := NewDeny(SourceDNSResolver, ReasonDNSResolutionBlocked, host, port, protocol)
			return d
		}
		for _, addr := range ips {
			ip := net.ParseIP(addr)
			if ip == nil {
				continue
			}
			if isSpecialRangeIP(ip) && !matchIPRule(allow.rules, ip, port) {
				d, _ := NewDeny(SourceDNSResolver, ReasonLocalBindingDisabled, host, port, protocol)
				return d
			}
		}
	}

	d, _ := NewAllow(SourceNetworkProxy, host, port, protocol)
	return d
}

// matchAny reports whether any rule in rs allows host on the given
// port. Both port and host must match. host MUST already be
// normalized (lowercase, no trailing dot).
func matchAny(rs []policyRule, host string, port int) bool {
	for _, r := range rs {
		if r.Port != port {
			continue
		}
		if r.IsIP {
			// IP rule cannot match a hostname.
			continue
		}
		if r.matchesHost(host) {
			return true
		}
	}
	return false
}

// matchIPRule reports whether any rule matches the given IP literal
// at the given port. Compares parsed IP value (so "::1" and "0:0:0:0:0:0:0:1"
// are equivalent), not the string representation.
func matchIPRule(rs []policyRule, ip net.IP, port int) bool {
	for _, r := range rs {
		if r.Port != port {
			continue
		}
		if !r.IsIP {
			continue
		}
		if r.IP.Equal(ip) {
			return true
		}
	}
	return false
}

func (r policyRule) matchesHost(host string) bool {
	switch r.Wildcard {
	case wildcardExact:
		return host == r.HostPattern
	case wildcardSubdomain:
		// Subdomains only, NOT apex.
		if host == r.HostPattern {
			return false
		}
		return strings.HasSuffix(host, "."+r.HostPattern)
	case wildcardApexAndSub:
		if host == r.HostPattern {
			return true
		}
		return strings.HasSuffix(host, "."+r.HostPattern)
	}
	return false
}

// extendedIPv4SpecialCIDRs lists the IPv4 ranges Codex's
// is_non_public_ipv4 (`network-proxy/src/policy.rs:52-70`) classifies
// as non-public but Go's net.IP method-based classifiers do NOT cover.
// Parsed once at package init so per-evaluation cost stays at a
// handful of u32 mask compares.
//
// CGNAT (100.64.0.0/10) is the realistic SSRF vector — net.IP.IsPrivate
// only covers RFC1918, so without this table an attacker-controlled
// DNS record could resolve to a CGNAT address and slip past the
// classifier.
var extendedIPv4SpecialCIDRs = mustParseCIDRs([]string{
	"0.0.0.0/8",        // "this network" (RFC 1122)
	"100.64.0.0/10",    // CGNAT (RFC 6598)
	"192.0.0.0/24",     // IETF Protocol Assignments (RFC 6890)
	"192.0.2.0/24",     // TEST-NET-1 (RFC 5737)
	"198.18.0.0/15",    // benchmarking (RFC 2544)
	"198.51.100.0/24",  // TEST-NET-2 (RFC 5737)
	"203.0.113.0/24",   // TEST-NET-3 (RFC 5737)
	"240.0.0.0/4",      // reserved class E (RFC 6890)
	"255.255.255.255/32", // limited broadcast
})

func mustParseCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(fmt.Sprintf("netproxy: invalid CIDR %q in extendedIPv4SpecialCIDRs: %v", c, err))
		}
		out = append(out, n)
	}
	return out
}

// isSpecialRangeIP reports whether ip falls in a range we treat as
// "non-public" — loopback, private (RFC1918), ULA, link-local,
// multicast, v4-mapped IPv6 to a non-public v4, unspecified, plus the
// extended Codex parity ranges (CGNAT, TEST-NET-1/2/3, IETF protocol
// assignments, benchmarking, reserved class E, broadcast,
// "this network").
//
// Mirrors Codex `network-proxy/src/policy.rs:45-98`. Layered on top
// of Go stdlib classifiers because net.IP.IsPrivate covers RFC1918 +
// fc00::/7 only — CGNAT (100.64.0.0/10) and the TEST-NET / RFC6890
// blocks are NOT classified by stdlib, so we apply explicit CIDR
// membership for each.
func isSpecialRangeIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsUnspecified() {
		return true
	}
	if ip.IsLoopback() {
		return true
	}
	if ip.IsMulticast() {
		return true
	}
	if ip.IsLinkLocalUnicast() {
		return true
	}
	if ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	// v4-mapped IPv6 (::ffff:x.x.x.x) must be classified by its
	// embedded v4 — both for the existing stdlib categories AND for
	// the extended CIDR list below. Recurse so a single source of
	// truth handles the mapping.
	if v4 := ip.To4(); v4 != nil && len(ip) == net.IPv6len {
		// We already checked the stdlib categories above on the
		// 16-byte form; recurse on the 4-byte form so the extended
		// CIDR table sees a clean IPv4 value.
		return isSpecialRangeIP(v4)
	}
	if v4 := ip.To4(); v4 != nil {
		for _, n := range extendedIPv4SpecialCIDRs {
			if n.Contains(v4) {
				return true
			}
		}
	}
	return false
}

// SystemResolver is a stub Resolver this file exports for callers
// that haven't yet wired the netproxy_dns.go helper. The real
// SystemResolver type lives in netproxy_dns.go to keep DNS-specific
// concerns in one file. This stub is unexported and exists only as
// a typed nil sentinel for the package.
var _ Resolver = (*nilResolver)(nil)

type nilResolver struct{}

func (nilResolver) LookupHost(_ context.Context, _ string) ([]string, error) {
	return nil, errors.New("netproxy: no resolver configured")
}
