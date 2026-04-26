//go:build linux

package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// netproxyBwrapChildReadyTimeout caps how long the runner waits for the
// host-side netproxy child to publish its bound-port preamble. The
// child binds two UDS listeners (HTTP + SOCKS) on filesystem paths so
// readiness only blocks on subprocess startup + listener bind, both
// <100ms typically. 5s gives generous slack for loaded CI machines.
const netproxyBwrapChildReadyTimeout = 5 * time.Second

// netproxyBwrapChildShutdownGrace caps how long Close waits for the
// proxy child to exit after SIGTERM before escalating to SIGKILL.
const netproxyBwrapChildShutdownGrace = 2 * time.Second

// netproxyBwrapBinaryOverrideEnv lets linux tests substitute a freshly
// compiled `elnath` binary for the runner's self-exec path. Production
// reads os.Executable() unconditionally; the override exists because
// `go test` compiles the test harness as `tools.test` and there is no
// way to ask Go for the elnath binary path at test time. Mirrors the
// darwin substrate's ELNATH_NETPROXY_BINARY_OVERRIDE pattern but uses
// a distinct name so a test that wires both substrates does not have
// to reset the env between cases.
const netproxyBwrapBinaryOverrideEnv = "ELNATH_NETPROXY_BWRAP_BINARY_OVERRIDE"

// netproxyBridgeListenHTTPInternal is the address the in-bwrap bridge
// binds for HTTP CONNECT. The port is fixed (not 0) because the env
// injection HTTP_PROXY=http://127.0.0.1:N is computed BEFORE bwrap
// starts; the bridge cannot publish its bound port back out of the
// netns. Using a fixed loopback port inside the netns is safe because
// each Run gets a fresh netns (bwrap --unshare-net) so there is no
// host-side or cross-Run port collision.
const netproxyBridgeListenHTTPInternal = "127.0.0.1:18080"

// netproxyBridgeListenSOCKSInternal is the netns-local address for the
// SOCKS5 listener. See netproxyBridgeListenHTTPInternal for the
// fixed-port rationale.
const netproxyBridgeListenSOCKSInternal = "127.0.0.1:18081"

// BwrapRunner is the Linux-specific BashRunner backend that wraps each
// command in bubblewrap (bwrap) with user-, network-, and pid-namespace
// isolation. Filesystem writes are confined to the session workspace
// (--bind), the host filesystem is mounted read-only (--ro-bind / /), and
// outbound network is blocked at the kernel level via --unshare-net,
// which creates a fresh network namespace containing only the loopback
// device.
//
// As of v41 / B3b-4-3 the runner ALSO owns an optional netproxy child
// process AND a UDS bridge directory. When the configured allowlist
// contains any domain entry or any non-loopback IP entry (i.e.
// EvaluateWithDenylist needs the proxy to enforce policy), the runner:
//
//   - creates a per-runner UDS directory containing two endpoints
//     (http.sock + socks.sock) which the netproxy child binds,
//   - self-execs the elnath binary as
//     `elnath netproxy --http-listen unix:<udsdir>/http.sock --socks-listen unix:...`
//     OUTSIDE the bwrap netns at runner construction time,
//   - waits for the child to publish its readiness preamble,
//   - on each Run, prepends `--bind <udsdir> <udsdir>` and
//     `--ro-bind <elnathBinary> <elnathBinary>` to the bwrap argv so
//     the bridge subcommand can reach the host UDS endpoints,
//   - invokes `elnath netproxy-bridge --uds-http ... --uds-socks ...
//     --listen-http 127.0.0.1:18080 --listen-socks 127.0.0.1:18081
//     --user-cmd <BashRunRequest.Command>` AS the bwrap-spawned
//     wrapper command (the bridge IS the wrapper + parent of the user
//     command, S0 productionize pattern),
//   - injects HTTP_PROXY / HTTPS_PROXY / ALL_PROXY into the bash env
//     pointing at the netns-local bridge ports.
//
// On Close, the proxy child is shut down (SIGTERM, then SIGKILL after
// netproxyBwrapChildShutdownGrace), the UDS directory is removed, and
// drain goroutines are released via shutdown channels (M-3 fix). The
// proxy child cannot be silently restarted: if it crashes mid-session,
// the next Run returns IsError=true with Classification =
// "network_proxy_failed" — partner-locked no-silent-fallback invariant.
//
// netproxy is NOT shipped as a host process for empty allowlists or
// loopback-only allowlists (the latter is rejected at the factory for
// bwrap because there is no SBPL-equivalent loopback rule on bwrap;
// empty allowlists rely on `--unshare-net` default-deny).
type BwrapRunner struct {
	killGrace   time.Duration
	binaryPath  string
	probeResult BashRunnerProbe

	// Proxy lifecycle (B3b-4-3). Populated only when the allowlist
	// requires the proxy substrate (any domain or non-loopback IP
	// entry). All zero / nil when the runner is in default-deny mode.
	proxyMu        sync.Mutex
	proxyCmd       *exec.Cmd
	proxyDoneCh    chan error
	proxyShutdown  bool
	proxyUDSDir    string
	proxyUDSHTTP   string
	proxyUDSSOCKS  string
	proxyHasHTTP   bool
	proxyHasSOCKS  bool
	proxyElnathBin string
	// proxyDead is the sticky "child has exited" flag, updated by the
	// Wait goroutine when cmd.Wait() returns. Run consults it via
	// proxyChildAlive without taking proxyMu so the hot path stays
	// lock-free.
	proxyDead atomic.Bool
	// proxyDrainShutdown signals the stdout drain goroutine to exit
	// (M-3 follow-up). The drain goroutine selects on this channel
	// alongside its read loop so Close() can release it deterministically
	// rather than relying on the child's pipe close.
	proxyDrainShutdown chan struct{}
	// proxyDrainDone is closed by the drain goroutine on exit so Close
	// can synchronously wait for the drain to finish.
	proxyDrainDone chan struct{}
}

