// Package tools — netproxy_dns.go
//
// v41 / B3b-4-0 proxy core. Self-contained library used by the
// macOS Seatbelt and Linux bwrap substrate lanes (B3b-4-2, B3b-4-3)
// to enforce domain + IP allowlists for outbound TCP traffic. NOT
// wired into BashRunner in this lane.
//
// Partner-locked pins observed here:
//   - DNS rebinding is not fully defended (cite Codex
//     network-proxy/README.md:217-219). Resolver lookups happen at
//     each policy evaluation; if the upstream resolver rotates an A
//     record between two evaluations, the second sees the new IP.
//     Callers requiring rebinding defense MUST enforce at a lower
//     network layer (firewall, VPC, corporate proxy policies).
//   - No allowLocalBinding boolean. Local services are reached only
//     via explicit per-port entries.
//   - Forked-child self-exec proxy model. No in-process goroutine
//     proxy.
//   - Source enum is fixed at four values.
//   - No ProxyEnabled config flag — substrate lanes infer proxy need
//     from allowlist shape.

package tools

import (
	"context"
	"fmt"
	"net"
)

// SystemResolver wraps net.DefaultResolver.LookupHost. It is the
// production Resolver; the proxy uses it to resolve hostnames host-side
// before applying allowlist + special-range checks against the
// candidate IPs.
//
// Production callers should construct a single SystemResolver via
// NewSystemResolver and share it across requests. The underlying
// net.DefaultResolver is goroutine-safe.
type SystemResolver struct {
	inner *net.Resolver
}

// NewSystemResolver returns a Resolver backed by net.DefaultResolver.
// Elnath builds with CGO_ENABLED=0 (project invariant per CLAUDE.md
// "no CGo"), so the resolver is the pure-Go path; no GODEBUG
// override is required.
func NewSystemResolver() *SystemResolver {
	return &SystemResolver{inner: net.DefaultResolver}
}

// LookupHost resolves a hostname to its IP literal strings (A and AAAA
// records). On error returns a descriptive error wrapping the stdlib
// error. Honors ctx cancellation.
func (s *SystemResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	if s.inner == nil {
		return nil, fmt.Errorf("netproxy: SystemResolver not initialized")
	}
	ips, err := s.inner.LookupHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("netproxy: resolve %q: %w", host, err)
	}
	return ips, nil
}

// MockResolver is a Resolver for tests. The Hosts map maps hostnames
// to their canned IP-literal strings. Err, when non-nil, is returned
// from every LookupHost call (overrides Hosts). Both fields are
// exported so tests can construct via struct literal.
type MockResolver struct {
	Hosts map[string][]string
	Err   error
}

// NewMockResolver constructs a MockResolver with the given mapping
// and no preconfigured error.
func NewMockResolver(hosts map[string][]string) MockResolver {
	return MockResolver{Hosts: hosts}
}

// LookupHost returns the canned IPs for host or an error if host is
// unknown. If the resolver was constructed with an Err, that error is
// returned regardless of the host.
func (m MockResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	if ips, ok := m.Hosts[host]; ok {
		return ips, nil
	}
	return nil, fmt.Errorf("netproxy mock: no canned ips for %q", host)
}
