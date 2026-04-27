package tools

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------
// ParseAllowlist / ParseDenylist
// ---------------------------------------------------------------

func TestParseAllowlist_DomainGrammar(t *testing.T) {
	cases := []struct {
		name    string
		entries []string
		wantErr string
	}{
		{
			name:    "exact host with port accepted",
			entries: []string{"api.github.com:443"},
		},
		{
			name:    "single-label wildcard accepted",
			entries: []string{"*.github.com:443"},
		},
		{
			name:    "apex+sub wildcard accepted",
			entries: []string{"**.github.com:443"},
		},
		{
			name:    "ipv4 host accepted",
			entries: []string{"192.168.1.10:5432"},
		},
		{
			name:    "loopback v4 accepted",
			entries: []string{"127.0.0.1:5432"},
		},
		{
			name:    "ipv6 loopback accepted",
			entries: []string{"[::1]:5432"},
		},
		{
			name:    "ipv6 ULA accepted",
			entries: []string{"[fc00::1]:5432"},
		},
		{
			name:    "localhost literal accepted",
			entries: []string{"localhost:6379"},
		},
		{
			name:    "bare global wildcard rejected",
			entries: []string{"*:443"},
			wantErr: "global wildcard",
		},
		{
			name:    "scoped IPv6 literal rejected",
			entries: []string{"[fe80::1%en0]:443"},
			wantErr: "scoped IPv6",
		},
		{
			name:    "missing port rejected",
			entries: []string{"github.com"},
			wantErr: "port",
		},
		{
			name:    "port out of range rejected",
			entries: []string{"github.com:70000"},
			wantErr: "port",
		},
		{
			name:    "port zero rejected",
			entries: []string{"github.com:0"},
			wantErr: "port",
		},
		{
			name:    "empty entry rejected",
			entries: []string{""},
			wantErr: "empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, err := ParseAllowlist(tc.entries)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got allowlist=%+v", tc.wantErr, al)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error %q should mention %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if al.IsEmpty() {
				t.Errorf("expected non-empty allowlist for %v", tc.entries)
			}
		})
	}
}

