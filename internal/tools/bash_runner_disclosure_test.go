package tools

import (
	"strings"
	"testing"
	"time"
)

// B3b-4-1 Phase E: N5 operator-tunable connectIOTimeout (parser only,
// no substrate wiring) + partner-locked disclosure helper.

// TestSandboxConfig_NetworkProxyConnectTimeoutDefault verifies the
// resolver returns the package default when the field is zero.
func TestSandboxConfig_NetworkProxyConnectTimeoutDefault(t *testing.T) {
	cfg := SandboxConfig{Mode: "seatbelt"}
	got := cfg.ResolvedNetworkProxyConnectTimeout()
	if got != netproxyDefaultConnectTimeout {
		t.Errorf("default = %v, want %v", got, netproxyDefaultConnectTimeout)
	}
	if got != 30*time.Second {
		t.Errorf("default expected 30s baseline; got %v", got)
	}
}

func TestSandboxConfig_NetworkProxyConnectTimeoutOperatorOverride(t *testing.T) {
	cfg := SandboxConfig{
		Mode:                       "seatbelt",
		NetworkProxyConnectTimeout: 90 * time.Second,
	}
	if got := cfg.ResolvedNetworkProxyConnectTimeout(); got != 90*time.Second {
		t.Errorf("override = %v, want 90s", got)
	}
}

// TestNetworkProxyDisclosure_EmitsThreePartnerLockedSentences pins
// the verbatim disclosure grammar from the v41 partner verdict.
func TestNetworkProxyDisclosure_EmitsThreePartnerLockedSentences(t *testing.T) {
	got := networkProxyDisclosure()
	for _, want := range []string{
		"Network allowlist changes require Elnath restart.",
		"UDP and QUIC egress are blocked in this sandbox version.",
		"DNS rebinding is not fully defended; for hostile DNS threat models, enforce egress at a lower layer.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("disclosure missing partner-locked sentence %q; got: %q", want, got)
		}
	}
}

// TestFactoryRejection_IncludesAllThreeDisclosureSentences confirms
// the factory rejection error surfaces the full disclosure so the
// operator learns the Phase 1 invariants without consulting external
// docs.
//
// After B3b-4-3 the bwrap factory accepts proxy-required entries
// (domain, non-loopback IP) and only rejects loopback-only entries
// (no SBPL-equivalent loopback rule on bwrap, netns blocks all
// egress). We exercise the loopback rejection path because it is
// portable across all platforms (no darwin gate needed) and still
// surfaces the disclosure.
func TestFactoryRejection_IncludesAllThreeDisclosureSentences(t *testing.T) {
	_, err := NewBashRunnerForConfig(SandboxConfig{
		Mode:             "bwrap",
		NetworkAllowlist: []string{"127.0.0.1:8080"},
	})
	if err == nil {
		t.Fatal("expected rejection")
	}
	for _, want := range []string{
		"restart",
		"UDP and QUIC",
		"DNS rebinding",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("rejection error missing disclosure substring %q; got: %v", want, err)
		}
	}
}
