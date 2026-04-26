//go:build linux

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// netproxyBridgeProcessTag is the argv marker used by lifecycle tests
// to pgrep the bridge wrapper from outside the bwrap namespace. The
// production bridge subcommand is registered as `netproxy-bridge`; the
// constant lives here so the integration test file can reference the
// same string the cmd dispatcher answers to.
const netproxyBridgeProcessTag = "netproxy-bridge"

// netproxyBridgeUserCommandTimeout caps how long the user command may
// run inside the bridge before the bridge force-cancels and exits.
// Bash invocations that exceed BashRunRequest's own timeout will
// already be canceled by the parent BwrapRunner via context; this
// secondary deadline guards against a stuck child blocking the
// bridge process indefinitely.
const netproxyBridgeUserCommandTimeout = 10 * time.Minute

// cmdNetproxyBridge implements the v41 / B3b-4-3 production Linux
// bridge subcommand. Unlike the B3b-4-S0 spike subcommand
// (`netproxy-bridge-spike`), this is the runtime that BwrapRunner
// productionizes for proxy-required allowlists. The subcommand is
// invoked AS the bwrap-spawned wrapper command (i.e. bwrap exec's
// `/path/to/elnath netproxy-bridge ...` directly, NOT via bash). It:
//
//  1. installs prctl(PR_SET_PDEATHSIG, SIGTERM) BEFORE binding any
//     listener so the kernel signals the bridge if the bwrap parent
//     dies unexpectedly (no orphans),
//  2. binds two TCP loopback listeners inside the netns: one for the
//     HTTP CONNECT proxy and one for the SOCKS5 TCP proxy,
//  3. spawns --user-cmd as its child via /bin/bash -c (pre-existing
//     BashRunRequest.Command shape — see partner verdict on quoting),
//  4. for each accepted TCP connection on either listener, opens a
//     UDS connection to the corresponding host UDS (--uds-http and
//     --uds-socks) and splices bytes both ways. The host-side UDS is
//     served by the real netproxy proxy child running OUTSIDE the
//     bwrap netns; this bridge only forwards bytes.
//  5. waits for the user command to exit, returns its exit code via
//     the wrapping process exit.
//
// Lifecycle ownership: the bridge IS the bwrap-spawned wrapper AND
// the parent of the user command. When the user command exits, the
// bridge closes its listeners and exits; when bwrap exits, the kernel
// SIGTERMs the bridge via pdeathsig. There is no
// "/proc/self/exe netns-bridge & exec ..." pattern (rejected by
// critic C1 because that leaves the bridge orphaned mid-request when
// the wrapping bash exec's away).
//
// Distinct from cmd_netproxy_bridge_spike_linux.go: that file is the
// closed S0 spike, this file is the production wiring.
func cmdNetproxyBridge(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet(netproxyBridgeProcessTag, flag.ContinueOnError)
	udsHTTP := fs.String("uds-http", "", "host-side Unix socket path bind-mounted into the netns; receives connections accepted on --listen-http")
	udsSOCKS := fs.String("uds-socks", "", "host-side Unix socket path bind-mounted into the netns; receives connections accepted on --listen-socks")
	listenHTTP := fs.String("listen-http", "127.0.0.1:0", "loopback address:port the bridge binds inside the netns for HTTP CONNECT")
	listenSOCKS := fs.String("listen-socks", "127.0.0.1:0", "loopback address:port the bridge binds inside the netns for SOCKS5 TCP")
	userCmd := fs.String("user-cmd", "", "shell command executed as the bridge's child after the listeners are up")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *udsHTTP == "" && *udsSOCKS == "" {
		return errors.New("at least one of --uds-http or --uds-socks must be set")
	}
	if *userCmd == "" {
		return errors.New("--user-cmd is required")
	}

	// PR_SET_PDEATHSIG MUST be set BEFORE accepting connections so
	// any race where bwrap dies between fork and listener-bind still
	// kills this process. Set on the calling goroutine's OS thread,
	// which is the runtime's main thread for the subcommand.
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(syscall.SIGTERM), 0, 0, 0); err != nil {
		return fmt.Errorf("prctl PR_SET_PDEATHSIG: %w", err)
	}

	// Bind the two TCP listeners ONLY if a UDS endpoint was provided
	// for that protocol. This matches the host-side proxy: an
	// allowlist that needs only HTTP CONNECT will not have a SOCKS
	// UDS, and vice versa. Refusing to bind keeps the bridge surface
	// minimal.
	var (
		httpListener  net.Listener
		socksListener net.Listener
		err           error
	)
	if *udsHTTP != "" {
		httpListener, err = net.Listen("tcp", *listenHTTP)
		if err != nil {
			return fmt.Errorf("listen http %s: %w", *listenHTTP, err)
		}
		defer httpListener.Close()
	}
	if *udsSOCKS != "" {
		socksListener, err = net.Listen("tcp", *listenSOCKS)
		if err != nil {
			if httpListener != nil {
				_ = httpListener.Close()
			}
			return fmt.Errorf("listen socks %s: %w", *listenSOCKS, err)
		}
		defer socksListener.Close()
	}

	// shutdown coordinates the accept-loop goroutine exit. Closing the
	// channel signals every in-flight goroutine to drain and the loop
	// goroutines themselves to exit on the next Accept error (which
	// follows immediately because Close unblocks Accept).
	shutdown := make(chan struct{})
	var wg sync.WaitGroup

	if httpListener != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runBridgeAcceptLoopProd(httpListener, *udsHTTP, shutdown, os.Stderr)
		}()
	}
	if socksListener != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runBridgeAcceptLoopProd(socksListener, *udsSOCKS, shutdown, os.Stderr)
		}()
	}

	runErr := runBridgeUserCommand(ctx, *userCmd)

	// Tear listeners down BEFORE waiting on the goroutines so the
	// in-flight Accept calls return with ErrClosed; otherwise wg.Wait
	// would block on the still-blocked Accept.
	close(shutdown)
	if httpListener != nil {
		_ = httpListener.Close()
	}
	if socksListener != nil {
		_ = socksListener.Close()
	}
	wg.Wait()

	return runErr
}

