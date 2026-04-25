// Package tools — netproxy_proxy_skeleton.go
//
// v41 / B3b-4-0 proxy core. Self-contained library used by the
// macOS Seatbelt and Linux bwrap substrate lanes (B3b-4-2, B3b-4-3)
// to enforce domain + IP allowlists for outbound TCP traffic. NOT
// wired into BashRunner in this lane.
//
// Partner-locked pins observed here:
//   - DNS rebinding is not fully defended (cite Codex
//     network-proxy/README.md:217-219).
//   - No allowLocalBinding boolean. Local services are reached only
//     via explicit per-port entries.
//   - Forked-child self-exec proxy model. RunProxyChildMain is the
//     entry function a future `cmd/elnath/cmd_netproxy.go`
//     subcommand will dispatch to. The substrate lanes (B3b-4-2,
//     B3b-4-3) will spawn the elnath binary as `elnath netproxy
//     ...`; this file makes that callable. NO in-process goroutine
//     proxy.
//   - Source enum is fixed at four values.
//   - No ProxyEnabled config flag — substrate lanes infer proxy need
//     from allowlist shape.

package tools

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"sync"
)

// ProxyChildConfig is the input to RunProxyChildMain. Args is the
// argv slice the future `elnath netproxy ...` subcommand would have
// received (without the subcommand name). Sink is the EventSink that
// receives Decisions and proxy-internal errors. HTTPListener and
// SOCKSListener may be set by the caller to skip the bind step (used
// in tests; future substrate lanes may also pre-bind so the parent
// can hand a verified-port number into the SBPL profile).
//
// When a listener is supplied via the config, the corresponding
// --http-listen / --socks-listen flag is ignored.
type ProxyChildConfig struct {
	Args          []string
	HTTPListener  net.Listener
	SOCKSListener net.Listener
	Sink          EventSink
	// Resolver is consulted by the proxy when an allowlisted
	// hostname needs its resolved IPs validated against the
	// special-range default-deny. nil means SystemResolver.
	Resolver Resolver
	// Stderr receives parser usage messages on --help / unknown
	// flags. Defaults to io.Discard so embedded tests stay quiet;
	// future cmd dispatch sets this to os.Stderr.
	Stderr io.Writer
}

// ProxyChildParsed is the parsed form of ProxyChildConfig.Args.
// Exported for direct unit testing of the parser.
type ProxyChildParsed struct {
	Help         bool
	HTTPListen   string
	SOCKSListen  string
	AllowEntries []string
	DenyEntries  []string
}

