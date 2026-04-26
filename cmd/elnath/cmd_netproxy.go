package main

import (
	"context"
	"fmt"
	"os"

	"github.com/stello/elnath/internal/tools"
)

// cmdNetproxy is the production `elnath netproxy ...` subcommand. It
// is the entry point that B3b-4-2 macOS Seatbelt and B3b-4-3 Linux
// bwrap substrate runners invoke when they self-exec the elnath
// binary as a forked-child proxy (per partner pin C4 forked-child
// self-exec model).
//
// The subcommand is hidden from `elnath help` because no operator
// invokes it directly; it exists only so SeatbeltRunner / BwrapRunner
// can spawn it via os.Executable() (NOT /proc/self/exe — that path
// does not exist on macOS; os.Executable() is the cross-platform
// replacement). The handler is a thin wrapper around
// tools.RunProxyChildMain that wires stderr to os.Stderr (so --help
// text reaches the operator on the rare debug invocation) and
// surfaces the return code as a Go error so the cmd dispatcher
// translates it into a non-zero process exit.
//
// A discarding sink is installed because the parent does not collect
// proxy events from this binary's stdout — the events flow back via
// the proxy's printed bound-port preamble plus the substrate's
// out-of-band telemetry channel (B3b-4-2 future). Sink errors land in
// stderr via the standard ChannelEventSink + RunProxyChildMain
// behavior; the discarding implementation here only suppresses
// proxy-internal Decisions, not bind-failure errors which RunProxyChildMain
// surfaces through the return code.
func cmdNetproxy(ctx context.Context, args []string) error {
	sink := &netproxyStderrSink{stderr: os.Stderr}
	cfg := tools.ProxyChildConfig{
		Args:        args,
		Sink:        sink,
		Stderr:      os.Stderr,
		ReadyWriter: os.Stdout,
	}
	code := tools.RunProxyChildMain(ctx, cfg)
	if code != 0 {
		return fmt.Errorf("netproxy exited with code %d", code)
	}
	return nil
}

// netproxyStderrSink is the EventSink installed by cmdNetproxy. It
// prints proxy-internal errors to stderr so an operator running the
// subcommand directly (e.g. for debugging) can see what failed; per
// connection allow/deny Decisions are dropped because cmdNetproxy is
// not the production observer. SeatbeltRunner / BwrapRunner that
// self-exec the binary install their own sinks via the structured
// telemetry channel established in B3b-4-2 / B3b-4-3.
type netproxyStderrSink struct {
	stderr *os.File
}

func (s *netproxyStderrSink) EmitDecision(_ tools.Decision) {}

func (s *netproxyStderrSink) EmitError(err error) {
	if err == nil || s.stderr == nil {
		return
	}
	fmt.Fprintln(s.stderr, "netproxy:", err)
}
