package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

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
// As of B3b-4-3.5 per-connection Decisions are projected into stdout
// as `event=<json>` lines via tools.EncodeDecisionEventLine. The
// readiness preamble (`httpListen=`, `socksListen=`, `ready`) shares
// the same stdout — the prefix prevents collision so a parent runner
// can drain a single pipe and demux preamble lines vs event lines via
// tools.ParseDecisionEventLine. Errors continue to land on stderr.
//
// Decisions are still informational from the netproxy child's own
// perspective (it has already enforced policy by the time it emits
// the event) but the parent BwrapRunner uses them to populate
// BashRunResult.Violations on the deny path so the agent sees a
// structured deny reason instead of a bare curl exit code.
func cmdNetproxy(ctx context.Context, args []string) error {
	sink := &netproxyStdoutSink{stdout: os.Stdout, stderr: os.Stderr}
	cfg := tools.ProxyChildConfig{
		Args:        args,
		Sink:        sink,
		Stderr:      os.Stderr,
		ReadyWriter: os.Stdout,
		Resolver:    tools.NewSystemResolver(),
	}
	code := tools.RunProxyChildMain(ctx, cfg)
	if code != 0 {
		return fmt.Errorf("netproxy exited with code %d", code)
	}
	return nil
}

// netproxyStdoutSink is the EventSink installed by cmdNetproxy. Each
// Decision is serialized as a `event=<json>` line on stdout so the
// parent BwrapRunner / SeatbeltRunner can ingest it via the existing
// stdout pipe. Errors land on stderr.
//
// The sink serializes Decision writes through stdoutMu so concurrent
// listener goroutines (HTTP CONNECT and SOCKS5) cannot interleave
// half-lines on the same pipe. Encoding errors are demoted to stderr
// rather than propagating because the listener-side path must never
// block on telemetry (partner-locked invariant for the proxy accept
// loop).
type netproxyStdoutSink struct {
	stdoutMu sync.Mutex
	stdout   *os.File
	stderr   *os.File
}

func (s *netproxyStdoutSink) EmitDecision(d tools.Decision) {
	if s.stdout == nil {
		return
	}
	line, err := tools.EncodeDecisionEventLine(d)
	if err != nil {
		if s.stderr != nil {
			fmt.Fprintln(s.stderr, "netproxy: encode decision:", err)
		}
		return
	}
	s.stdoutMu.Lock()
	defer s.stdoutMu.Unlock()
	_, _ = io.WriteString(s.stdout, line)
}

func (s *netproxyStdoutSink) EmitError(err error) {
	if err == nil || s.stderr == nil {
		return
	}
	fmt.Fprintln(s.stderr, "netproxy:", err)
}
