package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProxySource_IsValid(t *testing.T) {
	cases := []struct {
		name string
		src  ProxySource
		want bool
	}{
		{"network_proxy is valid", SourceNetworkProxy, true},
		{"sandbox_substrate is valid", SourceSandboxSubstrate, true},
		{"sandbox_substrate_heuristic is valid", SourceSandboxSubstrateHeuristic, true},
		{"dns_resolver is valid", SourceDNSResolver, true},
		{"empty source rejected", ProxySource(""), false},
		{"unknown source rejected", ProxySource("unknown"), false},
		{"capital case rejected", ProxySource("Network_Proxy"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.src.IsValid(); got != tc.want {
				t.Errorf("IsValid(%q) = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}

func TestProxyProtocol_IsValid(t *testing.T) {
	cases := []struct {
		name string
		p    ProxyProtocol
		want bool
	}{
		{"tcp is valid", ProtocolTCP, true},
		{"https_connect is valid", ProtocolHTTPSConnect, true},
		{"socks5_tcp is valid", ProtocolSOCKS5TCP, true},
		{"socks5_udp rejected (out of scope)", ProxyProtocol("socks5_udp"), false},
		{"http rejected (use https_connect)", ProxyProtocol("http"), false},
		{"empty protocol rejected", ProxyProtocol(""), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.IsValid(); got != tc.want {
				t.Errorf("IsValid(%q) = %v, want %v", tc.p, got, tc.want)
			}
		})
	}
}

func TestProxyReason_IsValid(t *testing.T) {
	valid := []ProxyReason{
		ReasonNotInAllowlist,
		ReasonDeniedByRule,
		ReasonDNSResolutionBlocked,
		ReasonLocalBindingDisabled,
		ReasonModeGuard,
		ReasonProtocolUnsupported,
		ReasonInvalidConfig,
	}
	for _, r := range valid {
		t.Run("valid_"+string(r), func(t *testing.T) {
			if !r.IsValid() {
				t.Errorf("expected %q to be valid", r)
			}
		})
	}
	invalid := []ProxyReason{"", "unknown_reason", "Not_In_Allowlist"}
	for _, r := range invalid {
		t.Run("invalid_"+string(r), func(t *testing.T) {
			if r.IsValid() {
				t.Errorf("expected %q to be invalid", r)
			}
		})
	}
}

func TestProxySource_StringRoundtrip(t *testing.T) {
	roundtrip := []ProxySource{
		SourceNetworkProxy,
		SourceSandboxSubstrate,
		SourceSandboxSubstrateHeuristic,
		SourceDNSResolver,
	}
	for _, s := range roundtrip {
		if got := s.String(); got != string(s) {
			t.Errorf("String(%q) = %q, want %q", s, got, s)
		}
		if !ProxySource(s.String()).IsValid() {
			t.Errorf("string roundtrip for %q lost validity", s)
		}
	}
}

func TestProxyProtocol_StringRoundtrip(t *testing.T) {
	for _, p := range []ProxyProtocol{ProtocolTCP, ProtocolHTTPSConnect, ProtocolSOCKS5TCP} {
		if got := p.String(); got != string(p) {
			t.Errorf("String(%q) = %q, want %q", p, got, p)
		}
		if !ProxyProtocol(p.String()).IsValid() {
			t.Errorf("string roundtrip for %q lost validity", p)
		}
	}
}

func TestNewAllow_Valid(t *testing.T) {
	d, err := NewAllow(SourceNetworkProxy, "github.com", 443, ProtocolHTTPSConnect)
	if err != nil {
		t.Fatalf("NewAllow returned unexpected error: %v", err)
	}
	if !d.Allow {
		t.Errorf("expected Allow=true")
	}
	if d.Source != SourceNetworkProxy {
		t.Errorf("Source = %q, want %q", d.Source, SourceNetworkProxy)
	}
	if d.Reason != "" {
		t.Errorf("Reason should be empty for allow decision, got %q", d.Reason)
	}
	if d.Host != "github.com" {
		t.Errorf("Host = %q, want github.com", d.Host)
	}
	if d.Port != 443 {
		t.Errorf("Port = %d, want 443", d.Port)
	}
	if d.Protocol != ProtocolHTTPSConnect {
		t.Errorf("Protocol = %q, want %q", d.Protocol, ProtocolHTTPSConnect)
	}
}

func TestNewAllow_RejectsInvalid(t *testing.T) {
	cases := []struct {
		name     string
		source   ProxySource
		host     string
		port     int
		protocol ProxyProtocol
		wantSub  string
	}{
		{"empty source rejected", "", "github.com", 443, ProtocolHTTPSConnect, "Source"},
		{"unknown source rejected", "garbage", "github.com", 443, ProtocolHTTPSConnect, "Source"},
		{"empty host rejected", SourceNetworkProxy, "", 443, ProtocolHTTPSConnect, "Host"},
		{"invalid protocol rejected", SourceNetworkProxy, "github.com", 443, "udp", "Protocol"},
		{"port too high rejected", SourceNetworkProxy, "github.com", 70000, ProtocolHTTPSConnect, "Port"},
		{"negative port rejected", SourceNetworkProxy, "github.com", -1, ProtocolHTTPSConnect, "Port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewAllow(tc.source, tc.host, tc.port, tc.protocol)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q should mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestNewDeny_Valid(t *testing.T) {
	d, err := NewDeny(SourceNetworkProxy, ReasonNotInAllowlist, "evil.com", 443, ProtocolHTTPSConnect)
	if err != nil {
		t.Fatalf("NewDeny returned unexpected error: %v", err)
	}
	if d.Allow {
		t.Errorf("expected Allow=false")
	}
	if d.Reason != ReasonNotInAllowlist {
		t.Errorf("Reason = %q, want %q", d.Reason, ReasonNotInAllowlist)
	}
	if d.Source != SourceNetworkProxy {
		t.Errorf("Source = %q, want %q", d.Source, SourceNetworkProxy)
	}
}

func TestNewDeny_RejectsInvalid(t *testing.T) {
	cases := []struct {
		name     string
		source   ProxySource
		reason   ProxyReason
		host     string
		port     int
		protocol ProxyProtocol
		wantSub  string
	}{
		{"empty source rejected", "", ReasonNotInAllowlist, "evil.com", 443, ProtocolHTTPSConnect, "Source"},
		{"empty reason rejected", SourceNetworkProxy, "", "evil.com", 443, ProtocolHTTPSConnect, "Reason"},
		{"unknown reason rejected", SourceNetworkProxy, "garbage", "evil.com", 443, ProtocolHTTPSConnect, "Reason"},
		{"empty host rejected", SourceNetworkProxy, ReasonNotInAllowlist, "", 443, ProtocolHTTPSConnect, "Host"},
		{"invalid protocol rejected", SourceNetworkProxy, ReasonNotInAllowlist, "evil.com", 443, "udp", "Protocol"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewDeny(tc.source, tc.reason, tc.host, tc.port, tc.protocol)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q should mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestDecision_JSONStableShape(t *testing.T) {
	d, err := NewDeny(SourceNetworkProxy, ReasonNotInAllowlist, "github.com", 443, ProtocolHTTPSConnect)
	if err != nil {
		t.Fatalf("NewDeny: %v", err)
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	want := `{"allow":false,"source":"network_proxy","reason":"not_in_allowlist","host":"github.com","port":443,"protocol":"https_connect"}`
	if got != want {
		t.Errorf("JSON = %s\nwant: %s", got, want)
	}
}

func TestDecision_JSONOmitsEmptyReasonOnAllow(t *testing.T) {
	d, err := NewAllow(SourceNetworkProxy, "github.com", 443, ProtocolHTTPSConnect)
	if err != nil {
		t.Fatalf("NewAllow: %v", err)
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "reason") {
		t.Errorf("allow JSON should omit empty reason; got %s", got)
	}
}

func TestEncodeAndParseDecisionEventLine(t *testing.T) {
	d, err := NewDeny(SourceNetworkProxy, ReasonNotInAllowlist, "evil.com", 443, ProtocolHTTPSConnect)
	if err != nil {
		t.Fatalf("NewDeny: %v", err)
	}
	line, err := EncodeDecisionEventLine(d)
	if err != nil {
		t.Fatalf("EncodeDecisionEventLine: %v", err)
	}
	if !strings.HasPrefix(line, "event=") {
		t.Errorf("encoded line missing event= prefix: %q", line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("encoded line missing trailing newline: %q", line)
	}

	parsed, ok, err := ParseDecisionEventLine(line)
	if err != nil {
		t.Fatalf("ParseDecisionEventLine: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true for event line")
	}
	if parsed != d {
		t.Errorf("round-trip mismatch: got %+v want %+v", parsed, d)
	}
}

func TestParseDecisionEventLine_NonEventLineSkipped(t *testing.T) {
	cases := []string{
		"httpListen=127.0.0.1:8080",
		"socksListen=/tmp/uds/socks.sock",
		"ready",
		"",
		"some debug noise",
	}
	for _, line := range cases {
		_, ok, err := ParseDecisionEventLine(line)
		if ok {
			t.Errorf("non-event line %q should yield ok=false", line)
		}
		if err != nil {
			t.Errorf("non-event line %q should not error; got %v", line, err)
		}
	}
}

func TestParseDecisionEventLine_MalformedJSONErrors(t *testing.T) {
	_, ok, err := ParseDecisionEventLine("event={not json")
	if !ok {
		t.Errorf("event-prefixed line should yield ok=true even when JSON malformed")
	}
	if err == nil {
		t.Errorf("expected JSON decode error for malformed body")
	}
}