// NewBwrapRunner constructs a BwrapRunner with no network allowlist
// (default-deny only). Equivalent to NewBwrapRunnerWithAllowlist(nil).
// Kept for callers that have not migrated to the allowlist constructor
// and for the cross-platform stub path.
func NewBwrapRunner() *BwrapRunner {
	r, _ := NewBwrapRunnerWithAllowlist(nil)
	return r
}

// NewBwrapRunnerWithAllowlist constructs a BwrapRunner. When the
// allowlist requires the proxy substrate (any domain or non-loopback
// IP entry), the runner spawns the netproxy child OUTSIDE the bwrap
// netns at construction time and provisions the UDS bridge directory.
// Spawn failures abort construction with a descriptive error so the
// factory caller can surface "sandbox unavailable" — partner-locked
// no-silent-fallback invariant.
//
// Empty allowlist falls back to the legacy default-deny construction
// (no proxy spawn) so existing factory paths continue to work.
func NewBwrapRunnerWithAllowlist(allowlist []string) (*BwrapRunner, error) {
	r := &BwrapRunner{
		killGrace:  bashKillGrace,
		binaryPath: "/usr/bin/bwrap",
	}
	r.probeResult = r.runProbe()

	if len(allowlist) == 0 {
		return r, nil
	}

	// Determine whether any entry requires the proxy substrate.
	// entryRequiresProxy is the canonical classifier — a single
	// source of truth shared with the factory.
	loopbackEntries, proxyEntries, err := splitBwrapAllowlistByProxyNeed(allowlist)
	if err != nil {
		return nil, err
	}
	if len(proxyEntries) == 0 {
		// All entries are loopback IPs. Bwrap has no SBPL-equivalent
		// loopback rule, so loopback-only allowlists are rejected by
		// the factory before this constructor is called. Defensive
		// guard: refuse construction here too rather than silently
		// degrading to default-deny.
		return nil, fmt.Errorf("bwrap loopback-only allowlist not supported (use the proxy substrate or remove the entries)")
	}

	// Refuse construction when the substrate probe failed; spawning a
	// proxy child against an unavailable substrate would yield an
	// inconsistent state.
	if !r.probeResult.Available {
		return nil, fmt.Errorf("bwrap substrate unavailable: %s", r.probeResult.Message)
	}

	if err := r.spawnProxyChild(proxyEntries, loopbackEntries); err != nil {
		// spawnProxyChild may have allocated the UDS dir; clean it up
		// so we don't leak a dir on failure.
		r.cleanupUDSDir()
		return nil, fmt.Errorf("netproxy: spawn child: %w", err)
	}
	return r, nil
}

// splitBwrapAllowlistByProxyNeed partitions the allowlist into the
// loopback entries and the proxy-required entries, mirroring the
// darwin splitAllowlistByProxyNeed signature so factory code stays
// substrate-agnostic.
func splitBwrapAllowlistByProxyNeed(allowlist []string) (loopback, proxy []string, err error) {
	for _, raw := range allowlist {
		need, perr := entryRequiresProxy(raw)
		if perr != nil {
			return nil, nil, fmt.Errorf("network allowlist entry %q invalid: %w", raw, perr)
		}
		if need {
			proxy = append(proxy, raw)
		} else {
			loopback = append(loopback, raw)
		}
	}
	return loopback, proxy, nil
}

func (r *BwrapRunner) Name() string { return "bwrap" }