func TestParseDenylist_AcceptsSameGrammar(t *testing.T) {
	dl, err := ParseDenylist([]string{"evil.example.com:443", "*.tracking.com:443"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dl.IsEmpty() {
		t.Errorf("expected non-empty denylist")
	}
}

func TestParseAllowlist_CaseInsensitive(t *testing.T) {
	al, err := ParseAllowlist([]string{"GitHub.COM:443"})
	if err != nil {
		t.Fatalf("ParseAllowlist: %v", err)
	}
	d := al.Evaluate(context.Background(), "github.com", 443, ProtocolHTTPSConnect, nil)
	if !d.Allow {
		t.Errorf("expected case-insensitive match; got %+v", d)
	}
}

func TestParseAllowlist_TrailingDotNormalized(t *testing.T) {
	al, err := ParseAllowlist([]string{"github.com.:443"})
	if err != nil {
		t.Fatalf("ParseAllowlist: %v", err)
	}
	d := al.Evaluate(context.Background(), "github.com", 443, ProtocolHTTPSConnect, nil)
	if !d.Allow {
		t.Errorf("expected trailing-dot normalized match; got %+v", d)
	}
}

// ---------------------------------------------------------------
// Domain matcher semantics
// ---------------------------------------------------------------

func TestAllowlist_ExactHostMatch(t *testing.T) {
	al, _ := ParseAllowlist([]string{"api.github.com:443"})
	got := al.Evaluate(context.Background(), "api.github.com", 443, ProtocolHTTPSConnect, nil)
	if !got.Allow {
		t.Errorf("expected allow; got %+v", got)
	}
	got = al.Evaluate(context.Background(), "github.com", 443, ProtocolHTTPSConnect, nil)
	if got.Allow {
		t.Errorf("apex should NOT match exact 'api.github.com'; got %+v", got)
	}
}

func TestAllowlist_SingleLabelWildcard(t *testing.T) {
	al, _ := ParseAllowlist([]string{"*.github.com:443"})

	// Subdomain matches.
	got := al.Evaluate(context.Background(), "api.github.com", 443, ProtocolHTTPSConnect, nil)
	if !got.Allow {
		t.Errorf("expected api.github.com to match *.github.com; got %+v", got)
	}
	// Apex does NOT match (Codex semantics).
	got = al.Evaluate(context.Background(), "github.com", 443, ProtocolHTTPSConnect, nil)
	if got.Allow {
		t.Errorf("apex github.com should NOT match *.github.com; got %+v", got)
	}
	// Different domain does not match.
	got = al.Evaluate(context.Background(), "evil.com", 443, ProtocolHTTPSConnect, nil)
	if got.Allow {
		t.Errorf("evil.com should not match *.github.com; got %+v", got)
	}
}

func TestAllowlist_ApexAndSubdomainsWildcard(t *testing.T) {
	al, _ := ParseAllowlist([]string{"**.github.com:443"})

	// Apex matches.
	if !al.Evaluate(context.Background(), "github.com", 443, ProtocolHTTPSConnect, nil).Allow {
		t.Errorf("apex github.com should match **.github.com")
	}
	// Subdomain matches.
	if !al.Evaluate(context.Background(), "api.github.com", 443, ProtocolHTTPSConnect, nil).Allow {
		t.Errorf("api.github.com should match **.github.com")
	}
	// Multi-level subdomain matches.
	if !al.Evaluate(context.Background(), "raw.api.github.com", 443, ProtocolHTTPSConnect, nil).Allow {
		t.Errorf("raw.api.github.com should match **.github.com")
	}
}

func TestAllowlist_PortMustMatch(t *testing.T) {
	al, _ := ParseAllowlist([]string{"github.com:443"})
	d := al.Evaluate(context.Background(), "github.com", 80, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("port 80 should NOT match entry github.com:443; got %+v", d)
	}
	if d.Reason != ReasonNotInAllowlist {
		t.Errorf("expected ReasonNotInAllowlist; got %q", d.Reason)
	}
}

// ---------------------------------------------------------------
// IP classifier — IPv4
// ---------------------------------------------------------------

func TestAllowlist_IPv4PublicAllowedExact(t *testing.T) {
	al, _ := ParseAllowlist([]string{"8.8.8.8:443"})
	if !al.Evaluate(context.Background(), "8.8.8.8", 443, ProtocolHTTPSConnect, nil).Allow {
		t.Errorf("8.8.8.8:443 should be allowed when explicitly listed")
	}
}

func TestAllowlist_IPv4LoopbackOnlyExact(t *testing.T) {
	// Per partner pin C3: no allowLocalBinding; only explicit per-port
	// allows reach loopback.
	emptyAl, _ := ParseAllowlist([]string{})
	d := emptyAl.Evaluate(context.Background(), "127.0.0.1", 5432, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("loopback should be denied without explicit allowlist; got %+v", d)
	}
	if d.Reason != ReasonLocalBindingDisabled {
		t.Errorf("expected ReasonLocalBindingDisabled for loopback default-deny; got %q", d.Reason)
	}

	// Explicit per-port entry permits.
	al, _ := ParseAllowlist([]string{"127.0.0.1:5432"})
	if !al.Evaluate(context.Background(), "127.0.0.1", 5432, ProtocolHTTPSConnect, nil).Allow {
		t.Errorf("127.0.0.1:5432 should be allowed with explicit entry")
	}
	// Different port denied.
	d = al.Evaluate(context.Background(), "127.0.0.1", 6379, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("127.0.0.1:6379 should be denied (only 5432 is explicit); got %+v", d)
	}
}

func TestAllowlist_IPv4PrivateBlockedByDefault(t *testing.T) {
	emptyAl, _ := ParseAllowlist([]string{})
	cases := []string{
		"10.0.0.1",        // RFC1918
		"172.16.0.1",      // RFC1918
		"192.168.1.10",    // RFC1918
		"169.254.169.254", // link-local
	}
	for _, ip := range cases {
		t.Run(ip, func(t *testing.T) {
			d := emptyAl.Evaluate(context.Background(), ip, 80, ProtocolHTTPSConnect, nil)
			if d.Allow {
				t.Errorf("expected %s to be blocked by default; got %+v", ip, d)
			}
			if d.Reason != ReasonLocalBindingDisabled {
				t.Errorf("expected ReasonLocalBindingDisabled; got %q", d.Reason)
			}
		})
	}
}

func TestAllowlist_IPv4MappedIPv6BlockedAsPrivateWhenMappedToPrivate(t *testing.T) {
	// ::ffff:127.0.0.1 is loopback per Codex policy.rs:84-85.
	emptyAl, _ := ParseAllowlist([]string{})
	d := emptyAl.Evaluate(context.Background(), "::ffff:127.0.0.1", 443, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("::ffff:127.0.0.1 should be treated as loopback; got %+v", d)
	}
	if d.Reason != ReasonLocalBindingDisabled {
		t.Errorf("expected ReasonLocalBindingDisabled; got %q", d.Reason)
	}
}

// ---------------------------------------------------------------
// IP classifier — IPv6
// ---------------------------------------------------------------

func TestAllowlist_IPv6LoopbackBlockedDefault(t *testing.T) {
	emptyAl, _ := ParseAllowlist([]string{})
	d := emptyAl.Evaluate(context.Background(), "::1", 443, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("expected ::1 to be blocked by default; got %+v", d)
	}
	if d.Reason != ReasonLocalBindingDisabled {
		t.Errorf("expected ReasonLocalBindingDisabled; got %q", d.Reason)
	}
}

func TestAllowlist_IPv6ULABlockedDefault(t *testing.T) {
	emptyAl, _ := ParseAllowlist([]string{})
	d := emptyAl.Evaluate(context.Background(), "fc00::1", 443, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("expected fc00::/7 ULA to be blocked; got %+v", d)
	}
	if d.Reason != ReasonLocalBindingDisabled {
		t.Errorf("expected ReasonLocalBindingDisabled; got %q", d.Reason)
	}
}

func TestAllowlist_IPv6LinkLocalBlockedDefault(t *testing.T) {
	emptyAl, _ := ParseAllowlist([]string{})
	d := emptyAl.Evaluate(context.Background(), "fe80::1", 443, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("expected fe80::/10 link-local to be blocked; got %+v", d)
	}
	if d.Reason != ReasonLocalBindingDisabled {
		t.Errorf("expected ReasonLocalBindingDisabled; got %q", d.Reason)
	}
}

func TestAllowlist_IPv6MulticastBlockedDefault(t *testing.T) {
	emptyAl, _ := ParseAllowlist([]string{})
	d := emptyAl.Evaluate(context.Background(), "ff02::1", 443, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("expected ff00::/8 multicast to be blocked; got %+v", d)
	}
}

func TestAllowlist_IPv4MulticastBlockedDefault(t *testing.T) {
	emptyAl, _ := ParseAllowlist([]string{})
	d := emptyAl.Evaluate(context.Background(), "224.0.0.1", 443, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("expected 224.0.0.0/4 multicast to be blocked; got %+v", d)
	}
}

func TestAllowlist_IPv6UnspecifiedBlockedDefault(t *testing.T) {
	emptyAl, _ := ParseAllowlist([]string{})
	d := emptyAl.Evaluate(context.Background(), "::", 443, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("expected :: unspecified to be blocked; got %+v", d)
	}
}

func TestAllowlist_IPv4UnspecifiedBlockedDefault(t *testing.T) {
	emptyAl, _ := ParseAllowlist([]string{})
	d := emptyAl.Evaluate(context.Background(), "0.0.0.0", 80, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("expected 0.0.0.0 unspecified to be blocked; got %+v", d)
	}
}

// ---------------------------------------------------------------
// M1 — IPv4 special-range parity with Codex policy.rs:52-70
// ---------------------------------------------------------------
// The classifier MUST reject every range Codex's is_non_public_ipv4
// classifies as non-public so that allowlisted hostnames cannot be
// silently bypassed by resolving to a CGNAT / TEST-NET / RFC6890
// address. CGNAT is the realistic SSRF vector — net.IP.IsPrivate
// does NOT classify 100.64.0.0/10 as private.

func TestIsSpecialRangeIP_IPv4ExtendedRanges(t *testing.T) {
	cases := []struct {
		name string
		ip   string
	}{
		{"this network 0.0.0.0/8 mid", "0.1.2.3"},
		{"this network 0.0.0.0/8 high", "0.255.255.254"},
		{"CGNAT 100.64.0.0/10 low", "100.64.0.1"},
		{"CGNAT 100.64.0.0/10 mid", "100.96.42.42"},
		{"CGNAT 100.64.0.0/10 high", "100.127.255.254"},
		{"IETF 192.0.0.0/24", "192.0.0.1"},
		{"TEST-NET-1 192.0.2.0/24", "192.0.2.1"},
		{"benchmarking 198.18.0.0/15 low", "198.18.0.1"},
		{"benchmarking 198.18.0.0/15 high", "198.19.255.254"},
		{"TEST-NET-2 198.51.100.0/24", "198.51.100.1"},
		{"TEST-NET-3 203.0.113.0/24", "203.0.113.1"},
		{"reserved class E 240.0.0.0/4 low", "240.0.0.1"},
		{"reserved class E 240.0.0.0/4 high", "254.255.255.254"},
		{"broadcast 255.255.255.255", "255.255.255.255"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("ParseIP(%q) returned nil", tc.ip)
			}
			if !isSpecialRangeIP(ip) {
				t.Errorf("isSpecialRangeIP(%s) = false; want true (%s)", tc.ip, tc.name)
			}
		})
	}
}