// ParseProxyChildArgs parses argv flags into a ProxyChildParsed
// struct. Flags:
//
//	--http-listen   <host:port>   listen address for the HTTP CONNECT
//	                              proxy. Empty = no HTTP listener.
//	--socks-listen  <host:port>   listen address for the SOCKS5 TCP
//	                              proxy. Empty = no SOCKS listener.
//	--allow         <host:port>   add an allowlist entry. Repeatable.
//	--deny          <host:port>   add a denylist entry. Repeatable.
//	--help                        print usage and exit.
//
// Note: at least one of --http-listen or --socks-listen must be
// provided OR the caller must inject pre-bound listeners via
// ProxyChildConfig. RunProxyChildMain checks that constraint after
// parsing.
func ParseProxyChildArgs(args []string) (ProxyChildParsed, error) {
	var p ProxyChildParsed
	var allowEntries multiStringFlag
	var denyEntries multiStringFlag

	fs := flag.NewFlagSet("netproxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&p.Help, "help", false, "print usage and exit")
	fs.StringVar(&p.HTTPListen, "http-listen", "", "HTTP CONNECT listen address (e.g. 127.0.0.1:3128)")
	fs.StringVar(&p.SOCKSListen, "socks-listen", "", "SOCKS5 TCP listen address (e.g. 127.0.0.1:8081)")
	fs.Var(&allowEntries, "allow", "allowlist entry, repeatable (host:port)")
	fs.Var(&denyEntries, "deny", "denylist entry, repeatable (host:port)")
	if err := fs.Parse(args); err != nil {
		return ProxyChildParsed{}, err
	}
	p.AllowEntries = []string(allowEntries)
	p.DenyEntries = []string(denyEntries)
	return p, nil
}

// multiStringFlag implements flag.Value for repeatable string flags.
type multiStringFlag []string

func (m *multiStringFlag) String() string         { return fmt.Sprint([]string(*m)) }
func (m *multiStringFlag) Set(v string) error     { *m = append(*m, v); return nil }

// RunProxyChildMain is the entry function a future `elnath netproxy
// ...` subcommand will dispatch to. Returns a process exit code: 0
// on graceful shutdown / --help; non-zero on parse / construction /
// listener errors.
//
// Per the partner-locked pin C4 ("forked-child self-exec proxy"),
// production B3b-4-2 and B3b-4-3 lanes will spawn this via
// `os/exec.Cmd{Path: /proc/self/exe, Args: [/proc/self/exe netproxy ...]}`
// rather than running it in-process. B3b-4-0 only ships the entry
// function; substrate-side spawn is out of scope here.
//
// Any listener bind failure or parser error flows through both the
// return code AND the sink's EmitError method, so the parent can
// observe errors structurally rather than relying on stderr scraping
// (partner-mini-lap N1 carry-forward).
//
// Caller MUST provide a non-nil Sink. A nil sink is a programmer
// error; the function returns code=2 to mark "invalid invocation."
func RunProxyChildMain(ctx context.Context, cfg ProxyChildConfig) int {
	if cfg.Sink == nil {
		return 2
	}
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}

	parsed, err := ParseProxyChildArgs(cfg.Args)
	if err != nil {
		cfg.Sink.EmitError(fmt.Errorf("netproxy parse: %w", err))
		fmt.Fprintln(cfg.Stderr, err)
		return 2
	}
	if parsed.Help {
		fmt.Fprintln(cfg.Stderr, "usage: elnath netproxy [--http-listen host:port] [--socks-listen host:port] [--allow host:port ...] [--deny host:port ...]")
		return 0
	}

	allow, err := ParseAllowlist(parsed.AllowEntries)
	if err != nil {
		cfg.Sink.EmitError(fmt.Errorf("netproxy allowlist: %w", err))
		return 1
	}
	deny, err := ParseDenylist(parsed.DenyEntries)
	if err != nil {
		cfg.Sink.EmitError(fmt.Errorf("netproxy denylist: %w", err))
		return 1
	}

	// Resolve listeners: prefer caller-provided over flag-derived.
	httpL := cfg.HTTPListener
	if httpL == nil && parsed.HTTPListen != "" {
		l, err := net.Listen("tcp", parsed.HTTPListen)
		if err != nil {
			cfg.Sink.EmitError(fmt.Errorf("netproxy http listen %s: %w", parsed.HTTPListen, err))
			return 1
		}
		httpL = l
	}
	socksL := cfg.SOCKSListener
	if socksL == nil && parsed.SOCKSListen != "" {
		l, err := net.Listen("tcp", parsed.SOCKSListen)
		if err != nil {
			// If the HTTP listener was already bound, close it so
			// we don't leak.
			if httpL != nil {
				_ = httpL.Close()
			}
			cfg.Sink.EmitError(fmt.Errorf("netproxy socks listen %s: %w", parsed.SOCKSListen, err))
			return 1
		}
		socksL = l
	}

	if httpL == nil && socksL == nil {
		cfg.Sink.EmitError(errors.New("netproxy: at least one of --http-listen or --socks-listen must be set"))
		return 1
	}

	resolver := cfg.Resolver

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 2)

	if httpL != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ServeHTTPConnect(ctx, httpL, allow, deny, resolver, cfg.Sink); err != nil {
				errs <- err
				cancel()
			}
		}()
	}
	if socksL != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ServeSOCKS5(ctx, socksL, allow, deny, resolver, cfg.Sink); err != nil {
				errs <- err
				cancel()
			}
		}()
	}

	// Wait for both goroutines to return. Listener.Close on context
	// cancel propagates through ServeHTTPConnect / ServeSOCKS5 which
	// return nil on graceful shutdown.
	wg.Wait()

	// Ensure listeners are closed; double-close is harmless.
	if httpL != nil {
		_ = httpL.Close()
	}
	if socksL != nil {
		_ = socksL.Close()
	}

	close(errs)
	if firstErr, ok := <-errs; ok {
		cfg.Sink.EmitError(firstErr)
		return 1
	}
	return 0
}