// Close gracefully shuts down the proxy child (SIGTERM → wait →
// SIGKILL after netproxyBwrapChildShutdownGrace), drains the stdout
// drain goroutine via its shutdown channel (M-3), and removes the UDS
// directory. No-op when the runner is in default-deny mode. Safe to
// call multiple times.
//
// M-1: the select-on-channel runs against the live cmd.Wait
// goroutine's done channel directly — no proxyDead snapshot before
// the select — so a child that died after the snapshot but before
// the timer fires is observed instantly via the doneCh receive.
//
// M-3: closing proxyDrainShutdown unblocks the drain goroutine even
// when the child is still alive but quiet, deterministically
// releasing the goroutine for tests / daemon-mode runner recycling.
func (r *BwrapRunner) Close(_ context.Context) error {
	r.proxyMu.Lock()
	cmd := r.proxyCmd
	doneCh := r.proxyDoneCh
	already := r.proxyShutdown
	drainShutdown := r.proxyDrainShutdown
	drainDone := r.proxyDrainDone
	udsDir := r.proxyUDSDir
	r.proxyShutdown = true
	r.proxyMu.Unlock()
	if already {
		return nil
	}

	defer func() {
		// Always remove the UDS directory after the child exits so
		// we never leak the bind-target across runner constructions.
		// Capture-then-act so the deferred call runs even if the
		// child cleanup below panics.
		if udsDir != "" {
			_ = os.RemoveAll(udsDir)
		}
	}()

	if cmd == nil || cmd.Process == nil {
		// Drain goroutine is only spawned when the child is, so it
		// cannot exist without cmd.
		return nil
	}

	// Signal the drain goroutine to exit. The drain holds the stdout
	// pipe open via bufio.Scanner; closing the shutdown channel lets
	// the goroutine detect the request even if no further bytes
	// arrive on stdout. M-3 fix.
	if drainShutdown != nil {
		close(drainShutdown)
	}

	// Send SIGTERM to the proxy child's process group and wait for
	// the live doneCh. No proxyDead.Load() shortcut before the
	// select — the select naturally returns immediately on doneCh
	// if the child has already exited. M-1 fix.
	//
	// Process-group kill (rather than cmd.Process.Signal) is required
	// to reach grandchildren that the netproxy child may have spawned
	// — see configureProxyChildProcessGroup for the recursive-test-
	// binary leak this defends against.
	_ = killProxyChildTree(cmd, syscall.SIGTERM)
	timer := time.NewTimer(netproxyBwrapChildShutdownGrace)
	defer timer.Stop()
	select {
	case <-doneCh:
	case <-timer.C:
		_ = killProxyChildTree(cmd, syscall.SIGKILL)
		<-doneCh
	}

	// Wait for the drain goroutine to exit so callers can rely on
	// "Close returned" implying "no leftover goroutines from this
	// runner". Bounded by the next pipe-close from the reaped child;
	// drainShutdown above ensures this never blocks indefinitely.
	if drainDone != nil {
		<-drainDone
	}

	return nil
}

// cleanupUDSDir removes the UDS directory if one was provisioned.
// Used by NewBwrapRunnerWithAllowlist when spawnProxyChild fails part
// way through (Close is not called for partially-constructed runners).
func (r *BwrapRunner) cleanupUDSDir() {
	r.proxyMu.Lock()
	dir := r.proxyUDSDir
	r.proxyMu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

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
		base.Message = fmt.Sprintf("bwrap user-namespace probe failed: %v (check kernel.apparmor_restrict_unprivileged_userns, kernel.unprivileged_userns_clone, or install an AppArmor profile that allows /usr/bin/bwrap to use userns)", err)
		return base
	}

	base.Available = true
	base.FilesystemEnforced = true
	base.NetworkEnforced = true
	base.SandboxEnforced = true
	base.Message = "bwrap available with user namespaces; default-deny network and session-confined writes"
	return base
}