// runBridgeAcceptLoopProd accepts TCP connections on listener and
// proxies each to a fresh UDS connection at udsPath. Bidirectional
// io.Copy closes both halves when either side EOFs. Returns when the
// listener is closed (Accept returns net.ErrClosed) or when shutdown
// is closed.
//
// Per-connection diagnostic errors are emitted to stderr (M-3): the
// upstream netproxy listener (HTTP CONNECT / SOCKS5 framing) on the
// host side records per-connection Decisions through its own EventSink,
// but transport-layer failures (e.g., the host-side UDS path
// disappearing because the supervised proxy child died) used to leave
// zero trace. The parent BwrapRunner forwards bridge stderr through
// its bash invocation's stderr drain, so a single line per failed
// dial reaches the operator and disambiguates "user network call
// failed" from "supervised proxy died".
func runBridgeAcceptLoopProd(listener net.Listener, udsPath string, shutdown <-chan struct{}, stderr io.Writer) {
	for {
		select {
		case <-shutdown:
			return
		default:
		}
		tcpConn, err := listener.Accept()
		if err != nil {
			// Accept returns an error on listener.Close() — that is
			// the normal exit path on shutdown. Any other error
			// terminates the loop too because there is no recovery
			// strategy for a wedged listener.
			return
		}
		go bridgeConn(tcpConn, udsPath, stderr)
	}
}

// bridgeConn opens a UDS connection to the host-side proxy and
// splices bytes both ways with the accepted TCP connection. The
// goroutine exits when either copy returns EOF or an error; both
// connections are closed via defer so a unilateral EOF still releases
// the other side.
//
// M-3: when net.Dial("unix", udsPath) fails (path missing, kernel
// refuses, supervised proxy child crashed), bridgeConn emits a single
// `netproxy-bridge: dial <path>: <err>` line to stderr so the parent
// BwrapRunner has signal to attribute the resulting bash failure to a
// substrate-side cause rather than a user-network problem. Pre-fix
// this function silently returned, leaving zero trace and amplifying
// the M-1 race-window symptom.
func bridgeConn(tcpConn net.Conn, udsPath string, stderr io.Writer) {
	defer tcpConn.Close()
	uds, err := net.Dial("unix", udsPath)
	if err != nil {
		if stderr != nil {
			fmt.Fprintf(stderr, "netproxy-bridge: dial %s: %v\n", udsPath, err)
		}
		return
	}
	defer uds.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(uds, tcpConn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(tcpConn, uds); done <- struct{}{} }()
	<-done
}

// runBridgeUserCommand spawns the user command as a child of the
// bridge process. Stdout/stderr are wired to the bridge's own — bwrap
// pipes these out to the parent BwrapRunner, which captures them via
// runBashCmd. A bounded grace deadline guards against hung children
// blocking the bridge forever; the parent BashRunRequest's own
// timeout reaches the bridge first via ctx cancellation.
//
// CRITICAL quoting note (per partner verdict): userCmd is the
// existing BashRunRequest.Command — already a shell string the agent
// provided. We pass it as a SINGLE argument to /bin/bash -c with no
// extra quoting layers. Multiple bash sublayers or string
// concatenation would create new expansion vulnerabilities relative
// to the existing direct/seatbelt bash execution path; the bridge
// must behave identically.
func runBridgeUserCommand(ctx context.Context, userCmd string) error {
	runCtx, cancel := context.WithTimeout(ctx, netproxyBridgeUserCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/bin/bash", "-c", userCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("user command exited with code %d", exitErr.ExitCode())
		}
		return fmt.Errorf("user command failed: %w", err)
	}
	return nil
}
