package tools

import (
	"strings"
	"testing"
	"time"
)

// B3b-4-1 Phase C: BASH RESULT output rendering for SandboxViolation.
//
// formatBashResult must emit a "SANDBOX VIOLATIONS:" section after
// the standard STDOUT/STDERR sections when result.Violations is
// non-empty. Section is INDEPENDENT of IsError: a successful command
// (exit 0) with violations still renders the section. Network-shaped
// violations render with Source/Host/Port/Protocol/Reason; legacy
// filesystem-style violations render with Source/Message. Cap is
// 50 entries; overflow renders a truncation summary.

func newCappedOutputForTest() *cappedOutput {
	return newCappedOutput(1024)
}

func metaForTest() bashResultMeta {
	exit := 0
	return bashResultMeta{
		Status:         "success",
		ExitCode:       &exit,
		Duration:       10 * time.Millisecond,
		CWD:            ".",
		Classification: "success",
	}
}

func TestFormatBashResult_NoViolationsNoSection(t *testing.T) {
	stdout := newCappedOutputForTest()
	_, _ = stdout.Write([]byte("ok"))
	stderr := newCappedOutputForTest()
	out := formatBashResult(metaForTest(), stdout, stderr)
	if strings.Contains(out, "SANDBOX VIOLATIONS:") {
		t.Errorf("violations section emitted with empty violations: %q", out)
	}
}

func TestFormatBashResult_FilesystemStyleViolationRenders(t *testing.T) {
	stdout := newCappedOutputForTest()
	stderr := newCappedOutputForTest()
	violations := []SandboxViolation{
		{
			Kind:    "fs_denied",
			Source:  string(SourceSandboxSubstrateHeuristic),
			Message: "low confidence: heuristic inferred sandbox-exec denial",
		},
	}
	out := formatBashResultWithViolations(metaForTest(), stdout, stderr, violations)
	if !strings.Contains(out, "SANDBOX VIOLATIONS:") {
		t.Fatalf("missing SANDBOX VIOLATIONS section: %q", out)
	}
	if !strings.Contains(out, "sandbox_substrate_heuristic") {
		t.Errorf("source not rendered: %q", out)
	}
	if !strings.Contains(out, "low confidence") {
		t.Errorf("message not rendered: %q", out)
	}
}

func TestFormatBashResult_NetworkStyleViolationRenders(t *testing.T) {
	stdout := newCappedOutputForTest()
	stderr := newCappedOutputForTest()
	violations := []SandboxViolation{
		{
			Source:   string(SourceNetworkProxy),
			Host:     "github.com",
			Port:     443,
			Protocol: string(ProtocolHTTPSConnect),
			Reason:   string(ReasonNotInAllowlist),
		},
	}
	out := formatBashResultWithViolations(metaForTest(), stdout, stderr, violations)
	if !strings.Contains(out, "SANDBOX VIOLATIONS:") {
		t.Fatalf("missing SANDBOX VIOLATIONS section: %q", out)
	}
	for _, want := range []string{
		"network_proxy",
		"github.com:443",
		"https_connect",
		"not_in_allowlist",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("network violation missing %q: %q", want, out)
		}
	}
}

func TestFormatBashResult_MixedViolationsBothRender(t *testing.T) {
	stdout := newCappedOutputForTest()
	stderr := newCappedOutputForTest()
	violations := []SandboxViolation{
		{
			Source:  string(SourceSandboxSubstrateHeuristic),
			Message: "heuristic stderr inference",
		},
		{
			Source:   string(SourceNetworkProxy),
			Host:     "1.2.3.4",
			Port:     80,
			Protocol: string(ProtocolTCP),
			Reason:   string(ReasonDeniedByRule),
		},
	}
	out := formatBashResultWithViolations(metaForTest(), stdout, stderr, violations)
	for _, want := range []string{
		"sandbox_substrate_heuristic",
		"network_proxy",
		"heuristic stderr inference",
		"1.2.3.4:80",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("mixed violations missing %q: %q", want, out)
		}
	}
}

// TestFormatBashResult_ViolationsCapAt50EntriesWithTruncation checks
// the cap. The 51st entry must not render verbatim; instead a
// truncation summary appears.
func TestFormatBashResult_ViolationsCapAt50EntriesWithTruncation(t *testing.T) {
	stdout := newCappedOutputForTest()
	stderr := newCappedOutputForTest()
	violations := make([]SandboxViolation, 0, 75)
	for i := 0; i < 75; i++ {
		violations = append(violations, SandboxViolation{
			Source:   string(SourceNetworkProxy),
			Host:     "host.example",
			Port:     uint16(1000 + i),
			Protocol: string(ProtocolHTTPSConnect),
			Reason:   string(ReasonNotInAllowlist),
		})
	}
	out := formatBashResultWithViolations(metaForTest(), stdout, stderr, violations)
	// Render must include truncation summary.
	if !strings.Contains(out, "and 25 more violations") {
		t.Errorf("truncation summary missing for 51+ entries; got: %q", out)
	}
	// Sanity: must contain the first entry's port and not the 70th
	// entry's port (which is past the cap).
	if !strings.Contains(out, ":1000") {
		t.Errorf("first entry not rendered: %q", out)
	}
	if strings.Contains(out, ":1070") {
		t.Errorf("entry past cap leaked into render: %q", out)
	}
}

// TestFormatBashResult_ViolationsRenderEvenWhenIsErrorFalse pins the
// "independent of IsError" invariant. A successful command must still
// surface its violations.
func TestFormatBashResult_ViolationsRenderEvenWhenIsErrorFalse(t *testing.T) {
	stdout := newCappedOutputForTest()
	_, _ = stdout.Write([]byte("ok"))
	stderr := newCappedOutputForTest()
	meta := metaForTest()
	violations := []SandboxViolation{
		{
			Source:   string(SourceNetworkProxy),
			Host:     "blocked.example",
			Port:     443,
			Protocol: string(ProtocolHTTPSConnect),
			Reason:   string(ReasonNotInAllowlist),
		},
	}
	out := formatBashResultWithViolations(meta, stdout, stderr, violations)
	if !strings.Contains(out, "SANDBOX VIOLATIONS:") {
		t.Errorf("violations section missing on success path: %q", out)
	}
	if !strings.Contains(out, "blocked.example:443") {
		t.Errorf("network entry missing on success path: %q", out)
	}
}