// resolveBwrapNetproxyBinary returns the path the runner should
// self-exec for both the netproxy child and the bridge subcommand.
// Production reads os.Executable() (the elnath binary itself);
// integration tests can override via netproxyBwrapBinaryOverrideEnv.
//
// Linux supports /proc/self/exe directly but production code uses
// os.Executable() for cross-platform consistency with the darwin
// substrate (B3b-4-2 partner pin).
func resolveBwrapNetproxyBinary() (string, error) {
	if override := os.Getenv(netproxyBwrapBinaryOverrideEnv); override != "" {
		return override, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return exe, nil
}

// spawnProxyChild creates the per-runner UDS directory, spawns the
// netproxy child outside the bwrap netns binding the two UDS
// endpoints, and waits for the readiness preamble.
//
// M-2: waitForBwrapProxyChildReady has an explicit child kill on
// timeout INSIDE the helper rather than relying on the caller to
// notice the readyErr and clean up. That makes the helper safe to
// call from any context and removes the foot-gun the darwin substrate
// follow-up flagged.
func (r *BwrapRunner) spawnProxyChild(proxyEntries, loopbackEntries []string) error {
	binary, err := resolveBwrapNetproxyBinary()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	// Per-runner UDS directory. Prefix marks the owner so a leaked
	// dir is recognizably ours during host triage.
	udsDir, err := os.MkdirTemp("", "elnath-bwrap-uds-")
	if err != nil {
		return fmt.Errorf("create uds dir: %w", err)
	}
	udsHTTP := filepath.Join(udsDir, "http.sock")
	udsSOCKS := filepath.Join(udsDir, "socks.sock")

	// Stash the dir before any further fallible step so cleanup paths
	// can find it.
	r.proxyMu.Lock()
	r.proxyUDSDir = udsDir
	r.proxyUDSHTTP = udsHTTP
	r.proxyUDSSOCKS = udsSOCKS
	r.proxyHasHTTP = true
	r.proxyHasSOCKS = true
	r.proxyElnathBin = binary
	r.proxyMu.Unlock()

	args := []string{
		"netproxy",
		"--http-listen", "unix:" + udsHTTP,
		"--socks-listen", "unix:" + udsSOCKS,
	}
	for _, e := range proxyEntries {
		args = append(args, "--allow", e)
	}
	for _, e := range loopbackEntries {
		args = append(args, "--allow", e)
	}

	cmd := exec.Command(binary, args...)
	cmd.Env = []string{
		// Minimal env: no host PATH leak. The netproxy subcommand
		// invokes nothing through a shell.
		"PATH=/usr/bin:/bin",
	}
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	// Place the proxy child in its own process group so kill semantics
	// reach the entire descendant tree. Critical when the resolved
	// binary turns out to be a Go test binary (no override env): test
	// binaries silently accept unknown argv and recursively re-run the
	// suite, holding the parent test process's stderr fd via inherited
	// descriptors. Without process-group kill, cmd.Process.Kill only
	// targets the immediate child PID, leaving grandchildren alive long
	// enough to trigger Go test's WaitDelay 1m hang
	// (`Test I/O incomplete 1m0s after exiting`). Production runs see
	// the same hardening: a future netproxy child that spawned its own
	// helpers would also be reaped via process-group kill.
	configureProxyChildProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	httpAddr, socksAddr, readyErr := waitForBwrapProxyChildReady(cmd, stdout)
	if readyErr != nil {
		// waitForBwrapProxyChildReady kills the child via process
		// group on the timeout branch; the non-timeout failure modes
		// (scanner error, child closed stdout before ready) leave the
		// child running. Best-effort process-group SIGKILL covers
		// both paths so a recursive grandchild tree (e.g., Go test
		// binary recursing under no-override) is fully reaped before
		// we surface the error.
		_ = killProxyChildTree(cmd, syscall.SIGKILL)
		_ = cmd.Wait()
		return fmt.Errorf("readiness preamble: %w", readyErr)
	}
	// httpAddr / socksAddr are returned as host:port-style strings even
	// for unix listeners. We do not need them for env injection
	// (HTTP_PROXY uses the netns-local bridge port), but the readiness
	// preamble proves both UDS endpoints are bound.
	_ = httpAddr
	_ = socksAddr

	// Drain remaining stdout in the background so the child does not
	// block on a full pipe. Drain selects on proxyDrainShutdown so
	// Close can release it deterministically rather than relying on
	// the child's pipe-close (M-3 fix).
	drainShutdown := make(chan struct{})
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		// Use io.CopyBuffer + a tiny channel-driven reader so the
		// goroutine can react to drainShutdown even when the child
		// is silent. We do this by running the io.Copy in a nested
		// goroutine and selecting on shutdown vs copy-complete.
		copyDone := make(chan struct{})
		go func() {
			_, _ = io.Copy(io.Discard, stdout)
			close(copyDone)
		}()
		select {
		case <-copyDone:
		case <-drainShutdown:
			// Close the read end so the inner io.Copy returns; the
			// pipe close races with the child exit which Close also
			// triggers via SIGTERM, but either way the inner copy
			// terminates and copyDone closes.
			_ = stdout.Close()
			<-copyDone
		}
	}()

	doneCh := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		// Sticky flag so any subsequent Run() observes the dead
		// state immediately without racing on the channel.
		r.proxyDead.Store(true)
		doneCh <- err
	}()

	r.proxyMu.Lock()
	r.proxyCmd = cmd
	r.proxyDoneCh = doneCh
	r.proxyDrainShutdown = drainShutdown
	r.proxyDrainDone = drainDone
	r.proxyMu.Unlock()
	return nil
}

