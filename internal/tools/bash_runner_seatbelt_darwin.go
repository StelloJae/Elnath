//go:build darwin

package tools

import (
	"context"
	"fmt"
	"net"
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

// SeatbeltRunner is the macOS-specific BashRunner backend that wraps
// each command in sandbox-exec with an SBPL profile. After B3b-2.5 the
// profile enforces both filesystem and network policy:
//
//   - writes are confined to the session workspace
//   - reads remain broad (host filesystem read denial is deferred to a
//     future allowRead/denyRead config lane)
//   - outbound network defaults to deny; only explicit IP:port entries
//     in NetworkAllowlist are permitted
//
// SandboxEnforced becomes true when the runner is constructed on darwin
// because both axes (filesystem + network) are now actively enforced.
// Domain-based allowlists are intentionally rejected at construction —
// domain proxying is a B3b-4 substrate, not a B3b-2.5 surface.
type SeatbeltRunner struct {
	killGrace        time.Duration
	binaryPath       string
	profileBuilder   func(req BashRunRequest) string
	networkAllowlist []string
}

// NewSeatbeltRunner constructs a SeatbeltRunner with default-deny
// network (no allowlist entries). Equivalent to
// NewSeatbeltRunnerWithAllowlist(nil) but kept as a convenience because
// most callers want the strictest baseline.
func NewSeatbeltRunner() *SeatbeltRunner {
	r, _ := NewSeatbeltRunnerWithAllowlist(nil)
	return r
}

// NewSeatbeltRunnerWithAllowlist constructs a SeatbeltRunner with the
// given IP:port allowlist. Each entry is validated; domain entries
// return an error rather than silently being treated as no-policy.
func NewSeatbeltRunnerWithAllowlist(allowlist []string) (*SeatbeltRunner, error) {
	cleaned, err := validateNetworkAllowlist(allowlist)
	if err != nil {
		return nil, err
	}
	captured := append([]string(nil), cleaned...)
	return &SeatbeltRunner{
		killGrace:  bashKillGrace,
		binaryPath: seatbeltBinary,
		profileBuilder: func(req BashRunRequest) string {
			return seatbeltProfile(req, captured)
		},
		networkAllowlist: captured,
	}, nil
}

// Name returns the stable runner identifier used in telemetry slog fields.
func (r *SeatbeltRunner) Name() string { return "seatbelt" }

// Close is a no-op: per-invocation profile temp files are cleaned up
// inside Run, and the runner has no long-lived helper process to tear
// down at session teardown.
func (r *SeatbeltRunner) Close(_ context.Context) error { return nil }

// Probe reports whether sandbox-exec is available and what surface this
// substrate enforces. After B3b-2.5 SeatbeltRunner enforces both
// filesystem (subpath confinement) and network (default-deny + IP:port
// allowlist), so SandboxEnforced becomes true on darwin.
func (r *SeatbeltRunner) Probe(_ context.Context) BashRunnerProbe {
	p := BashRunnerProbe{
		Name:               r.Name(),
		Platform:           runtime.GOOS,
		ExecutionMode:      "macos_seatbelt",
		PolicyName:         "seatbelt",
		FilesystemEnforced: true,
		NetworkEnforced:    true,
		SandboxEnforced:    true,
	}
	if runtime.GOOS != "darwin" {
		p.Available = false
		p.FilesystemEnforced = false
		p.NetworkEnforced = false
		p.SandboxEnforced = false
		p.Message = "macos_seatbelt requires darwin"
		return p
	}
	if _, err := os.Stat(r.binaryPath); err != nil {
		p.Available = false
		p.FilesystemEnforced = false
		p.NetworkEnforced = false
		p.SandboxEnforced = false
		p.Message = fmt.Sprintf("seatbelt binary not present at %s", r.binaryPath)
		return p
	}
	p.Available = true
	if len(r.networkAllowlist) == 0 {
		p.Message = "macos sandbox-exec available; default-deny network (no allowlist entries) and session-confined writes"
	} else {
		p.Message = fmt.Sprintf("macos sandbox-exec available; default-deny network with %d allowlist entries; session-confined writes", len(r.networkAllowlist))
	}
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

// seatbeltProfile composes the SBPL profile string for a single Run.
// The filesystem section confines writes to req.SessionDir while
// keeping reads broadly available; the network section starts from
// (deny default) and emits one (allow network-outbound (remote ip
// "host:port")) entry per allowlist member. An empty allowlist means
// no outbound network is permitted at all.
//
// SBPL string literals use bare quotes; req.SessionDir and the
// allowlist entries are constructed from canonical paths and
// validated IP:port forms respectively, so neither can carry a
// terminating " that would break the profile.
func seatbeltProfile(req BashRunRequest, networkAllowlist []string) string {
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
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow ipc-posix-shm)\n")
	b.WriteString("(allow ipc-posix-sem)\n")
	// Network: default-deny + explicit loopback:port allowlist
	// (B3b-2.5). (deny default) above already blocks network*; we
	// emit allow rules for the whitelisted ports. Seatbelt's
	// `(remote ip ...)` filter accepts only "*" or "localhost" as
	// the host portion — the validator has already restricted entries
	// to loopback IPs, so we translate each "127.0.0.1:port" or
	// "[::1]:port" into the SBPL-acceptable "localhost:port" form.
	for _, entry := range networkAllowlist {
		_, portStr, err := net.SplitHostPort(entry)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "(allow network-outbound (remote ip \"localhost:%s\"))\n", portStr)
	}
	return b.String()
}

// detectSeatbeltViolations is a best-effort parser for sandbox denial
// messages emitted by sandbox-exec on stderr. The format is not stable
// across macOS releases, so this is a heuristic — any signal we
// surface is better than the agent seeing a generic "permission
// denied" with no classification, but absence of a violation entry
// does not mean the command was unrestricted.
func detectSeatbeltViolations(res BashRunResult) []SandboxViolation {
	if res.StderrRawBytes == 0 {
		return nil
	}
	body := strings.ToLower(res.Output)
	if !strings.Contains(body, "deny ") && !strings.Contains(body, "operation not permitted") {
		return nil
	}
	violation := SandboxViolation{
		Kind:    "sandbox_denied",
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