func TestIsSpecialRangeIP_IPv4PublicAddressesNotSpecial(t *testing.T) {
	// Sanity: nearby public addresses must remain non-special so the
	// classifier doesn't widen too far. These are the boundary
	// addresses just outside each new CIDR.
	cases := []struct {
		name string
		ip   string
	}{
		{"just outside CGNAT low", "100.63.255.254"},
		{"just outside CGNAT high", "100.128.0.1"},
		{"just outside TEST-NET-1", "192.0.3.1"},
		{"just outside benchmarking low", "198.17.255.254"},
		{"just outside benchmarking high", "198.20.0.1"},
		{"just outside TEST-NET-2", "198.51.99.1"},
		{"just outside TEST-NET-3", "203.0.114.1"},
		{"public Google DNS", "8.8.8.8"},
		{"public Cloudflare DNS", "1.1.1.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("ParseIP(%q) returned nil", tc.ip)
			}
			if isSpecialRangeIP(ip) {
				t.Errorf("isSpecialRangeIP(%s) = true; want false (%s)", tc.ip, tc.name)
			}
		})
	}
}

func TestIsSpecialRangeIP_IPv4MappedExtendedRanges(t *testing.T) {
	// ::ffff:x.x.x.x form must classify by the embedded v4 — this
	// closes the v4-mapped IPv6 SSRF bypass that would otherwise let a
	// CGNAT address slip past the v4 classifier.
	cases := []struct {
		name string
		ip   string
	}{
		{"v4-mapped CGNAT", "::ffff:100.64.1.1"},
		{"v4-mapped TEST-NET-1", "::ffff:192.0.2.1"},
		{"v4-mapped TEST-NET-2", "::ffff:198.51.100.1"},
		{"v4-mapped TEST-NET-3", "::ffff:203.0.113.1"},
		{"v4-mapped reserved class E", "::ffff:240.0.0.1"},
		{"v4-mapped this network", "::ffff:0.1.2.3"},
		{"v4-mapped benchmarking", "::ffff:198.18.0.1"},
		{"v4-mapped IETF", "::ffff:192.0.0.1"},
		{"v4-mapped broadcast", "::ffff:255.255.255.255"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("ParseIP(%q) returned nil", tc.ip)
			}
			if !isSpecialRangeIP(ip) {
				t.Errorf("isSpecialRangeIP(%s) = false; want true (%s)", tc.ip, tc.name)
			}
		})
	}
}

