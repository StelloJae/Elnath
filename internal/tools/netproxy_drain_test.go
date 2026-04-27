package tools

import (
	"reflect"
	"sync"
	"testing"
)

// v42-2 RED tests for the priority-aware bounded decision buffer.
// The buffer type, helpers, and projectAuditRecordsFromAllowOnly are
// authored in this commit's GREEN successor; these tests fail to
// compile against the v42-1b production tree until that lands.

// TestDecisionBuffer_AllowFloodCapsAtAllowCap pins the allow-side
// drain-time enforcement: pushing 10000 allow Decisions through a
// buffer with allowCap=200 retains exactly 200 and counts the
// remaining 9800 as drops.
func TestDecisionBuffer_AllowFloodCapsAtAllowCap(t *testing.T) {
	b := newDecisionBufferForTest(64, 200)
	for i := 0; i < 10000; i++ {
		b.Push(Decision{
			Allow:    true,
			Source:   SourceNetworkProxy,
			Host:     "host.example",
			Port:     443,
			Protocol: ProtocolHTTPSConnect,
		})
	}
	_, allow, _, allowDrops := b.Drain()
	if len(allow) != 200 {
		t.Errorf("len(allow) = %d, want 200", len(allow))
	}
	if allowDrops != 9800 {
		t.Errorf("allowDrops = %d, want 9800", allowDrops)
	}
}

// TestDecisionBuffer_DenyRetainedDuringAllowFlood pins the deny-buffer
// isolation contract: an allow-side flood must not cause any deny
// Decision to be dropped because the two slices are independent.
func TestDecisionBuffer_DenyRetainedDuringAllowFlood(t *testing.T) {
	b := newDecisionBufferForTest(64, 200)
	denyEmitted := 0
	for i := 0; i < 10000; i++ {
		b.Push(Decision{
			Allow:    true,
			Source:   SourceNetworkProxy,
			Host:     "host.example",
			Port:     443,
			Protocol: ProtocolHTTPSConnect,
		})
		// Interleave a deny every 1000 allows for 10 total denies.
		if i > 0 && i%1000 == 0 {
			b.Push(Decision{
				Allow:    false,
				Source:   SourceNetworkProxy,
				Reason:   ReasonNotInAllowlist,
				Host:     "blocked.example",
				Port:     443,
				Protocol: ProtocolHTTPSConnect,
			})
			denyEmitted++
		}
	}
	if denyEmitted != 9 {
		// Note: i%1000 hits at i=1000,2000,...,9000 → 9 denies.
		t.Fatalf("test setup: expected 9 denies emitted, got %d", denyEmitted)
	}
	// Push one more deny so we land on 10.
	b.Push(Decision{
		Allow:    false,
		Source:   SourceNetworkProxy,
		Reason:   ReasonNotInAllowlist,
		Host:     "blocked.example",
		Port:     443,
		Protocol: ProtocolHTTPSConnect,
	})

	deny, allow, denyDrops, _ := b.Drain()
	if len(deny) != 10 {
		t.Errorf("len(deny) = %d, want 10 (deny isolation broken)", len(deny))
	}
	if denyDrops != 0 {
		t.Errorf("denyDrops = %d, want 0 (allow flood pressured deny buffer)", denyDrops)
	}
	if len(allow) > 200 {
		t.Errorf("len(allow) = %d, want <= 200", len(allow))
	}
}

// TestDecisionBuffer_AllowDropCountAccurate pins the allow-side drop
// counter accuracy under a small custom cap.
func TestDecisionBuffer_AllowDropCountAccurate(t *testing.T) {
	b := newDecisionBufferForTest(64, 10)
	for i := 0; i < 25; i++ {
		b.Push(Decision{
			Allow:    true,
			Source:   SourceNetworkProxy,
			Host:     "host.example",
			Port:     443,
			Protocol: ProtocolHTTPSConnect,
		})
	}
	_, allow, _, allowDrops := b.Drain()
	if len(allow) != 10 {
		t.Errorf("len(allow) = %d, want 10", len(allow))
	}
	if allowDrops != 15 {
		t.Errorf("allowDrops = %d, want 15", allowDrops)
	}
}

// TestDecisionBuffer_DrainPreservesHostFieldsVerbatim pins the buffer's
// pure-storage semantics: Decision values pushed into the buffer come
// out via Drain unchanged. The buffer itself does NOT sanitize Host or
// any other field; sanitization is the projection layer's job
// (collectProxyDecisions / projectAuditRecordsFromAllowOnly).
//
// Replaces the v42-1b critic Finding 7 tautology where the test asserted
// Decision struct shape rather than buffer behavior.
func TestDecisionBuffer_DrainPreservesHostFieldsVerbatim(t *testing.T) {
	hosts := []string{
		"example.com",
		"api.test",
		"sub.domain.example",
		"127.0.0.1",
		"[::1]",
	}
	b := newDecisionBufferForTest(64, 200)
	for _, h := range hosts {
		b.Push(Decision{
			Allow:    true,
			Source:   SourceNetworkProxy,
			Host:     h,
			Port:     443,
			Protocol: ProtocolHTTPSConnect,
		})
	}
	_, allow, _, _ := b.Drain()
	if len(allow) != len(hosts) {
		t.Fatalf("len(allow) = %d, want %d", len(allow), len(hosts))
	}
	for i, h := range hosts {
		if allow[i].Host != h {
			t.Errorf("allow[%d].Host = %q, want %q (buffer must not transform Host)", i, allow[i].Host, h)
		}
	}
}