// waitForBwrapProxyChildReady reads the bound-port preamble printed
// by RunProxyChildMain via cfg.ReadyWriter. The preamble format
// matches the darwin substrate:
//
//	httpListen=<addr>
//	socksListen=<addr>
//	ready
//
// addr is a UDS path for the bwrap substrate (e.g.
// `/tmp/elnath-bwrap-uds-XXXX/http.sock`). Returns the parsed addrs
// when both lines are present and `ready` is observed within
// netproxyBwrapChildReadyTimeout.
//
// M-2 fix: explicit child kill on timeout inside this helper rather
// than relying on caller cleanup.
func waitForBwrapProxyChildReady(cmd *exec.Cmd, stdout io.Reader) (httpAddr, socksAddr string, err error) {
	type result struct {
		httpAddr, socksAddr string
		err                 error
	}
	ch := make(chan result, 1)
	go func() {
		var (
			httpA, socksA string
			ready         bool
		)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			switch {
			case strings.HasPrefix(line, "httpListen="):
				httpA = strings.TrimPrefix(line, "httpListen=")
			case strings.HasPrefix(line, "socksListen="):
				socksA = strings.TrimPrefix(line, "socksListen=")
			case line == "ready":
				ready = true
				ch <- result{httpAddr: httpA, socksAddr: socksA}
				return
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			ch <- result{err: scanErr}
			return
		}
		if !ready {
			ch <- result{err: errors.New("child closed stdout before ready")}
		}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			return "", "", res.err
		}
		if res.httpAddr == "" || res.socksAddr == "" {
			return "", "", fmt.Errorf("incomplete preamble: http=%q socks=%q", res.httpAddr, res.socksAddr)
		}
		return res.httpAddr, res.socksAddr, nil
	case <-time.After(netproxyBwrapChildReadyTimeout):
		// M-2: explicit kill rather than relying on caller cleanup.
		// Best-effort signal then return; the caller still calls Wait
		// so the goroutine above completes when the pipe closes.
		// Use process-group kill so a child that managed to spawn
		// helpers (recursive Go test binary, future production
		// helpers) is fully reaped — without this, the immediate
		// child dies but grandchildren keep the inherited stderr fd
		// alive, triggering Go test's WaitDelay leak.
		_ = killProxyChildTree(cmd, syscall.SIGKILL)
		return "", "", fmt.Errorf("timeout waiting for ready preamble (%s)", netproxyBwrapChildReadyTimeout)
	}
}

// proxyChild returns the active proxy child cmd or nil when no proxy
// child was spawned. Test-only accessor.
func (r *BwrapRunner) proxyChild() *exec.Cmd {
	r.proxyMu.Lock()
	defer r.proxyMu.Unlock()
	return r.proxyCmd
}

// proxyUDSPaths returns the two bind-mounted UDS paths or empty
// strings when no proxy child was spawned. Test-only accessor.
func (r *BwrapRunner) proxyUDSPaths() (httpUDS, socksUDS string) {
	r.proxyMu.Lock()
	defer r.proxyMu.Unlock()
	return r.proxyUDSHTTP, r.proxyUDSSOCKS
}

// proxyChildAlive reports whether the supervised proxy child is still
// running. Returns true when no proxy child was ever spawned (the
// "no proxy needed" path is always considered ready). Implementation
// uses an atomic sticky flag flipped by the Wait goroutine so the
// hot path stays lock-free.
func (r *BwrapRunner) proxyChildAlive() bool {
	r.proxyMu.Lock()
	cmd := r.proxyCmd
	r.proxyMu.Unlock()
	if cmd == nil {
		return true
	}
	return !r.proxyDead.Load()
}

// proxyActive reports whether the runner is in proxy-required mode.
// Used by Run to decide whether to invoke the bridge wrapping path
// vs the legacy default-deny path.
func (r *BwrapRunner) proxyActive() bool {
	r.proxyMu.Lock()
	defer r.proxyMu.Unlock()
	return r.proxyCmd != nil
}