func TestAllowlist_IPv4CGNATBlockedByDefault(t *testing.T) {
	// Realistic SSRF surface: an attacker-controlled DNS record
	// resolves an allowlisted hostname to a CGNAT address. The
	// resolver-side IP check must classify CGNAT as non-public so the
	// connection is denied.
	emptyAl, _ := ParseAllowlist([]string{})
	d := emptyAl.Evaluate(context.Background(), "100.64.1.1", 443, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("CGNAT 100.64.1.1 should be denied by default; got %+v", d)
	}
	if d.Reason != ReasonLocalBindingDisabled {
		t.Errorf("expected ReasonLocalBindingDisabled for CGNAT default-deny; got %q", d.Reason)
	}
}

func TestAllowlist_IPv4TestNetBlockedByDefault(t *testing.T) {
	emptyAl, _ := ParseAllowlist([]string{})
	cases := []string{
		"192.0.2.1",    // TEST-NET-1
		"198.51.100.1", // TEST-NET-2
		"203.0.113.1",  // TEST-NET-3
	}
	for _, ip := range cases {
		t.Run(ip, func(t *testing.T) {
			d := emptyAl.Evaluate(context.Background(), ip, 443, ProtocolHTTPSConnect, nil)
			if d.Allow {
				t.Errorf("TEST-NET %s should be denied by default; got %+v", ip, d)
			}
			if d.Reason != ReasonLocalBindingDisabled {
				t.Errorf("expected ReasonLocalBindingDisabled for %s; got %q", ip, d.Reason)
			}
		})
	}
}

