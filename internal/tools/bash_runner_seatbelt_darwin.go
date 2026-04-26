//go:build darwin

package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// seatbeltBinary is the macOS sandbox-exec path. Apple has deprecated
// sandbox-exec but has not removed it through macOS 26; the BashRunner
// abstraction lets us swap to a successor (Apple Endpoint Security or
// Containerization.framework) without caller changes.
const seatbeltBinary = "/usr/bin/sandbox-exec"

// netproxyChildReadyTimeout caps how long the runner waits for the
// proxy child to publish its bound-port preamble. The proxy binds on
// 127.0.0.1:0 (ephemeral) so readiness only blocks on go startup +
// listener bind, both <100ms typically. 5s gives generous slack for
// loaded CI machines without hanging the runner indefinitely.
const netproxyChildReadyTimeout = 5 * time.Second

// netproxyChildShutdownGrace caps how long Close waits for the proxy
// child to exit after SIGTERM before escalating to SIGKILL. The proxy
// only needs to close listeners and reap copy goroutines, so 2s is
// generous.
const netproxyChildShutdownGrace = 2 * time.Second

// netproxyBinaryOverrideEnv lets darwin tests substitute a freshly
// compiled `elnath` binary for the runner's self-exec path. Production
// reads os.Executable() unconditionally; the override exists because
// `go test` compiles the test harness as `tools.test` and there is no
// way to ask Go for the elnath binary path at test time.
const netproxyBinaryOverrideEnv = "ELNATH_NETPROXY_BINARY_OVERRIDE"

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
//
// As of B3b-4-2 the runner ALSO owns an optional netproxy child
// process. When the configured allowlist contains any domain entry or
// any non-loopback IP entry (i.e. EvaluateWithDenylist needs the proxy
// to enforce policy), the runner self-execs the elnath binary as
// `elnath netproxy ...` at construction time, captures the bound HTTP
// CONNECT and SOCKS5 ports, and:
//
//   - emits an SBPL `(allow network-outbound (remote ip "localhost:N"))`
//     entry for each proxy port (per partner pin C3 — never `localhost:*`)
//   - injects HTTP_PROXY / HTTPS_PROXY / ALL_PROXY into the bash env
//     so user commands route through the proxy
//   - shuts down the child cleanly on Close (SIGTERM, then SIGKILL
//     after netproxyChildShutdownGrace)
//
// If the proxy child crashes mid-session the next Run returns
// IsError=true with Classification="network_proxy_failed" rather than
// silently falling back to DirectRunner — sandbox unavailable MUST be
// observable.
//
// Loopback-only and empty allowlists do NOT spawn the proxy child;
// the SBPL `(remote ip "localhost:port")` rule handles loopback ports
// directly.
type SeatbeltRunner struct {
	killGrace        time.Duration
	binaryPath       string
	profileBuilder   func(req BashRunRequest) string
	networkAllowlist []string

	// Proxy lifecycle (B3b-4-2). Populated only when the allowlist
	// requires the proxy substrate (any domain or non-loopback IP
	// entry). nil when the runner is in loopback-only or default-deny
	// mode.
	proxyMu        sync.Mutex
	proxyCmd       *exec.Cmd
	proxyHTTPPort  int
	proxySOCKSPort int
	proxyDoneCh    chan error
	proxyShutdown  bool
	// proxyDead is the sticky "child has exited" flag. Set inside
	// the Wait goroutine when cmd.Wait() returns; consulted by
	// proxyChildAlive on the Run hot path. Atomic so the Run path
	// stays lock-free and so the test that kills the child mid-session
	// can observe the dead state deterministically.
	proxyDead atomic.Bool

	// proxyDrainShutdown signals the stdout drain goroutine to exit.
	// The drain goroutine selects on this channel alongside its read
	// loop so Close() can release it deterministically rather than
	// relying on the child's pipe close. Mirrors the Linux M-3 fix —
	// see bash_runner_bwrap_linux.go:121-128 for the canonical doc.
	proxyDrainShutdown chan struct{}
	// proxyDrainDone is closed by the drain goroutine on exit so Close
	// can synchronously wait for the drain to finish. Mirrors Linux at
	// bash_runner_bwrap_linux.go:126-128.
	proxyDrainDone chan struct{}

	// proxyDecisionsMu guards proxyDecisions. Held only briefly during
	// append (drain goroutine) and snapshot+clear (Run completion). The
	// listener accept-loop path never touches this slice directly so
	// the lock cannot block the proxy's hot path. Mirrors Linux at
	// bash_runner_bwrap_linux.go:130-141.
	proxyDecisionsMu sync.Mutex
	// proxyDecisions buffers per-connection Decision events the drain
	// goroutine parses out of the netproxy child's stdout. v42-1a
	// surfaces these into BashRunResult.Violations on the next Run
	// completion via collectProxyViolations.
	proxyDecisions []Decision
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
// given allowlist. Loopback-only and empty allowlists are honored
// directly via SBPL rules; allowlists containing any domain entry or
// any non-loopback IP entry trigger the netproxy proxy child spawn at
// construction time. Spawn failures abort construction with a
// descriptive error so the factory caller can surface "sandbox
// unavailable" rather than silently falling back.
func NewSeatbeltRunnerWithAllowlist(allowlist []string) (*SeatbeltRunner, error) {
	captured := append([]string(nil), allowlist...)

	// Determine which mode: pure-loopback (no proxy needed) or
	// proxy-required. The factory layer runs entryRequiresProxy on
	// each entry too — re-running it here means construction stays
	// safe even when callers bypass the factory (e.g. direct test
	// instantiation of the runner).
	loopbackEntries, proxyEntries, err := splitAllowlistByProxyNeed(captured)
	if err != nil {
		return nil, err
	}

	r := &SeatbeltRunner{
		killGrace:        bashKillGrace,
		binaryPath:       seatbeltBinary,
		networkAllowlist: append([]string(nil), captured...),
	}

	// Loopback-only / empty path: validate via the legacy IP-only
	// validator so existing factory tests that pass IP literals
	// still receive the strict "must be loopback" error.
	if len(proxyEntries) == 0 {
		if _, err := validateNetworkAllowlist(loopbackEntries); err != nil {
			return nil, err
		}
		r.profileBuilder = func(req BashRunRequest) string {
			return seatbeltProfileWithProxyPorts(req, captured, 0, 0)
		}
		return r, nil
	}

	// Proxy-required path: spawn the child, capture ports, then
	// install the proxy-aware profile builder so the SBPL profile
	// pins both proxy listener ports.
	if err := r.spawnProxyChild(proxyEntries, loopbackEntries); err != nil {
		return nil, fmt.Errorf("netproxy: spawn child: %w", err)
	}
	httpPort := r.proxyHTTPPort
	socksPort := r.proxySOCKSPort
	r.profileBuilder = func(req BashRunRequest) string {
		return seatbeltProfileWithProxyPorts(req, loopbackEntries, httpPort, socksPort)
	}
	return r, nil
}

// splitAllowlistByProxyNeed partitions the allowlist into the loopback
// entries (handled by SBPL directly) and the proxy-required entries
// (handled by the netproxy child). entryRequiresProxy is the canonical
// classifier — a single source of truth shared with the factory.
func splitAllowlistByProxyNeed(allowlist []string) (loopback, proxy []string, err error) {
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

// spawnProxyChild self-execs the elnath binary as `elnath netproxy
// --http-listen 127.0.0.1:0 --socks-listen 127.0.0.1:0 --allow ...`.
// It blocks until the child publishes its bound ports via the stdout
// preamble, then returns. The child runs until Close terminates it.
//
// Errors at every stage are surfaced as a wrapped error so the
// factory caller can return "sandbox unavailable" rather than silently
// falling back. This is the partner-locked no-silent-fallback
// invariant.
func (r *SeatbeltRunner) spawnProxyChild(proxyEntries, loopbackEntries []string) error {
	binary, err := resolveNetproxyBinary()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	args := []string{
		"netproxy",
		"--http-listen", "127.0.0.1:0",
		"--socks-listen", "127.0.0.1:0",
	}
	for _, e := range proxyEntries {
		args = append(args, "--allow", e)
	}
	// Forward loopback-only allowlist entries to the proxy too so a
	// future allowlist mixing domain + loopback works through the
	// proxy when an upstream client picks the proxy. SBPL handles
	// loopback-direct, but the proxy must accept loopback CONNECTs as
	// well so the agent does not have to know which path applies.
	for _, e := range loopbackEntries {
		args = append(args, "--allow", e)
	}

	cmd := exec.Command(binary, args...)
	cmd.Env = []string{
		// Minimal env: no host PATH leak; the child only needs
		// PATH for resolving the bash shell, but the netproxy
		// subcommand invokes nothing through bash.
		"PATH=/usr/bin:/bin",
	}
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	httpPort, socksPort, readyErr := waitForProxyChildReady(stdout)
	if readyErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("readiness preamble: %w", readyErr)
	}

	// v42-1a: drain remaining stdout AND parse `event=<json>` lines
	// into r.proxyDecisions so the next Run completion can project
	// them into BashRunResult.Violations as Source=network_proxy.
	// Mirrors the Linux drain pattern at bash_runner_bwrap_linux.go:530-563.
	// Lines that don't carry the event prefix (future preamble lines,
	// debug noise) are silently dropped — readiness has already been
	// consumed by waitForProxyChildReady before this drain starts.
	drainShutdown := make(chan struct{})
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		scanDone := make(chan struct{})
		go func() {
			defer close(scanDone)
			scanner := bufio.NewScanner(stdout)
			// Decision JSON lines stay well under 4KiB; keep the cap
			// explicit so future growth is observable.
			scanner.Buffer(make([]byte, 0, 4*1024), 64*1024)
			for scanner.Scan() {
				line := scanner.Text()
				d, ok, err := ParseDecisionEventLine(line)
				if err != nil || !ok {
					continue
				}
				r.proxyDecisionsMu.Lock()
				r.proxyDecisions = append(r.proxyDecisions, d)
				r.proxyDecisionsMu.Unlock()
			}
		}()
		select {
		case <-scanDone:
		case <-drainShutdown:
			// Close the read end so the scanner returns; the pipe
			// close races with the child exit which Close also
			// triggers via SIGTERM, but either way the scanner
			// terminates and scanDone closes.
			_ = stdout.Close()
			<-scanDone
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
	r.proxyHTTPPort = httpPort
	r.proxySOCKSPort = socksPort
	r.proxyDoneCh = doneCh
	r.proxyDrainShutdown = drainShutdown
	r.proxyDrainDone = drainDone
	r.proxyMu.Unlock()
	return nil
}

// resolveNetproxyBinary returns the path the runner should self-exec.
// In production it returns os.Executable() (the elnath binary itself).
// Darwin tests can override via the ELNATH_NETPROXY_BINARY_OVERRIDE
// env var pointing at a freshly compiled binary.
//
// CRITICAL: must use os.Executable(), NOT /proc/self/exe — the latter
// does not exist on macOS. Linux substrate wiring (B3b-4-3) re-evaluates
// whether /proc/self/exe is preferable on that platform; this file is
// macOS-only.
func resolveNetproxyBinary() (string, error) {
	if override := os.Getenv(netproxyBinaryOverrideEnv); override != "" {
		return override, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return exe, nil
}

// waitForProxyChildReady reads the bound-port preamble printed by
// RunProxyChildMain via cfg.ReadyWriter. The preamble format is:
//
//	httpListen=127.0.0.1:NNNN
//	socksListen=127.0.0.1:NNNN
//	ready
//
// Returns the parsed port numbers when both lines are present and
// `ready` is observed within netproxyChildReadyTimeout.
func waitForProxyChildReady(stdout io.Reader) (httpPort, socksPort int, err error) {
	type result struct {
		httpPort, socksPort int
		err                 error
	}
	ch := make(chan result, 1)
	go func() {
		var (
			http, socks int
			ready       bool
		)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			switch {
			case strings.HasPrefix(line, "httpListen="):
				http = parseLoopbackPortFromAddr(strings.TrimPrefix(line, "httpListen="))
			case strings.HasPrefix(line, "socksListen="):
				socks = parseLoopbackPortFromAddr(strings.TrimPrefix(line, "socksListen="))
			case line == "ready":
				ready = true
				ch <- result{httpPort: http, socksPort: socks}
				return
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- result{err: err}
			return
		}
		if !ready {
			ch <- result{err: errors.New("child closed stdout before ready")}
		}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			return 0, 0, res.err
		}
		if res.httpPort == 0 || res.socksPort == 0 {
			return 0, 0, fmt.Errorf("incomplete preamble: http=%d socks=%d",
				res.httpPort, res.socksPort)
		}
		return res.httpPort, res.socksPort, nil
	case <-time.After(netproxyChildReadyTimeout):
		return 0, 0, fmt.Errorf("timeout waiting for ready preamble (%s)", netproxyChildReadyTimeout)
	}
}

// parseLoopbackPortFromAddr extracts the port from a `127.0.0.1:NNNN`
// or `[::1]:NNNN` style address string. Returns 0 on parse failure so
// the caller's incomplete-preamble guard fires.
func parseLoopbackPortFromAddr(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	var p int
	for _, c := range portStr {
		if c < '0' || c > '9' {
			return 0
		}
		p = p*10 + int(c-'0')
	}
	return p
}

// proxyChild returns the active proxy child cmd or nil when no proxy
// child was spawned. Test-only accessor.
func (r *SeatbeltRunner) proxyChild() *exec.Cmd {
	r.proxyMu.Lock()
	defer r.proxyMu.Unlock()
	return r.proxyCmd
}

// httpProxyPort returns the bound HTTP CONNECT proxy port, or 0 when
// no proxy child was spawned.
func (r *SeatbeltRunner) httpProxyPort() int {
	r.proxyMu.Lock()
	defer r.proxyMu.Unlock()
	return r.proxyHTTPPort
}

// socksProxyPort returns the bound SOCKS5 proxy port, or 0 when no
// proxy child was spawned.
func (r *SeatbeltRunner) socksProxyPort() int {
	r.proxyMu.Lock()
	defer r.proxyMu.Unlock()
	return r.proxySOCKSPort
}

// bashEnvForRun returns the bash environment for a Run invocation,
// extending cleanBashEnv with HTTP_PROXY / HTTPS_PROXY / ALL_PROXY
// when the proxy child is active. No injection when proxy ports are
// zero (loopback-only / empty allowlist) — DirectRunner-equivalent
// behavior is preserved.
func (r *SeatbeltRunner) bashEnvForRun(hostEnv []string, sessionRoot, workingDir string) []string {
	env := cleanBashEnv(hostEnv, sessionRoot, workingDir)
	httpPort := r.httpProxyPort()
	socksPort := r.socksProxyPort()
	if httpPort == 0 && socksPort == 0 {
		return env
	}
	if httpPort != 0 {
		httpURL := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
		env = append(env, "HTTP_PROXY="+httpURL)
		env = append(env, "HTTPS_PROXY="+httpURL)
		env = append(env, "http_proxy="+httpURL)
		env = append(env, "https_proxy="+httpURL)
	}
	if socksPort != 0 {
		socksURL := fmt.Sprintf("socks5h://127.0.0.1:%d", socksPort)
		env = append(env, "ALL_PROXY="+socksURL)
		env = append(env, "all_proxy="+socksURL)
	}
	return env
}

// proxyChildAlive reports whether the supervised proxy child is still
// running. Returns true when no proxy child was ever spawned (the
// "no proxy needed" path is always considered ready). Implementation
// uses an atomic sticky flag flipped by the Wait goroutine so the
// hot path stays lock-free.
func (r *SeatbeltRunner) proxyChildAlive() bool {
	r.proxyMu.Lock()
	cmd := r.proxyCmd
	r.proxyMu.Unlock()
	if cmd == nil {
		return true
	}
	return !r.proxyDead.Load()
}

// proxyActive reports whether the runner is in proxy-required mode.
// Used by Run completion to decide whether the proxy lifecycle is
// expected to be alive. Mirrors the Linux helper at
// bash_runner_bwrap_linux.go:688-692. Holds proxyMu so concurrent
// reads cannot race with the spawnProxyChild writer (critic Minor 3).
func (r *SeatbeltRunner) proxyActive() bool {
	r.proxyMu.Lock()
	defer r.proxyMu.Unlock()
	return r.proxyCmd != nil
}

// Name returns the stable runner identifier used in telemetry slog fields.
func (r *SeatbeltRunner) Name() string { return "seatbelt" }

// Close gracefully shuts down the proxy child (SIGTERM → wait →
// SIGKILL after netproxyChildShutdownGrace) when one was spawned,
// drains the stdout drain goroutine via its shutdown channel (M-3),
// and is safe to call multiple times.
//
// The Wait goroutine spawned by spawnProxyChild is the sole reaper;
// Close synchronizes on its done channel so the second caller never
// double-reaps. When the child has already exited (e.g. the test
// killed it) the doneCh receive is the only thing we wait for; sending
// SIGTERM to an already-reaped PID is harmless.
//
// v42-1a (critic Major 1+2): Close MUST close drainShutdown + await
// drainDone whenever cmd != nil, regardless of whether the child is
// already dead or this is a second Close call. Skipping these on the
// dead-proxy or already-shutdown branches would leak the drain
// goroutine. Mirrors Linux ordering at bash_runner_bwrap_linux.go:241-307
// which has no fast-path early-returns.
func (r *SeatbeltRunner) Close(_ context.Context) error {
	r.proxyMu.Lock()
	cmd := r.proxyCmd
	doneCh := r.proxyDoneCh
	already := r.proxyShutdown
	drainShutdown := r.proxyDrainShutdown
	drainDone := r.proxyDrainDone
	r.proxyShutdown = true
	r.proxyMu.Unlock()
	if already {
		return nil
	}

	if cmd == nil || cmd.Process == nil {
		// Drain goroutine is only spawned when the child is, so it
		// cannot exist without cmd. Nothing to release.
		return nil
	}

	// Signal the drain goroutine to exit. The drain holds the stdout
	// pipe open via bufio.Scanner; closing the shutdown channel lets
	// the goroutine detect the request even if no further bytes
	// arrive on stdout. Closed BEFORE SIGTERM so the scanner is
	// released independently of the child reap path.
	if drainShutdown != nil {
		close(drainShutdown)
	}

	// Send SIGTERM and wait for doneCh. If the child has already
	// exited, the doneCh receive returns immediately because the Wait
	// goroutine has pushed onto it; the SIGTERM signal to a reaped PID
	// is harmless.
	_ = cmd.Process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(netproxyChildShutdownGrace)
	defer timer.Stop()
	select {
	case <-doneCh:
	case <-timer.C:
		_ = cmd.Process.Signal(syscall.SIGKILL)
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
	} else if r.httpProxyPort() == 0 {
		p.Message = fmt.Sprintf("macos sandbox-exec available; default-deny network with %d allowlist entries; session-confined writes", len(r.networkAllowlist))
	} else {
		p.Message = fmt.Sprintf("macos sandbox-exec available; netproxy on 127.0.0.1:%d (HTTP) + 127.0.0.1:%d (SOCKS5); %d allowlist entries; session-confined writes",
			r.httpProxyPort(), r.socksProxyPort(), len(r.networkAllowlist))
	}
	return p
}

// Run wraps the command in sandbox-exec with a per-invocation SBPL
// profile written to a temp file. The profile is cleaned up before Run
// returns, so per-invocation cleanup stays inside the runner per the
// B3b-0 contract.
func (r *SeatbeltRunner) Run(ctx context.Context, req BashRunRequest) (BashRunResult, error) {
	// If a proxy child was spawned and has since died, refuse to run
	// rather than silently falling back to direct egress (which would
	// route around the SBPL allowlist that pins only the proxy port).
	if !r.proxyChildAlive() {
		return seatbeltProxyDownError(req), nil
	}

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
	cmd.Env = r.bashEnvForRun(os.Environ(), req.SessionDir, req.WorkDir)
	configureProcessCleanup(cmd)

	res := runBashCmd(ctx, cmd, req, r.killGrace)
	// Post-Run check for the race window between the pre-bash
	// proxyChildAlive() guard at line 523 and the bash invocation
	// finishing. If the supervised proxy child died DURING the Run,
	// the bash command's network calls will have failed with bare
	// connection errors. Override Classification to
	// "network_proxy_failed" so operators see the substrate cause.
	// Mirrors the Linux M-1 fix at bash_runner_bwrap_linux.go:777.
	res = overrideClassificationIfProxyDiedDarwin(res, r.proxyActive() && !r.proxyChildAlive())
	// v42-1a: pull authoritative network_proxy violations the drain
	// goroutine accumulated for this Run, then append the existing
	// substrate-stderr heuristic so the agent sees both surfaces.
	// network_proxy entries appear first because they are
	// authoritative; heuristic entries follow as low-confidence signal.
	// Mirrors Linux at bash_runner_bwrap_linux.go:777-793.
	netViolations := r.collectProxyViolations()
	heuristic := detectSeatbeltViolations(res)
	if len(netViolations) > 0 || len(heuristic) > 0 {
		combined := make([]SandboxViolation, 0, len(netViolations)+len(heuristic))
		combined = append(combined, netViolations...)
		combined = append(combined, heuristic...)
		res.Violations = combined
	}
	res.Output = appendViolationsSection(res.Output, res.Violations)
	return res, nil
}

// overrideClassificationIfProxyDiedDarwin returns the supplied result
// with Classification rewritten to "network_proxy_failed" when
// proxyDied is true. Mirrors the Linux helper at
// bash_runner_bwrap_linux.go:1094 — kept as a darwin-local copy so
// v42-1a does not touch the Linux file. Behavior is identical and
// idempotent: a false proxyDied returns the input unchanged.
func overrideClassificationIfProxyDiedDarwin(res BashRunResult, proxyDied bool) BashRunResult {
	if !proxyDied {
		return res
	}
	res.Classification = "network_proxy_failed"
	res.IsError = true
	return res
}

// collectProxyViolations snapshots the per-Run Decision buffer the
// drain goroutine fills, projects each deny Decision into a
// SandboxViolation with Source=network_proxy, and clears the buffer
// for the next Run. Allow Decisions are intentionally dropped — the
// SANDBOX VIOLATIONS surface is for blocked actions, not informational
// allow events. The projection sanitizes Host/Reason/Protocol newlines
// at the construction boundary (M4 closure) before they reach the
// renderer.
//
// Decision.Port is bounded to uint16 by NewAllow / NewDeny construction
// (0..65535 enforced); the explicit conversion below cannot overflow.
//
// Mirrors the Linux helper at bash_runner_bwrap_linux.go:807-836; the
// near-copy is intentional per the v42-1a Option A architect call.
func (r *SeatbeltRunner) collectProxyViolations() []SandboxViolation {
	r.proxyDecisionsMu.Lock()
	decisions := r.proxyDecisions
	r.proxyDecisions = nil
	r.proxyDecisionsMu.Unlock()
	if len(decisions) == 0 {
		return nil
	}
	out := make([]SandboxViolation, 0, len(decisions))
	for _, d := range decisions {
		if d.Allow {
			continue
		}
		if d.Source != SourceNetworkProxy {
			// Only network_proxy decisions belong in violations from
			// this lane. Other Source values are reserved for
			// substrate-direct paths that emit through detect*
			// helpers; routing them here would double-count.
			continue
		}
		out = append(out, SandboxViolation{
			Source:   string(d.Source),
			Host:     sanitizeViolationField(d.Host),
			Port:     uint16(d.Port),
			Protocol: sanitizeViolationField(string(d.Protocol)),
			Reason:   sanitizeViolationField(string(d.Reason)),
		})
	}
	return out
}

// seatbeltProfile (legacy entry) emits the SBPL profile for a Run
// without any proxy ports. Kept for callers that constructed
// SeatbeltRunner before B3b-4-2 and have no proxy concept; they still
// see the loopback-only translated rule shape.
//
// New code should prefer seatbeltProfileWithProxyPorts which honors
// per-port pinning for the netproxy child listeners.
func seatbeltProfile(req BashRunRequest, networkAllowlist []string) string {
	return seatbeltProfileWithProxyPorts(req, networkAllowlist, 0, 0)
}

// seatbeltProfileWithProxyPorts emits the SBPL profile with optional
// proxy port pinning (B3b-4-2). When httpProxyPort > 0 the SBPL emits
// `(allow network-outbound (remote ip "localhost:<httpProxyPort>"))`;
// likewise for socksProxyPort. User-provided loopback entries
// (127.0.0.1:port, [::1]:port) are translated to the SBPL-acceptable
// `localhost:port` form.
//
// Per partner pin C3 the profile NEVER emits `localhost:*` or any
// other broad wildcard. A user that wants a local service on port
// 5432 must list `127.0.0.1:5432` explicitly; the profile then emits
// only `localhost:5432`.
//
// Limitation (B3b-4-2): SBPL grammar permits `(remote ip "host:port")`
// per entry but cannot encode "this localhost:port is the proxy" vs
// "this localhost:port is a user-authorized local service". An
// explicit per-port loopback allowlist entry therefore intentionally
// permits direct sandbox egress to that exact port, even when a
// netproxy child is also bound on a different loopback port.
// Direct-egress enforcement is observed only against ports the user
// did NOT allowlist; the integration test suite uses a separate
// non-allowlisted loopback server to assert default-deny works for
// those (see TestSeatbeltProxyIntegration_DirectEgressToNonAllowlistedPortBlocked).
// The broad `localhost:*` footgun (Critic C3) is what stays
// forbidden — explicit user opt-in to a known port does not.
//
// SBPL string literals use bare quotes; req.SessionDir and the
// allowlist entries are constructed from canonical paths and validated
// IP:port forms respectively, so neither can carry a terminating " that
// would break the profile.
func seatbeltProfileWithProxyPorts(req BashRunRequest, networkAllowlist []string, httpProxyPort, socksProxyPort int) string {
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
	// Network: default-deny + explicit port allowlist (per partner
	// pin C3 — never `localhost:*`).
	if httpProxyPort > 0 {
		fmt.Fprintf(&b, "(allow network-outbound (remote ip \"localhost:%d\"))\n", httpProxyPort)
	}
	if socksProxyPort > 0 {
		fmt.Fprintf(&b, "(allow network-outbound (remote ip \"localhost:%d\"))\n", socksProxyPort)
	}
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
//
// Per B3b-4-1 the entry's Source is stamped
// "sandbox_substrate_heuristic" so output rendering and structured
// telemetry can mark the entry as low-confidence inferred-from-stderr
// rather than authoritative. The legacy Kind/Message fields are kept
// for backward compatibility with callers that consumed the original
// shape.
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
		Source:  string(SourceSandboxSubstrateHeuristic),
		Message: "low confidence: heuristic inferred sandbox-exec denial of filesystem or network operation; see stderr",
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

// seatbeltProxyDownError signals that the supervised proxy child has
// died mid-session. The runner refuses to execute the command rather
// than silently fall back to direct egress (which would bypass the
// allowlist). Classification is the partner-locked
// "network_proxy_failed" string so callers can distinguish it from
// generic sandbox setup failures.
func seatbeltProxyDownError(req BashRunRequest) BashRunResult {
	return BashRunResult{
		Output: "seatbelt: netproxy child has exited; refusing to execute command without active proxy. " +
			"This is the partner-locked no-silent-fallback invariant — the proxy enforces the network allowlist " +
			"and a missing proxy means the sandbox cannot enforce policy. Restart Elnath to recover.",
		IsError:        true,
		Classification: "network_proxy_failed",
		CWD:            req.DisplayCWD,
	}
}
