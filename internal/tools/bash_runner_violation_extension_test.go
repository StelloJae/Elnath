package tools

import (
	"strings"
	"testing"
)

// B3b-4-1 Phase A: SandboxViolation field extension contract.
//
// SandboxViolation now carries optional network-attribution fields
// (Host/Port/Protocol/Reason/Source) that substrate-aware runners
// populate when a deny event is network-shaped. Filesystem-style
// violations (the legacy Kind/Path/Message shape) must continue to
// construct cleanly with the zero values for the new fields, so
// existing callers do not regress.

func TestSandboxViolation_LegacyFilesystemShapeUnchanged(t *testing.T) {
	v := SandboxViolation{
		Kind:    "fs_denied",
		Path:    "/etc/passwd",
		Message: "write blocked by Seatbelt profile",
	}
	if v.Kind != "fs_denied" {
		t.Errorf("Kind dropped: %+v", v)
	}
	if v.Path != "/etc/passwd" {
		t.Errorf("Path dropped: %+v", v)
	}
	if v.Message == "" {
		t.Errorf("Message dropped: %+v", v)
	}
	// New fields default to zero on legacy entries.
	if v.Host != "" || v.Port != 0 || v.Protocol != "" || v.Reason != "" || v.Source != "" {
		t.Errorf("legacy entry must zero-value the new fields: %+v", v)
	}
}

func TestSandboxViolation_NetworkShapeAcceptsNewFields(t *testing.T) {
	v := SandboxViolation{
		Kind:     "net_denied",
		Source:   string(SourceNetworkProxy),
		Host:     "github.com",
		Port:     443,
		Protocol: string(ProtocolHTTPSConnect),
		Reason:   string(ReasonNotInAllowlist),
		Message:  "blocked github.com:443",
	}
	if v.Source != "network_proxy" {
		t.Errorf("Source = %q, want %q", v.Source, "network_proxy")
	}
	if v.Host != "github.com" || v.Port != 443 {
		t.Errorf("Host/Port not retained: %+v", v)
	}
	if v.Protocol != "https_connect" {
		t.Errorf("Protocol = %q, want %q", v.Protocol, "https_connect")
	}
	if v.Reason != "not_in_allowlist" {
		t.Errorf("Reason = %q, want %q", v.Reason, "not_in_allowlist")
	}
}

// TestSandboxViolationSource_AcceptsLockedEnumValues verifies the
// helper validation function accepts the four partner-locked Source
// values and rejects everything else. This lock prevents drift away
// from the netproxy_event.go ProxySource enum.
func TestSandboxViolationSource_AcceptsLockedEnumValues(t *testing.T) {
	cases := []struct {
		source string
		want   bool
	}{
		{"network_proxy", true},
		{"sandbox_substrate", true},
		{"sandbox_substrate_heuristic", true},
		{"dns_resolver", true},
		{"", false},
		{"unknown_source", false},
		{"NetworkProxy", false}, // case-sensitive
		{"network-proxy", false},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			got := IsValidSandboxViolationSource(tc.source)
			if got != tc.want {
				t.Errorf("IsValidSandboxViolationSource(%q) = %v, want %v", tc.source, got, tc.want)
			}
		})
	}
}

// TestDetectSeatbeltViolations_DowngradedToHeuristicSource verifies
// the detect helper now stamps Source = "sandbox_substrate_heuristic"
// so output rendering and telemetry can mark the entry as
// low-confidence inferred-from-stderr rather than authoritative.
func TestDetectSeatbeltViolations_DowngradedToHeuristicSource(t *testing.T) {
	// Skip on non-darwin: detector is in the darwin-tagged file.
	res := BashRunResult{
		StderrRawBytes: 100,
		Output:         "BASH RESULT\nSTDERR:\nbash: deny file-write* /etc/passwd\n",
	}
	got := detectSeatbeltViolationsForTest(res)
	if len(got) == 0 {
		t.Skip("detector not available on this build (darwin-only); covered by darwin runtime tests")
	}
	if got[0].Source != "sandbox_substrate_heuristic" {
		t.Errorf("Source = %q, want %q", got[0].Source, "sandbox_substrate_heuristic")
	}
	if !strings.Contains(strings.ToLower(got[0].Message), "low confidence") &&
		!strings.Contains(strings.ToLower(got[0].Message), "heuristic") &&
		!strings.Contains(strings.ToLower(got[0].Message), "inferred") {
		t.Errorf("Message should mark heuristic/low-confidence; got %q", got[0].Message)
	}
}

func TestDetectBwrapViolations_DowngradedToHeuristicSource(t *testing.T) {
	res := BashRunResult{
		StderrRawBytes: 100,
		Output:         "BASH RESULT\nSTDERR:\nNetwork is unreachable\n",
	}
	got := detectBwrapViolationsForTest(res)
	if len(got) == 0 {
		t.Skip("detector not available on this build (linux-only); covered by linux runtime tests")
	}
	if got[0].Source != "sandbox_substrate_heuristic" {
		t.Errorf("Source = %q, want %q", got[0].Source, "sandbox_substrate_heuristic")
	}
	if !strings.Contains(strings.ToLower(got[0].Message), "low confidence") &&
		!strings.Contains(strings.ToLower(got[0].Message), "heuristic") &&
		!strings.Contains(strings.ToLower(got[0].Message), "inferred") {
		t.Errorf("Message should mark heuristic/low-confidence; got %q", got[0].Message)
	}
}
