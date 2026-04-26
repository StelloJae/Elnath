//go:build linux

package tools

// detectBwrapViolationsForTest exposes the linux-only
// detectBwrapViolations heuristic to cross-platform tests; on
// non-linux builds the bridge returns nil and the test skips.
func detectBwrapViolationsForTest(res BashRunResult) []SandboxViolation {
	return detectBwrapViolations(res)
}
