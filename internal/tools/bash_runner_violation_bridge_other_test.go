//go:build !darwin

package tools

// detectSeatbeltViolationsForTest stub on non-darwin: returns nil so
// the cross-platform tests skip cleanly. Real coverage lives in the
// darwin runtime test file.
func detectSeatbeltViolationsForTest(_ BashRunResult) []SandboxViolation {
	return nil
}