func TestAllowlist_IPv4BroadcastBlockedByDefault(t *testing.T) {
	emptyAl, _ := ParseAllowlist([]string{})
	d := emptyAl.Evaluate(context.Background(), "255.255.255.255", 80, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("broadcast 255.255.255.255 should be denied by default; got %+v", d)
	}
	if d.Reason != ReasonLocalBindingDisabled {
		t.Errorf("expected ReasonLocalBindingDisabled for broadcast; got %q", d.Reason)
	}
}

func TestAllowlist_HostnameResolvesToCGNATBlocked(t *testing.T) {
	// Hostname is allowlisted; resolver returns a CGNAT IP. The
	// resolved-IP check must catch this — without M1 the classifier
	// would let it through because net.IP.IsPrivate does NOT cover
	// 100.64.0.0/10.
	al, _ := ParseAllowlist([]string{"shady.example.com:443"})
	r := &stubResolver{hosts: map[string][]string{"shady.example.com": {"100.64.1.1"}}}
	d := al.Evaluate(context.Background(), "shady.example.com", 443, ProtocolHTTPSConnect, r)
	if d.Allow {
		t.Errorf("hostname resolving to CGNAT should be denied; got %+v", d)
	}
	if d.Reason != ReasonLocalBindingDisabled {
		t.Errorf("expected ReasonLocalBindingDisabled; got %q", d.Reason)
	}
}

// ---------------------------------------------------------------
// Deny-wins semantics
// ---------------------------------------------------------------

func TestEvaluate_DenyWinsOverAllow(t *testing.T) {
	al, _ := ParseAllowlist([]string{"**.github.com:443"})
	dl, _ := ParseDenylist([]string{"evil.github.com:443"})
	d, _ := EvaluateWithDenylist(context.Background(), al, dl, "evil.github.com", 443, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("denylist should win over allowlist; got %+v", d)
	}
	if d.Reason != ReasonDeniedByRule {
		t.Errorf("expected ReasonDeniedByRule; got %q", d.Reason)
	}

	// Sibling subdomain not on denylist still allowed.
	d, _ = EvaluateWithDenylist(context.Background(), al, dl, "api.github.com", 443, ProtocolHTTPSConnect, nil)
	if !d.Allow {
		t.Errorf("api.github.com should be allowed; got %+v", d)
	}
}

func TestEvaluate_NotInAllowlistDenied(t *testing.T) {
	al, _ := ParseAllowlist([]string{"github.com:443"})
	d := al.Evaluate(context.Background(), "gitlab.com", 443, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("gitlab.com not in allowlist should be denied; got %+v", d)
	}
	if d.Reason != ReasonNotInAllowlist {
		t.Errorf("expected ReasonNotInAllowlist; got %q", d.Reason)
	}
}

// ---------------------------------------------------------------
// Hostname resolution via injected resolver
// ---------------------------------------------------------------

type stubResolver struct {
	hosts map[string][]string
	err   error
}

func (s *stubResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	if ips, ok := s.hosts[host]; ok {
		return ips, nil
	}
	return nil, errors.New("no such host: " + host)
}

func TestEvaluate_HostnameResolvesAndChecksIP(t *testing.T) {
	al, _ := ParseAllowlist([]string{"api.github.com:443"})
	r := &stubResolver{hosts: map[string][]string{"api.github.com": {"140.82.112.5"}}}

	// Allowlist hits the hostname directly (no resolution required).
	d := al.Evaluate(context.Background(), "api.github.com", 443, ProtocolHTTPSConnect, r)
	if !d.Allow {
		t.Errorf("expected hostname allow; got %+v", d)
	}
}

func TestEvaluate_HostnameResolvesToBlockedLocalIP(t *testing.T) {
	// Hostname is on allowlist BUT resolves to a private IP — the
	// resolved-IP check should block it.
	al, _ := ParseAllowlist([]string{"shady.example.com:443"})
	r := &stubResolver{hosts: map[string][]string{"shady.example.com": {"10.0.0.5"}}}

	d := al.Evaluate(context.Background(), "shady.example.com", 443, ProtocolHTTPSConnect, r)
	if d.Allow {
		t.Errorf("hostname resolving to private IP should be blocked; got %+v", d)
	}
	if d.Reason != ReasonLocalBindingDisabled {
		t.Errorf("expected ReasonLocalBindingDisabled; got %q", d.Reason)
	}
	if d.Source != SourceDNSResolver && d.Source != SourceNetworkProxy {
		t.Errorf("expected proxy or dns_resolver source; got %q", d.Source)
	}
}

