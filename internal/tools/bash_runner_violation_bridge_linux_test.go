//go:build linux

package tools

// detectBwrapViolationsForTest exposes the linux-only
// detectBwrapViolations heuristic to cross-platform tests; on
// non-linux builds the bridge returns nil and the test skips.
func detectBwrapViolationsForTest(res BashRunResult) []SandboxViolation {
	return detectBwrapViolations(res)
}

// seedBwrapProxyDecisionsForTest seeds the BwrapRunner's per-Run
// Decision buffer for cross-platform parity tests. Used by the
// audit-projection concurrent isolation test that needs to drive
// collectProxyDecisions without spinning up a real netproxy child.
func seedBwrapProxyDecisionsForTest(r *BwrapRunner, decisions []Decision) {
	r.proxyDecisionsMu.Lock()
	defer r.proxyDecisionsMu.Unlock()
	r.proxyDecisions = append(r.proxyDecisions, decisions...)
}

// collectBwrapProxyDecisionsForTest exposes the linux-only
// collectProxyDecisions method so cross-platform parity assertions
// can drive the projection without exercising the platform-specific
// drain goroutine.
func collectBwrapProxyDecisionsForTest(r *BwrapRunner) ([]SandboxViolation, []SandboxAuditRecord, int) {
	return r.collectProxyDecisions()
}

// newBwrapRunnerForAuditTest constructs a minimal BwrapRunner usable
// as the holder for proxyDecisionsMu + proxyDecisions during the
// cross-platform parity assertions. The test does not call Run (which
// would require bwrap + a netproxy child), only the projection helper.
func newBwrapRunnerForAuditTest() *BwrapRunner {
	return &BwrapRunner{}
}