// bwrapBashEnvForRun returns the bash environment for a Run
// invocation, extending cleanBashEnv with HTTP_PROXY / HTTPS_PROXY /
// ALL_PROXY when the proxy child is active. No injection when the
// runner is in default-deny mode — DirectRunner-equivalent behavior
// is preserved.
//
// The proxy URLs point at the netns-local bridge ports
// (netproxyBridgeListenHTTPInternal / SOCKS) because the bridge runs
// INSIDE the bwrap netns; the host UDS paths are not directly
// reachable from the netns user command.
func (r *BwrapRunner) bwrapBashEnvForRun(hostEnv []string, sessionRoot, workingDir string) []string {
	env := cleanBashEnv(hostEnv, sessionRoot, workingDir)
	if !r.proxyActive() {
		return env
	}
	httpHostPort := bwrapBridgeHostPort(netproxyBridgeListenHTTPInternal)
	socksHostPort := bwrapBridgeHostPort(netproxyBridgeListenSOCKSInternal)
	httpURL := "http://" + httpHostPort
	env = append(env, "HTTP_PROXY="+httpURL)
	env = append(env, "HTTPS_PROXY="+httpURL)
	env = append(env, "http_proxy="+httpURL)
	env = append(env, "https_proxy="+httpURL)
	socksURL := "socks5h://" + socksHostPort
	env = append(env, "ALL_PROXY="+socksURL)
	env = append(env, "all_proxy="+socksURL)
	return env
}

// bwrapBridgeHostPort returns the host:port form of a netns-local
// listen address, stripping the proto prefix the netproxy core would
// otherwise emit. The bridge listens on a literal `127.0.0.1:N` so
// this is a passthrough today; helper kept so the addr constants stay
// the single source of truth.
func bwrapBridgeHostPort(listenAddr string) string {
	return listenAddr
}

// Run wraps the command in bwrap and delegates the lifecycle to the
// shared runBashCmd helper. The bwrap argv enforces user/net/pid
// namespace isolation, makes the host filesystem read-only, mounts
// the session workspace read-write, and dies with the parent so a
// crashed Elnath cannot leave orphaned namespaces on the host.
//
// When the runner is in proxy-required mode the bwrap argv invokes
// `elnath netproxy-bridge ...` AS the wrapper command and threads the
// user command through `--user-cmd`. The bridge is the bwrap-spawned
// wrapper and the parent of the user bash; this is the S0 spike
// pattern productionized.
func (r *BwrapRunner) Run(ctx context.Context, req BashRunRequest) (BashRunResult, error) {
	if !r.probeResult.Available {
		return BashRunResult{
			Output:         fmt.Sprintf("bwrap unavailable: %s", r.probeResult.Message),
			IsError:        true,
			Classification: "sandbox_setup_failed",
			CWD:            req.DisplayCWD,
		}, nil
	}

	// If a proxy child was spawned and has since died, refuse to run
	// rather than silently falling back to direct egress (which would
	// route around the allowlist that the proxy enforces). Mirrors
	// the darwin substrate's no-silent-fallback invariant.
	if r.proxyActive() && !r.proxyChildAlive() {
		return bwrapProxyDownError(req), nil
	}

	args := r.buildBwrapArgsForRun(req)
	cmd := exec.Command(r.binaryPath, args...)
	cmd.Dir = req.WorkDir
	cmd.Env = r.bwrapBashEnvForRun(os.Environ(), req.SessionDir, req.WorkDir)
	configureProcessCleanup(cmd)

	res := runBashCmd(ctx, cmd, req, r.killGrace)
	// M-1: post-Run check for the race window between the pre-bash
	// proxyChildAlive() guard above and the bash invocation finishing.
	// If the supervised proxy child died DURING the Run, the bash
	// command's network calls will have failed with bare connection
	// errors and the Classification would otherwise reflect curl's
	// exit code. Override to "network_proxy_failed" so operators see
	// the substrate cause instead of guessing from the user-facing
	// network error. Only triggers when the runner is in
	// proxy-required mode (proxyActive); default-deny runs are
	// unaffected.
	res = overrideClassificationIfProxyDied(res, r.proxyActive() && !r.proxyChildAlive())
	res.Violations = detectBwrapViolations(res)
	res.Output = appendViolationsSection(res.Output, res.Violations)
	return res, nil
}

// buildBwrapArgsForRun composes the bwrap argv for a single Run. When
// the proxy is inactive this is the legacy `bash -c req.Command`
// invocation. When the proxy is active the wrapper is the
// `elnath netproxy-bridge` subcommand and the user command is
// threaded through `--user-cmd` so the bridge owns its lifecycle.
func (r *BwrapRunner) buildBwrapArgsForRun(req BashRunRequest) []string {
	if !r.proxyActive() {
		return buildBwrapArgs(req, resolveBashShell())
	}
	r.proxyMu.Lock()
	udsDir := r.proxyUDSDir
	udsHTTP := r.proxyUDSHTTP
	udsSOCKS := r.proxyUDSSOCKS
	elnathBin := r.proxyElnathBin
	r.proxyMu.Unlock()
	return buildBwrapArgsWithBridge(req, elnathBin, udsDir, udsHTTP, udsSOCKS)
}

