//go:build darwin

package tools

// detectSeatbeltViolationsForTest exposes the darwin-only
// detectSeatbeltViolations heuristic to cross-platform tests; on
// non-darwin builds the bridge returns nil and the test skips.
func detectSeatbeltViolationsForTest(res BashRunResult) []SandboxViolation {
	return detectSeatbeltViolations(res)
}

// seedOption mutates a *boundedDecisionBuffer before seedXxxForTest
// pushes Decisions into it. The functional-option shape lets tests
// inject custom caps (e.g. cap=0 for "disabled but counted" scenarios)
// without expanding the seed helper signature for callers that only
// need the production caps.
type seedOption func(*boundedDecisionBuffer)

// withDecisionCaps overrides the buffer's caps before seed pushes.
// Must be applied via seedSeatbeltProxyDecisionsForTest opts.
func withDecisionCaps(deny, allow int) seedOption {
	return func(b *boundedDecisionBuffer) {
		b.denyCap = deny
		b.allowCap = allow
	}
}

// seedSeatbeltProxyDecisionsForTest seeds the SeatbeltRunner's
// per-Run Decision buffer for cross-platform parity tests. Used by
// the audit-projection concurrent isolation test that needs to drive
// collectProxyDecisions without spinning up a real netproxy child.
//
// Panics rather than lazy-init when r.decisionBuf is nil so tests
// that forget the constructor surface a loud failure instead of
// silently installing default caps that would mask cap-related test
// bugs. v42-2 critic Finding 5 — explicit panic over silent default.
func seedSeatbeltProxyDecisionsForTest(r *SeatbeltRunner, decisions []Decision, opts ...seedOption) {
	if r.decisionBuf == nil {
		panic("seedSeatbeltProxyDecisionsForTest: r.decisionBuf is nil; use newSeatbeltRunnerForAuditTest() to construct runner")
	}
	for _, opt := range opts {
		opt(r.decisionBuf)
	}
	for _, d := range decisions {
		r.decisionBuf.Push(d)
	}
}

// collectSeatbeltProxyDecisionsForTest exposes the darwin-only
// collectProxyDecisions method so cross-platform parity assertions
// can drive the projection without exercising the platform-specific
// drain goroutine. Returns the legacy 3-tuple shape (violations,
// audit, audit-drop) for byte-identical compatibility with v42-1b
// tests; the new v42-2 deny-drop tuple is read by callers that invoke
// r.collectProxyDecisions() directly.
func collectSeatbeltProxyDecisionsForTest(r *SeatbeltRunner) ([]SandboxViolation, []SandboxAuditRecord, int) {
	surfaces, _ := r.collectProxyDecisions()
	return surfaces.Violations, surfaces.Permitted, surfaces.PermittedDropCount
}

// newSeatbeltRunnerForAuditTest constructs a minimal SeatbeltRunner
// usable as the holder for the per-Run decision buffer during
// cross-platform parity assertions. The test does not call Run
// (which would require sandbox-exec + a netproxy child), only the
// projection helper. v42-2: pre-initializes decisionBuf so seed
// helpers don't trip the nil-panic guard.
func newSeatbeltRunnerForAuditTest() *SeatbeltRunner {
	return &SeatbeltRunner{
		decisionBuf: newDecisionBuffer(),
	}
}
