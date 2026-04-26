//go:build !linux

package tools

// detectBwrapViolationsForTest stub on non-linux: returns nil so the
// cross-platform tests skip cleanly. Real coverage lives in the linux
// runtime test file.
func detectBwrapViolationsForTest(_ BashRunResult) []SandboxViolation {
	return nil
}