// bwrapHostReadBinds names the only host paths exposed read-only inside
// the sandbox. The shell, libc, dynamic linker, and standard coreutils
// live under /bin, /sbin, /usr, /lib, /lib64, /lib32, /libx32 — those
// directories are bound whole. /etc is NOT bound whole because that
// would expose /etc/ssh, /etc/sudoers, /etc/shadow, package configs,
// and any distro-specific secret-bearing files; instead, only the
// individual /etc entries libc / nss / the dynamic linker need to
// resolve uid → name, hostname, time zone, and ldconfig are bound by
// path. Anything else in /etc — and host HOME, /root, /Users, /var,
// /opt, /srv, /mnt — stays invisible inside the sandbox.
//
// --ro-bind-try silently skips entries that do not exist on a given
// host (e.g. /lib32 on x86_64-only systems, /etc/ld.so.conf.d on a
// distro without it) so the runner stays portable across distros
// without per-distro forks.
var bwrapHostReadBinds = []string{
	// Runtime trees: the shell, libc, dynamic linker, coreutils.
	"/bin",
	"/sbin",
	"/usr",
	"/lib",
	"/lib64",
	"/lib32",
	"/libx32",
	// /etc subset: ONLY the entries libc / nss / ldconfig / locale
	// need. Bare /etc is intentionally not bound — see comment
	// above. Each path passes through --ro-bind-try so distros that
	// place a file elsewhere (or omit it entirely) do not break the
	// runner.
	"/etc/passwd",
	"/etc/group",
	"/etc/hosts",
	"/etc/nsswitch.conf",
	"/etc/resolv.conf",
	"/etc/ld.so.cache",
	"/etc/ld.so.conf",
	"/etc/ld.so.conf.d",
	"/etc/localtime",
}

// buildBwrapArgs composes the bwrap argument list. The order is:
//
//   - namespace unshares (user / pid / uts / net) and --die-with-parent
//     — userspace escape vectors closed and lifecycle pinned before
//     any mounts happen
//   - synthetic filesystems (--proc /proc, --dev /dev, --tmpfs /tmp)
//     so /proc/self, /dev/null, and /tmp work inside the namespace
//     without inheriting host /tmp contents
//   - read-only host binds for the runtime paths in bwrapHostReadBinds
//     — host HOME, /root, /Users, /var, /opt, /srv, /mnt are NOT
//     bound, so cat ~/.ssh/id_rsa or any /home/<user>/* path inside
//     the sandbox returns "No such file or directory" rather than
//     leaking host content
//   - --bind sessionDir sessionDir — read-write bind of the session
//     workspace at the same host path so any path PathGuard already
//     validated resolves identically inside the sandbox; bwrap creates
//     intermediate parent directories as empty mount points so
//     surrounding host paths remain invisible
//   - --chdir workDir — start bash with the validated working dir
//   - --, bash, -c, command — separator and the actual invocation
func buildBwrapArgs(req BashRunRequest, bashPath string) []string {
	args := []string{
		"--unshare-user",
		"--unshare-pid",
		"--unshare-uts",
		"--unshare-net",
		"--die-with-parent",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	}
	for _, p := range bwrapHostReadBinds {
		args = append(args, "--ro-bind-try", p, p)
	}
	args = append(args,
		"--bind", req.SessionDir, req.SessionDir,
		"--chdir", req.WorkDir,
		"--",
		bashPath, "-c", req.Command,
	)
	return args
}

// buildBwrapArgsWithBridge composes the bwrap argument list for a
// proxy-required Run. Differences from buildBwrapArgs:
//
//   - --bind <udsDir> <udsDir>: the host UDS dir holding http.sock +
//     socks.sock is exposed read-write inside the sandbox so the
//     bridge can dial both UDS endpoints.
//   - --ro-bind <elnathBin> <elnathBin>: the elnath binary is exposed
//     read-only so the bridge subcommand can self-exec at the same
//     absolute path.
//   - the wrapper command is `elnath netproxy-bridge ...` rather than
//     `bash -c <userCmd>`. The user command is threaded into the
//     bridge as `--user-cmd <userCmd>` so the bridge IS the parent
//     of the user bash (S0 productionize pattern).
func buildBwrapArgsWithBridge(req BashRunRequest, elnathBin, udsDir, udsHTTP, udsSOCKS string) []string {
	args := []string{
		"--unshare-user",
		"--unshare-pid",
		"--unshare-uts",
		"--unshare-net",
		"--die-with-parent",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	}
	for _, p := range bwrapHostReadBinds {
		args = append(args, "--ro-bind-try", p, p)
	}
	args = append(args,
		"--bind", req.SessionDir, req.SessionDir,
		"--bind", udsDir, udsDir,
		"--ro-bind", elnathBin, elnathBin,
		"--chdir", req.WorkDir,
		"--",
		elnathBin, "netproxy-bridge",
		"--uds-http", udsHTTP,
		"--uds-socks", udsSOCKS,
		"--listen-http", netproxyBridgeListenHTTPInternal,
		"--listen-socks", netproxyBridgeListenSOCKSInternal,
		"--user-cmd", req.Command,
	)
	return args
}