// TestDecisionBuffer_DrainResetsState pins the per-Run isolation
// contract: pushing N events, draining, then pushing M more events and
// draining again must return only the M post-drain events. Drop
// counters reset to 0 too.
func TestDecisionBuffer_DrainResetsState(t *testing.T) {
	b := newDecisionBufferForTest(64, 200)
	const n = 50
	for i := 0; i < n; i++ {
		b.Push(Decision{
			Allow:    true,
			Source:   SourceNetworkProxy,
			Host:     "host-1",
			Port:     443,
			Protocol: ProtocolHTTPSConnect,
		})
	}
	deny1, allow1, denyDrops1, allowDrops1 := b.Drain()
	if len(allow1) != n {
		t.Fatalf("first drain: len(allow) = %d, want %d", len(allow1), n)
	}
	if len(deny1) != 0 || denyDrops1 != 0 || allowDrops1 != 0 {
		t.Errorf("first drain: unexpected non-zero state deny=%d denyDrops=%d allowDrops=%d",
			len(deny1), denyDrops1, allowDrops1)
	}

	const m = 30
	for i := 0; i < m; i++ {
		b.Push(Decision{
			Allow:    true,
			Source:   SourceNetworkProxy,
			Host:     "host-2",
			Port:     443,
			Protocol: ProtocolHTTPSConnect,
		})
	}
	deny2, allow2, denyDrops2, allowDrops2 := b.Drain()
	if len(allow2) != m {
		t.Errorf("second drain: len(allow) = %d, want %d (state not reset)", len(allow2), m)
	}
	if len(deny2) != 0 || denyDrops2 != 0 || allowDrops2 != 0 {
		t.Errorf("second drain: drop counters not reset deny=%d denyDrops=%d allowDrops=%d",
			len(deny2), denyDrops2, allowDrops2)
	}
	if len(allow2) > 0 && allow2[0].Host != "host-2" {
		t.Errorf("second drain returned post-drain events as %q, want host-2", allow2[0].Host)
	}
}

// TestDecisionBuffer_CapZeroDropsAllAndCountsAccurately pins the
// "disabled but counted" semantics for both buffers when caps are
// zero: every Push increments the corresponding drop counter and no
// Decision is retained.
func TestDecisionBuffer_CapZeroDropsAllAndCountsAccurately(t *testing.T) {
	b := newDecisionBufferForTest(0, 0)
	for i := 0; i < 50; i++ {
		b.Push(Decision{
			Allow:    true,
			Source:   SourceNetworkProxy,
			Host:     "host.example",
			Port:     443,
			Protocol: ProtocolHTTPSConnect,
		})
		b.Push(Decision{
			Allow:    false,
			Source:   SourceNetworkProxy,
			Reason:   ReasonNotInAllowlist,
			Host:     "blocked.example",
			Port:     443,
			Protocol: ProtocolHTTPSConnect,
		})
	}
	deny, allow, denyDrops, allowDrops := b.Drain()
	if len(deny) != 0 {
		t.Errorf("len(deny) = %d, want 0", len(deny))
	}
	if len(allow) != 0 {
		t.Errorf("len(allow) = %d, want 0", len(allow))
	}
	if denyDrops != 50 {
		t.Errorf("denyDrops = %d, want 50", denyDrops)
	}
	if allowDrops != 50 {
		t.Errorf("allowDrops = %d, want 50", allowDrops)
	}
}

// TestDecisionBuffer_PushDrainConcurrentNoRace pins the concurrency
// contract via the race detector: a push goroutine and a drain
// goroutine can interleave without data races. -race flag (set by
// `make test`) is the actual verifier; the test passes by completing
// without a race report.
func TestDecisionBuffer_PushDrainConcurrentNoRace(t *testing.T) {
	b := newDecisionBuffer()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			d, err := NewAllow(SourceNetworkProxy, "example.com", 443, ProtocolHTTPSConnect)
			if err != nil {
				t.Errorf("NewAllow: %v", err)
				return
			}
			b.Push(d)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_, _, _, _ = b.Drain()
		}
	}()
	wg.Wait()
}

// TestProxySurfacesParity_MacOSLinux pins the platform-agnostic helper
// parity: projectAuditRecordsFromAllowOnly produces byte-identical
// output regardless of which substrate runner composes the buffer.
// Substrate runners themselves are platform-gated, but the shared
// helpers (boundedDecisionBuffer + projectAuditRecordsFromAllowOnly)
// MUST behave identically. This test exercises the SHARED helpers only.
func TestProxySurfacesParity_MacOSLinux(t *testing.T) {
	decisions := []Decision{
		{Allow: true, Source: SourceNetworkProxy, Host: "github.com", Port: 443, Protocol: ProtocolHTTPSConnect},
		{Allow: true, Source: SourceNetworkProxy, Host: "api.example.com", Port: 443, Protocol: ProtocolHTTPSConnect},
	}
	b := newDecisionBufferForTest(64, 200)
	for _, d := range decisions {
		b.Push(d)
	}
	_, allow, _, _ := b.Drain()

	got := projectAuditRecordsFromAllowOnly(allow)
	want := []SandboxAuditRecord{
		{Host: "github.com", Port: 443, Protocol: string(ProtocolHTTPSConnect), Source: string(SourceNetworkProxy), Decision: "allow"},
		{Host: "api.example.com", Port: 443, Protocol: string(ProtocolHTTPSConnect), Source: string(SourceNetworkProxy), Decision: "allow"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("projectAuditRecordsFromAllowOnly output diverges from expected\ngot:  %+v\nwant: %+v", got, want)
	}
}
