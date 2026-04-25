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
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// cmdNetproxyBridgeSpike implements the v41 / B3b-4-S0 Linux netns
// bridge spike. It is invoked AS the bwrap-spawned wrapper command
// (i.e. bwrap exec's `/path/to/elnath netproxy-bridge-spike ...`
// directly, NOT via bash). The subcommand:
//
//  1. installs prctl(PR_SET_PDEATHSIG, SIGTERM) so the kernel signals
//     the bridge if the bwrap parent dies unexpectedly (no orphans),
//  2. binds a TCP loopback listener at --listen inside the netns,
//  3. spawns --user-cmd as its child via `bash -c`,
//  4. for each accepted TCP connection, opens a UDS connection to the
//     bind-mounted --uds path and splices bytes both ways with
//     io.Copy (blind TCP forwarder; no HTTP CONNECT or SOCKS5
//     awareness — those belong to B3b-4-0 production proxy core),
//  5. waits for the user command to exit, returns its exit code.
//
// Lifecycle ownership is the bridge process itself: it is the wrapper
// AND the proxy AND the parent of the user command. When the user
// command exits, the bridge exits; when bwrap exits, the kernel
// SIGTERMs the bridge via pdeathsig. There is no
// "/proc/self/exe netns-bridge & exec curl" pattern — that approach
// (rejected by critic C1) leaves the bridge orphaned mid-request when
// the wrapping bash exec's away.
//
// This is a SPIKE only. It is hidden from `elnath help` because no
// production code path invokes it; the integration test in
// internal/tools/netproxy_bridge_spike_linux_test.go is the only
// caller. Production B3b-4 proxy wiring (B3b-4-0 onward) will
// supersede this entirely.
func cmdNetproxyBridgeSpike(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("netproxy-bridge-spike", flag.ContinueOnError)
	udsPath := fs.String("uds", "", "host-side Unix socket path bind-mounted into the netns")
	listenAddr := fs.String("listen", "127.0.0.1:0", "loopback address:port the bridge binds inside the netns")
	userCmd := fs.String("user-cmd", "", "shell command executed as the bridge's child after the listener is up")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *udsPath == "" {
		return errors.New("--uds is required")
	}
	if *userCmd == "" {
		return errors.New("--user-cmd is required")
	}

	// PR_SET_PDEATHSIG must be set BEFORE accepting connections so
	// any race where bwrap dies between fork and listener-bind still
	// kills this process. set on the calling goroutine's OS thread,
	// which is the runtime's main thread for the subcommand.
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(syscall.SIGTERM), 0, 0, 0); err != nil {
		return fmt.Errorf("prctl PR_SET_PDEATHSIG: %w", err)
	}

	listener, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", *listenAddr, err)
	}
	defer listener.Close()

	bridgeDone := make(chan struct{})
	go func() {
		defer close(bridgeDone)
		runBridgeAcceptLoop(listener, *udsPath)
	}()

	if err := runUserCommand(ctx, *userCmd); err != nil {
		// Even if the user command failed, close the listener and
		// drain bridge goroutines so we exit cleanly. The exit code
		// from the user command is surfaced via the wrapping error.
		_ = listener.Close()
		<-bridgeDone
		return err
	}
	_ = listener.Close()
	<-bridgeDone
	return nil
}

// runBridgeAcceptLoop accepts TCP connections on listener and proxies
// each to a fresh UDS connection at udsPath. Bidirectional io.Copy
// closes both halves when either side EOFs. Errors are intentionally
// swallowed — this is a spike; the production proxy will record
// per-connection events.
func runBridgeAcceptLoop(listener net.Listener, udsPath string) {
	for {
		tcpConn, err := listener.Accept()
		if err != nil {
			return
		}
		go func(tcp net.Conn) {
			defer tcp.Close()
			uds, err := net.Dial("unix", udsPath)
			if err != nil {
				return
			}
			defer uds.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(uds, tcp); done <- struct{}{} }()
			go func() { _, _ = io.Copy(tcp, uds); done <- struct{}{} }()
			<-done
		}(tcpConn)
	}
}

// runUserCommand spawns the user command as a child of the bridge
// process. Stdout/stderr are wired to the bridge's own — bwrap pipes
// these out to the integration test, which asserts on the curl
// response body. A bounded grace deadline guards against hung
// children blocking the spike forever.
func runUserCommand(ctx context.Context, userCmd string) error {
	runCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
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
