//go:build linux

package tools

// detectBwrapViolationsForTest exposes the linux-only
// detectBwrapViolations heuristic to cross-platform tests; on
// non-linux builds the bridge returns nil and the test skips.
func detectBwrapViolationsForTest(res BashRunResult) []SandboxViolation {
	return detectBwrapViolations(res)
}

// seedOption mutates a *boundedDecisionBuffer before seedXxxForTest
// pushes Decisions into it. The functional-option shape lets tests
// inject custom caps (e.g. cap=0 for "disabled but counted" scenarios)
// without expanding the seed helper signature for callers that only
// need the production caps.
type seedOption func(*boundedDecisionBuffer)

// withDecisionCaps overrides the buffer's caps before seed pushes.
// Must be applied via seedBwrapProxyDecisionsForTest opts.
func withDecisionCaps(deny, allow int) seedOption {
	return func(b *boundedDecisionBuffer) {
		b.denyCap = deny
		b.allowCap = allow
	}
}

// seedBwrapProxyDecisionsForTest seeds the BwrapRunner's per-Run
// Decision buffer for cross-platform parity tests. Used by the
// audit-projection concurrent isolation test that needs to drive
// collectProxyDecisions without spinning up a real netproxy child.
//
// Panics rather than lazy-init when r.decisionBuf is nil so tests
// that forget the constructor surface a loud failure instead of
// silently installing default caps that would mask cap-related test
// bugs. v42-2 critic Finding 5 — explicit panic over silent default.
func seedBwrapProxyDecisionsForTest(r *BwrapRunner, decisions []Decision, opts ...seedOption) {
	if r.decisionBuf == nil {
		panic("seedBwrapProxyDecisionsForTest: r.decisionBuf is nil; use newBwrapRunnerForAuditTest() to construct runner")
	}
	for _, opt := range opts {
		opt(r.decisionBuf)
	}
	for _, d := range decisions {
		r.decisionBuf.Push(d)
	}
}

// collectBwrapProxyDecisionsForTest exposes the linux-only
// collectProxyDecisions method so cross-platform parity assertions
// can drive the projection without exercising the platform-specific
// drain goroutine. Returns the legacy 3-tuple shape (violations,
// audit, audit-drop) for byte-identical compatibility with v42-1b
// tests; the new v42-2 deny-drop tuple is read by callers that invoke
// r.collectProxyDecisions() directly.
func collectBwrapProxyDecisionsForTest(r *BwrapRunner) ([]SandboxViolation, []SandboxAuditRecord, int) {
	surfaces, _ := r.collectProxyDecisions()
	return surfaces.Violations, surfaces.Permitted, surfaces.PermittedDropCount
}

// newBwrapRunnerForAuditTest constructs a minimal BwrapRunner usable
// as the holder for the per-Run decision buffer during cross-platform
// parity assertions. The test does not call Run (which would require
// bwrap + a netproxy child), only the projection helper. v42-2:
// pre-initializes decisionBuf so seed helpers don't trip the
// nil-panic guard.
func newBwrapRunnerForAuditTest() *BwrapRunner {
	return &BwrapRunner{
		decisionBuf: newDecisionBuffer(),
	}
}
