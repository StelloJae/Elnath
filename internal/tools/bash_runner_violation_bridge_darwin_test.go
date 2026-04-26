//go:build darwin

package tools

// detectSeatbeltViolationsForTest exposes the darwin-only
// detectSeatbeltViolations heuristic to cross-platform tests; on
// non-darwin builds the bridge returns nil and the test skips.
func detectSeatbeltViolationsForTest(res BashRunResult) []SandboxViolation {
	return detectSeatbeltViolations(res)
}
