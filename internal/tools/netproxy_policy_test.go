package tools

import (
	"context"
	"errors"
	"strings"
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
		"10.0.0.1",      // RFC1918
		"172.16.0.1",    // RFC1918
		"192.168.1.10",  // RFC1918
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
// Deny-wins semantics
// ---------------------------------------------------------------

func TestEvaluate_DenyWinsOverAllow(t *testing.T) {
	al, _ := ParseAllowlist([]string{"**.github.com:443"})
	dl, _ := ParseDenylist([]string{"evil.github.com:443"})
	d := EvaluateWithDenylist(context.Background(), al, dl, "evil.github.com", 443, ProtocolHTTPSConnect, nil)
	if d.Allow {
		t.Errorf("denylist should win over allowlist; got %+v", d)
	}
	if d.Reason != ReasonDeniedByRule {
		t.Errorf("expected ReasonDeniedByRule; got %q", d.Reason)
	}

	// Sibling subdomain not on denylist still allowed.
	d = EvaluateWithDenylist(context.Background(), al, dl, "api.github.com", 443, ProtocolHTTPSConnect, nil)
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