func TestEvaluate_DNSResolutionFailureIsBlocked(t *testing.T) {
	al, _ := ParseAllowlist([]string{"unreachable.example.com:443"})
	r := &stubResolver{err: errors.New("simulated dns failure")}
	d := al.Evaluate(context.Background(), "unreachable.example.com", 443, ProtocolHTTPSConnect, r)
	if d.Allow {
		t.Errorf("DNS resolution failure should not allow; got %+v", d)
	}
	if d.Reason != ReasonDNSResolutionBlocked {
		t.Errorf("expected ReasonDNSResolutionBlocked; got %q", d.Reason)
	}
	if d.Source != SourceDNSResolver {
		t.Errorf("expected SourceDNSResolver; got %q", d.Source)
	}
}

func TestEvaluate_AllowAlwaysCarriesValidSource(t *testing.T) {
	al, _ := ParseAllowlist([]string{"github.com:443"})
	d := al.Evaluate(context.Background(), "github.com", 443, ProtocolHTTPSConnect, nil)
	if !d.Source.IsValid() {
		t.Errorf("Source must be valid even on allow; got %q", d.Source)
	}
	if d.Source != SourceNetworkProxy {
		t.Errorf("expected SourceNetworkProxy on allow; got %q", d.Source)
	}
}

func TestEvaluate_DenyAlwaysCarriesValidSource(t *testing.T) {
	al, _ := ParseAllowlist([]string{"github.com:443"})
	d := al.Evaluate(context.Background(), "evil.com", 443, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Fatalf("expected deny")
	}
	if !d.Source.IsValid() {
		t.Errorf("Source must be valid on deny; got %q", d.Source)
	}
	if !d.Reason.IsValid() {
		t.Errorf("Reason must be valid on deny; got %q", d.Reason)
	}
}

// ---------------------------------------------------------------
// IsEmpty / housekeeping
// ---------------------------------------------------------------

func TestAllowlist_EmptyEvaluatesAsDeny(t *testing.T) {
	al, err := ParseAllowlist(nil)
	if err != nil {
		t.Fatalf("ParseAllowlist(nil) should succeed; got %v", err)
	}
	if !al.IsEmpty() {
		t.Errorf("expected IsEmpty() true")
	}
	d := al.Evaluate(context.Background(), "github.com", 443, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("empty allowlist should default-deny; got %+v", d)
	}
}

// ---------------------------------------------------------------
// v42-3 — resolve-pin tuple-return tests (#3, #4, #7, #10, #14)
// ---------------------------------------------------------------
//
// Tests in this section exercise the v42-3 contract that
// EvaluateWithDenylist returns (Decision, []net.IP) where the second
// element is the slice of guard-passing pinned IPs that the dial site
// must consume. Deny outcomes return pinnedIPs == nil. The empty-DNS
// edge case (err==nil && len==0) maps to the existing
// dns_resolution_blocked deny path. The taxonomy of Source values is
// preserved — DNS-originated denials remain SourceDNSResolver; policy
// allow/denylist denials remain SourceNetworkProxy.

// TestEvaluateWithDenylist_LoopbackResolvedForAllowlistedDomainBlocked
// — Partner #3. An allowlisted hostname whose resolver returns 127.0.0.1
// MUST be denied by the resolved-IP guard with SourceDNSResolver +
// ReasonLocalBindingDisabled, AND the returned pinned slice MUST be
// nil (no dial allowed).
func TestEvaluateWithDenylist_LoopbackResolvedForAllowlistedDomainBlocked(t *testing.T) {
	al, _ := ParseAllowlist([]string{"shady.example.com:443"})
	dl := Denylist{}
	resolver := NewMockResolver(map[string][]string{"shady.example.com": {"127.0.0.1"}})

	decision, pinned := EvaluateWithDenylist(
		context.Background(), al, dl,
		"shady.example.com", 443, ProtocolHTTPSConnect, resolver,
	)
	if decision.Allow {
		t.Fatalf("loopback-resolved allowlisted host must be denied; got %+v", decision)
	}
	if decision.Source != SourceDNSResolver {
		t.Errorf("expected SourceDNSResolver; got %q", decision.Source)
	}
	if decision.Reason != ReasonLocalBindingDisabled {
		t.Errorf("expected ReasonLocalBindingDisabled; got %q", decision.Reason)
	}
	if decision.Host != "shady.example.com" {
		t.Errorf("expected Host=shady.example.com; got %q", decision.Host)
	}
	if pinned != nil {
		t.Errorf("expected pinned == nil for deny outcome; got %v", pinned)
	}
}

