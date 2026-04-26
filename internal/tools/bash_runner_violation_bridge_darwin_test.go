//go:build darwin

package tools

// detectSeatbeltViolationsForTest exposes the darwin-only
// detectSeatbeltViolations heuristic to cross-platform tests; on
// non-darwin builds the bridge returns nil and the test skips.
func detectSeatbeltViolationsForTest(res BashRunResult) []SandboxViolation {
	return detectSeatbeltViolations(res)
}

// seedSeatbeltProxyDecisionsForTest seeds the SeatbeltRunner's
// per-Run Decision buffer for cross-platform parity tests. Used by
// the audit-projection concurrent isolation test that needs to drive
// collectProxyDecisions without spinning up a real netproxy child.
func seedSeatbeltProxyDecisionsForTest(r *SeatbeltRunner, decisions []Decision) {
	r.proxyDecisionsMu.Lock()
	defer r.proxyDecisionsMu.Unlock()
	r.proxyDecisions = append(r.proxyDecisions, decisions...)
}

// collectSeatbeltProxyDecisionsForTest exposes the darwin-only
// collectProxyDecisions method so cross-platform parity assertions
// can drive the projection without exercising the platform-specific
// drain goroutine.
func collectSeatbeltProxyDecisionsForTest(r *SeatbeltRunner) ([]SandboxViolation, []SandboxAuditRecord, int) {
	return r.collectProxyDecisions()
}

// newSeatbeltRunnerForAuditTest constructs a minimal SeatbeltRunner
// usable as the holder for proxyDecisionsMu + proxyDecisions during
// the cross-platform parity assertions. The test does not call Run
// (which would require sandbox-exec + a netproxy child), only the
// projection helper.
func newSeatbeltRunnerForAuditTest() *SeatbeltRunner {
	return &SeatbeltRunner{}
}
