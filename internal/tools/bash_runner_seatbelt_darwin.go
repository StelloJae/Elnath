//go:build darwin

package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// seatbeltBinary is the macOS sandbox-exec path. Apple has deprecated
// sandbox-exec but has not removed it through macOS 26; the BashRunner
// abstraction lets us swap to a successor (Apple Endpoint Security or
// Containerization.framework) without caller changes.
const seatbeltBinary = "/usr/bin/sandbox-exec"

// SeatbeltRunner is the macOS-specific BashRunner backend that wraps each
// command in sandbox-exec with an SBPL profile. B3b-2 ships a
// filesystem-only profile: writes are confined to the session workspace,
// reads are broad (the sandbox cannot block reads of the host system
// without a deeper allowlist that lives in B3b-2.5+), and network is
// unrestricted. SandboxEnforced therefore stays false in the probe —
// "sandbox" is reserved for the case where filesystem AND network are
// both enforced.
type SeatbeltRunner struct {
	killGrace      time.Duration
	binaryPath     string
	profileBuilder func(req BashRunRequest) string
}

// NewSeatbeltRunner constructs a SeatbeltRunner with the standard
// sandbox-exec binary path and the default filesystem profile.
func NewSeatbeltRunner() *SeatbeltRunner {
	return &SeatbeltRunner{
		killGrace:      bashKillGrace,
		binaryPath:     seatbeltBinary,
		profileBuilder: defaultSeatbeltProfile,
	}
}

// Name returns the stable runner identifier used in telemetry slog fields.
func (r *SeatbeltRunner) Name() string { return "seatbelt" }

// Close is a no-op: per-invocation profile temp files are cleaned up
// inside Run, and the runner has no long-lived helper process to tear
// down at session teardown.
func (r *SeatbeltRunner) Close(_ context.Context) error { return nil }

// Probe reports whether sandbox-exec is available and what surface this
// substrate enforces. B3b-2 reports FilesystemEnforced=true,
// NetworkEnforced=false, SandboxEnforced=false — partial enforcement
// must not be labeled as a full sandbox.
func (r *SeatbeltRunner) Probe(_ context.Context) BashRunnerProbe {
	p := BashRunnerProbe{
		Name:               r.Name(),
		Platform:           runtime.GOOS,
		ExecutionMode:      "macos_seatbelt_fs",
		PolicyName:         "seatbelt-fs",
		FilesystemEnforced: true,
		NetworkEnforced:    false,
		SandboxEnforced:    false,
	}
	if runtime.GOOS != "darwin" {
		p.Available = false
		p.FilesystemEnforced = false
		p.Message = "macos_seatbelt requires darwin"
		return p
	}
	if _, err := os.Stat(r.binaryPath); err != nil {
		p.Available = false
		p.FilesystemEnforced = false
		p.Message = fmt.Sprintf("seatbelt binary not present at %s", r.binaryPath)
		return p
	}
	p.Available = true
	p.Message = "macos sandbox-exec available; B3b-2 filesystem-only profile (network unrestricted, B3b-2.5 pending)"
	return p
}

// Run wraps the command in sandbox-exec with a per-invocation SBPL
// profile written to a temp file. The profile is cleaned up before Run
// returns, so per-invocation cleanup stays inside the runner per the
// B3b-0 contract.
func (r *SeatbeltRunner) Run(ctx context.Context, req BashRunRequest) (BashRunResult, error) {
	profileStr := r.profileBuilder(req)

	profileFile, err := os.CreateTemp("", "elnath-seatbelt-*.sb")
	if err != nil {
		return seatbeltSetupError(req, fmt.Errorf("create profile temp: %w", err)), nil
	}
	profilePath := profileFile.Name()
	defer os.Remove(profilePath)

	if _, err := profileFile.WriteString(profileStr); err != nil {
		_ = profileFile.Close()
		return seatbeltSetupError(req, fmt.Errorf("write profile: %w", err)), nil
	}
	if err := profileFile.Close(); err != nil {
		return seatbeltSetupError(req, fmt.Errorf("close profile: %w", err)), nil
	}

	cmd := exec.Command(r.binaryPath, "-f", profilePath, resolveBashShell(), "-c", req.Command)
	cmd.Dir = req.WorkDir
	cmd.Env = cleanBashEnv(os.Environ(), req.SessionDir, req.WorkDir)
	configureProcessCleanup(cmd)

	res := runBashCmd(ctx, cmd, req, r.killGrace)
	res.Violations = detectSeatbeltViolations(res)
	return res, nil
}

// defaultSeatbeltProfile returns the SBPL profile string allowing reads
// broadly and writes only within the session workspace. B3b-2 baseline.
//
// SBPL string literals use bare quotes; session paths cannot contain "
// because PathGuard.sanitizeSessionID strips control characters from the
// session id, and req.SessionDir is always the canonical real path of
// <workDir>/sessions/<sanitized-id>.
func defaultSeatbeltProfile(req BashRunRequest) string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")
	b.WriteString("(allow process-fork)\n")
	b.WriteString("(allow process-exec)\n")
	b.WriteString("(allow signal (target self))\n")
	b.WriteString("(allow file-read*)\n")
	fmt.Fprintf(&b, "(allow file-write* (subpath %q))\n", req.SessionDir)
	b.WriteString("(allow file-write* (literal \"/dev/null\"))\n")
	b.WriteString("(allow file-write* (literal \"/dev/dtracehelper\"))\n")
	b.WriteString("(allow file-ioctl)\n")
	// B3b-2 = filesystem-only prototype. Network is intentionally
	// unrestricted; B3b-2.5 will introduce the IP:port allowlist.
	b.WriteString("(allow network*)\n")
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow ipc-posix-shm)\n")
	b.WriteString("(allow ipc-posix-sem)\n")
	return b.String()
}

// detectSeatbeltViolations is a best-effort parser for sandbox denial
// messages emitted by sandbox-exec on stderr. The format is not stable
// across macOS releases, so this is a heuristic — any signal we surface
// is better than the agent seeing a generic "permission denied" with no
// classification, but absence of a violation entry does not mean the
// command was unrestricted.
func detectSeatbeltViolations(res BashRunResult) []SandboxViolation {
	if res.StderrRawBytes == 0 {
		return nil
	}
	body := strings.ToLower(res.Output)
	if !strings.Contains(body, "deny ") && !strings.Contains(body, "operation not permitted") {
		return nil
	}
	violation := SandboxViolation{
		Kind:    "filesystem_denied",
		Message: "sandbox-exec denied a filesystem or network operation; see stderr",
	}
	return []SandboxViolation{violation}
}

func seatbeltSetupError(req BashRunRequest, err error) BashRunResult {
	return BashRunResult{
		Output:         fmt.Sprintf("seatbelt setup failed: %v", err),
		IsError:        true,
		Classification: "sandbox_setup_failed",
		CWD:            req.DisplayCWD,
	}
}