// TestEvaluateWithDenylist_CGNATResolvedForAllowlistedDomainBlocked
// — Partner #4. An allowlisted hostname resolving to a CGNAT IP
// (100.64.0.0/10) MUST be denied with SourceDNSResolver +
// ReasonLocalBindingDisabled and pinned == nil. CGNAT is the realistic
// SSRF surface — net.IP.IsPrivate does NOT cover 100.64.0.0/10.
func TestEvaluateWithDenylist_CGNATResolvedForAllowlistedDomainBlocked(t *testing.T) {
	al, _ := ParseAllowlist([]string{"shady.example.com:443"})
	dl := Denylist{}
	resolver := NewMockResolver(map[string][]string{"shady.example.com": {"100.64.1.1"}})

	decision, pinned := EvaluateWithDenylist(
		context.Background(), al, dl,
		"shady.example.com", 443, ProtocolHTTPSConnect, resolver,
	)
	if decision.Allow {
		t.Fatalf("CGNAT-resolved allowlisted host must be denied; got %+v", decision)
	}
	if decision.Source != SourceDNSResolver {
		t.Errorf("expected SourceDNSResolver; got %q", decision.Source)
	}
	if decision.Reason != ReasonLocalBindingDisabled {
		t.Errorf("expected ReasonLocalBindingDisabled; got %q", decision.Reason)
	}
	if pinned != nil {
		t.Errorf("expected pinned == nil for deny outcome; got %v", pinned)
	}
}

// TestEvaluateWithDenylist_MultipleAAAAOnlyPinnedAllowedIPsUsed
// — Partner #7 / Q1 all-or-nothing. A multi-IP DNS answer where ALL
// entries pass the special-range guard MUST yield an Allow decision
// AND the returned pinned slice MUST contain ONLY guard-passing IPs
// in resolver-emitted order. Verifies the architect's multi-IP shape.
func TestEvaluateWithDenylist_MultipleAAAAOnlyPinnedAllowedIPsUsed(t *testing.T) {
	al, _ := ParseAllowlist([]string{"sentinel.test:443"})
	dl := Denylist{}
	resolver := NewMockResolver(map[string][]string{
		"sentinel.test": {"2001:db8::1", "2001:db8::2"},
	})

	decision, pinned := EvaluateWithDenylist(
		context.Background(), al, dl,
		"sentinel.test", 443, ProtocolHTTPSConnect, resolver,
	)
	if !decision.Allow {
		t.Fatalf("expected Allow when all resolved IPs are public; got %+v", decision)
	}
	if decision.Source != SourceNetworkProxy {
		t.Errorf("expected SourceNetworkProxy; got %q", decision.Source)
	}
	if len(pinned) != 2 {
		t.Fatalf("expected 2 pinned IPs; got %d (%v)", len(pinned), pinned)
	}
	wantOrder := []string{"2001:db8::1", "2001:db8::2"}
	for i, want := range wantOrder {
		if pinned[i].String() != want {
			t.Errorf("pinned[%d] = %q; want %q", i, pinned[i].String(), want)
		}
	}
}

