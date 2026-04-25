//go:build linux

package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// BwrapRunner is the Linux-specific BashRunner backend that wraps each
// command in bubblewrap (bwrap) with user-, network-, and pid-namespace
// isolation. Filesystem writes are confined to the session workspace
// (--bind), the host filesystem is mounted read-only (--ro-bind / /), and
// outbound network is blocked at the kernel level via --unshare-net,
// which creates a fresh network namespace containing only the loopback
// device.
//
// Per the v41 partner verdict B3b-3 ships default-deny network only;
// userspace IP/domain allowlist support requires the B3b-4 network
// proxy substrate. SandboxConfig entries with a non-empty
// NetworkAllowlist for "bwrap" mode are rejected at the factory.
type BwrapRunner struct {
	killGrace   time.Duration
	binaryPath  string
	probeResult BashRunnerProbe
}

// NewBwrapRunner constructs a BwrapRunner and runs the substrate probe
// once at construction. Probe results are cached; the factory inspects
// the cached result and refuses to return an unavailable runner so
// callers cannot accidentally fall through to DirectRunner.
func NewBwrapRunner() *BwrapRunner {
	r := &BwrapRunner{
		killGrace:  bashKillGrace,
		binaryPath: "/usr/bin/bwrap",
	}
	r.probeResult = r.runProbe()
	return r
}

func (r *BwrapRunner) Name() string { return "bwrap" }

// Close is a no-op: bwrap spawns a fresh namespace per Run that the
// kernel reaps when bash exits. There is no runner-lifetime resource
// to tear down.
func (r *BwrapRunner) Close(_ context.Context) error { return nil }

// Probe returns the cached substrate probe result. The probe runs once
// at NewBwrapRunner; per-command probing would add subprocess overhead
// without changing the answer between invocations.
func (r *BwrapRunner) Probe(_ context.Context) BashRunnerProbe {
	return r.probeResult
}

// runProbe verifies that:
//
//  1. the runtime platform is linux,
//  2. a usable bwrap binary is available either at /usr/bin/bwrap or
//     elsewhere on PATH, and
//  3. user namespaces are functional in this environment by running a
//     trivial `bwrap --unshare-user --unshare-net --ro-bind / / /bin/true`
//     command and verifying it exits cleanly.
//
// The third check is critical: hardened containers, older kernels, and
// some sysctl-restricted hosts ship a working bwrap binary but fail at
// unshare(2). Reporting Available=true in those environments would let
// the factory hand back a runner that errors on every Run.
func (r *BwrapRunner) runProbe() BashRunnerProbe {
	base := BashRunnerProbe{
		Name:          r.Name(),
		Platform:      runtime.GOOS,
		ExecutionMode: "linux_bwrap",
		PolicyName:    "bwrap",
	}
	if runtime.GOOS != "linux" {
		base.Available = false
		base.Message = "linux_bwrap requires linux"
		return base
	}

	binPath := r.binaryPath
	if _, err := os.Stat(binPath); err != nil {
		resolved, lookErr := exec.LookPath("bwrap")
		if lookErr != nil {
			base.Available = false
			base.Message = "bwrap binary not found at /usr/bin/bwrap or on PATH"
			return base
		}
		binPath = resolved
	}
	r.binaryPath = binPath

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath,
		"--unshare-user",
		"--unshare-net",
		"--ro-bind", "/", "/",
		"/bin/true",
	)
	if err := cmd.Run(); err != nil {
		base.Available = false
		base.Message = fmt.Sprintf("bwrap user-namespace probe failed: %v (kernel.unprivileged_userns_clone or similar restriction?)", err)
		return base
	}

	base.Available = true
	base.FilesystemEnforced = true
	base.NetworkEnforced = true
	base.SandboxEnforced = true
	base.Message = "bwrap available with user namespaces; default-deny network and session-confined writes"
	return base
}

// Run wraps the command in bwrap and delegates the lifecycle to the
// shared runBashCmd helper. The bwrap argv enforces user/net/pid
// namespace isolation, makes the host filesystem read-only, mounts
// the session workspace read-write, and dies with the parent so a
// crashed Elnath cannot leave orphaned namespaces on the host.
func (r *BwrapRunner) Run(ctx context.Context, req BashRunRequest) (BashRunResult, error) {
	if !r.probeResult.Available {
		return BashRunResult{
			Output:         fmt.Sprintf("bwrap unavailable: %s", r.probeResult.Message),
			IsError:        true,
			Classification: "sandbox_setup_failed",
			CWD:            req.DisplayCWD,
		}, nil
	}

	args := buildBwrapArgs(req, resolveBashShell())
	cmd := exec.Command(r.binaryPath, args...)
	cmd.Dir = req.WorkDir
	cmd.Env = cleanBashEnv(os.Environ(), req.SessionDir, req.WorkDir)
	configureProcessCleanup(cmd)

	res := runBashCmd(ctx, cmd, req, r.killGrace)
	res.Violations = detectBwrapViolations(res)
	return res, nil
}

// buildBwrapArgs composes the bwrap argument list. The order is:
//
//   - namespace unshares (user / pid / uts / net) — userspace
//     escape vectors closed before any binds happen
//   - --ro-bind / /  — host root mounted read-only so the agent can
//     read system binaries and config but cannot write outside the
//     workspace
//   - --bind sessionDir sessionDir — explicit read-write bind of the
//     session workspace at the same path the host sees, so any path
//     PathGuard validates also resolves correctly inside the sandbox
//   - --proc /proc, --dev /dev, --tmpfs /tmp — synthetic filesystems
//     so /proc/self, /dev/null, and /tmp work inside the namespace
//   - --die-with-parent — bwrap exits if Elnath crashes, preventing
//     orphaned sandbox processes and namespaces
//   - --chdir workDir — start bash with the validated working dir
//   - --, bash, -c, command — separator and the actual invocation
func buildBwrapArgs(req BashRunRequest, bashPath string) []string {
	return []string{
		"--unshare-user",
		"--unshare-pid",
		"--unshare-uts",
		"--unshare-net",
		"--ro-bind", "/", "/",
		"--bind", req.SessionDir, req.SessionDir,
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--die-with-parent",
		"--chdir", req.WorkDir,
		"--",
		bashPath, "-c", req.Command,
	}
}

// detectBwrapViolations is a best-effort heuristic for surfacing
// sandbox-induced failures. bwrap itself does not log per-syscall
// denials in a stable format, so we look for substrings that strongly
// suggest a sandbox-side block (network denied via ENETUNREACH inside
// --unshare-net, write denied on --ro-bind paths) and emit a single
// SandboxViolation entry. Absence of a violation does not prove the
// command was unrestricted; it just means the heuristic did not match.
func detectBwrapViolations(res BashRunResult) []SandboxViolation {
	if res.StderrRawBytes == 0 && res.StdoutRawBytes == 0 {
		return nil
	}
	body := res.Output
	switch {
	case containsAny(body, "Network is unreachable", "Operation not permitted", "Read-only file system"):
		return []SandboxViolation{{
			Kind:    "sandbox_denied",
			Message: "bwrap blocked a filesystem or network operation; see stderr",
		}}
	}
	return nil
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if len(n) == 0 {
			continue
		}
		for i := 0; i+len(n) <= len(haystack); i++ {
			if haystack[i:i+len(n)] == n {
				return true
			}
		}
	}
	return false
}