// bwrapProxyDownError signals that the supervised proxy child has
// died mid-session. The runner refuses to execute the command rather
// than silently fall back to direct egress. Classification is the
// partner-locked "network_proxy_failed" string so callers can
// distinguish it from generic sandbox setup failures.
func bwrapProxyDownError(req BashRunRequest) BashRunResult {
	return BashRunResult{
		Output: "bwrap: netproxy child has exited; refusing to execute command without active proxy. " +
			"This is the partner-locked no-silent-fallback invariant — the proxy enforces the network allowlist " +
			"and a missing proxy means the sandbox cannot enforce policy. Restart Elnath to recover.",
		IsError:        true,
		Classification: "network_proxy_failed",
		CWD:            req.DisplayCWD,
	}
}

// detectBwrapViolations is a best-effort heuristic for surfacing
// sandbox-induced failures. bwrap itself does not log per-syscall
// denials in a stable format, so we look for substrings that strongly
// suggest a sandbox-side block (network denied via ENETUNREACH inside
// --unshare-net, write denied on --ro-bind paths) and emit a single
// SandboxViolation entry. Absence of a violation does not prove the
// command was unrestricted; it just means the heuristic did not match.
//
// Per B3b-4-1 the entry's Source is stamped
// "sandbox_substrate_heuristic" so output rendering and structured
// telemetry can mark the entry as low-confidence inferred-from-stderr
// rather than authoritative.
func detectBwrapViolations(res BashRunResult) []SandboxViolation {
	if res.StderrRawBytes == 0 && res.StdoutRawBytes == 0 {
		return nil
	}
	body := res.Output
	switch {
	case containsAny(body, "Network is unreachable", "Operation not permitted", "Read-only file system"):
		return []SandboxViolation{{
			Kind:    "sandbox_denied",
			Source:  string(SourceSandboxSubstrateHeuristic),
			Message: "low confidence: heuristic inferred bwrap denial of filesystem or network operation; see stderr",
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

// configureProxyChildProcessGroup places the netproxy child in its own
// process group so kill-via-process-group reaches the entire descendant
// tree. Mirrors configureProcessCleanup but kept distinct so future
// netproxy-specific hardening (e.g. PR_SET_PDEATHSIG, prctl flags) can
// be added without entangling the bash-invocation pipeline.
//
// Why this matters: when the resolved netproxy binary turns out to be
// a Go test binary (no ELNATH_NETPROXY_BWRAP_BINARY_OVERRIDE set), the
// test binary silently accepts unknown argv and recursively re-runs
// the entire test suite. cmd.Process.Kill only targets the immediate
// child PID; without Setpgid the recursive grandchildren survive,
// inherit the parent test binary's stderr fd, and trigger Go test's
// `Test I/O incomplete 1m0s after exiting; exec: WaitDelay expired
// before I/O complete` runtime hang. Production paths see the same
// hardening at no cost.
func configureProxyChildProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProxyChildTree sends signal to the netproxy child's process
// group, reaching grandchildren spawned by the child. Falls back to a
// direct cmd.Process kill when Setpgid was not configured (defensive;
// configureProxyChildProcessGroup is always invoked before Start).
// ESRCH is treated as success — the group has already exited.
func killProxyChildTree(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
		err := syscall.Kill(-cmd.Process.Pid, sig)
		if err == syscall.ESRCH {
			return nil
		}
		return err
	}
	return cmd.Process.Signal(sig)
}

// overrideClassificationIfProxyDied returns the supplied result with
// Classification rewritten to "network_proxy_failed" when proxyDied
// is true. Implements reviewer M-1: a proxy crash mid-Run produces a
// bash-level network failure (curl exit 7, connection refused, etc.)
// whose Classification would otherwise reflect the bash exit code,
// hiding the true substrate-side cause from operators. The override
// preserves Output verbatim so the user-facing error text still
// names the underlying network failure; only the structured
// Classification field is rewritten.
//
// Behaviour is intentionally idempotent: calling this with proxyDied
// false returns the input unchanged, so the call site can be a
// trailing post-Run hook without conditional plumbing.
func overrideClassificationIfProxyDied(res BashRunResult, proxyDied bool) BashRunResult {
	if !proxyDied {
		return res
	}
	res.Classification = "network_proxy_failed"
	res.IsError = true
	return res
}