// TestEvaluateWithDenylist_DenySourcesPreserveTaxonomy — Partner #10
// post-verdict (option B). Verifies the 4-row Source/Reason taxonomy
// is preserved across distinct deny paths. Renamed from the original
// "AlsoCarriesNetworkProxySource" naming, which embedded a literal-text
// reading the partner verdict explicitly REJECTED. All four rows
// reference constants that exist in netproxy_event.go (zero invented).
func TestEvaluateWithDenylist_DenySourcesPreserveTaxonomy(t *testing.T) {
	cases := []struct {
		name       string
		allow      []string
		deny       []string
		host       string
		port       int
		resolver   Resolver
		wantSource ProxySource
		wantReason ProxyReason
	}{
		{
			name:       "denylist match (deny-wins)",
			allow:      []string{"**.github.com:443"},
			deny:       []string{"evil.github.com:443"},
			host:       "evil.github.com",
			port:       443,
			resolver:   nil,
			wantSource: SourceNetworkProxy,
			wantReason: ReasonDeniedByRule,
		},
		{
			name:       "hostname not in allowlist",
			allow:      []string{"github.com:443"},
			deny:       nil,
			host:       "gitlab.com",
			port:       443,
			resolver:   nil,
			wantSource: SourceNetworkProxy,
			wantReason: ReasonNotInAllowlist,
		},
		{
			name:       "DNS resolution failure",
			allow:      []string{"unreachable.example.com:443"},
			deny:       nil,
			host:       "unreachable.example.com",
			port:       443,
			resolver:   MockResolver{Err: errors.New("simulated dns failure")},
			wantSource: SourceDNSResolver,
			wantReason: ReasonDNSResolutionBlocked,
		},
		{
			name:       "allowlisted hostname resolves to special-range IP",
			allow:      []string{"shady.example.com:443"},
			deny:       nil,
			host:       "shady.example.com",
			port:       443,
			resolver:   NewMockResolver(map[string][]string{"shady.example.com": {"10.0.0.5"}}),
			wantSource: SourceDNSResolver,
			wantReason: ReasonLocalBindingDisabled,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, err := ParseAllowlist(tc.allow)
			if err != nil {
				t.Fatalf("ParseAllowlist: %v", err)
			}
			dl, err := ParseDenylist(tc.deny)
			if err != nil {
				t.Fatalf("ParseDenylist: %v", err)
			}
			decision, pinned := EvaluateWithDenylist(
				context.Background(), al, dl,
				tc.host, tc.port, ProtocolHTTPSConnect, tc.resolver,
			)
			if decision.Allow {
				t.Fatalf("expected deny; got %+v", decision)
			}
			if decision.Source != tc.wantSource {
				t.Errorf("Source = %q; want %q", decision.Source, tc.wantSource)
			}
			if decision.Reason != tc.wantReason {
				t.Errorf("Reason = %q; want %q", decision.Reason, tc.wantReason)
			}
			if pinned != nil {
				t.Errorf("expected pinned == nil for deny outcome; got %v", pinned)
			}
		})
	}
}

// TestEvaluateWithDenylist_EmptyDNSAnswerBlocked — Fix 3 / Test #14.
// A resolver that returns (ips=[], err=nil) — operationally equivalent
// to "host had no A/AAAA records" — MUST map to the
// dns_resolution_blocked deny path with pinned == nil. This extends
// the existing err≠nil deny path at netproxy_policy.go:330-332 to
// cover the err==nil but len==0 case, eliminating the divide-by-zero
// risk at the dial site.
func TestEvaluateWithDenylist_EmptyDNSAnswerBlocked(t *testing.T) {
	al, _ := ParseAllowlist([]string{"sentinel.test:443"})
	dl := Denylist{}
	tracking := &countingResolver{
		inner: NewMockResolver(map[string][]string{"sentinel.test": {}}),
	}

	decision, pinned := EvaluateWithDenylist(
		context.Background(), al, dl,
		"sentinel.test", 443, ProtocolHTTPSConnect, tracking,
	)
	if decision.Allow {
		t.Fatalf("empty DNS answer must be denied; got %+v", decision)
	}
	if decision.Source != SourceDNSResolver {
		t.Errorf("expected SourceDNSResolver; got %q", decision.Source)
	}
	if decision.Reason != ReasonDNSResolutionBlocked {
		t.Errorf("expected ReasonDNSResolutionBlocked; got %q", decision.Reason)
	}
	if decision.Host != "sentinel.test" {
		t.Errorf("expected Host=sentinel.test; got %q", decision.Host)
	}
	if pinned != nil {
		t.Errorf("expected pinned == nil; got %v", pinned)
	}
	if got := tracking.Count(); got != 1 {
		t.Errorf("expected exactly 1 LookupHost call; got %d", got)
	}
}

// countingResolver is a Resolver wrapper used by tests that need to
// assert the policy resolver was consulted exactly once.
type countingResolver struct {
	inner Resolver
	mu    sync.Mutex
	calls int
}

func (c *countingResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.inner.LookupHost(ctx, host)
}

func (c *countingResolver) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}
