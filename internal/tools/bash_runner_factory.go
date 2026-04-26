package tools

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// SandboxConfig captures the user-facing sandbox/runner mode for BashTool.
// Phase 1 supports "direct" (DirectRunner host-process backend) and
// "seatbelt" (macOS Seatbelt substrate). "bwrap" is the B3b-3 Linux
// lane (default-deny network only). "Sandbox" labeling stays reserved
// for substrates with namespace/chroot-equivalent enforcement; silent
// fallback to DirectRunner is forbidden because that would let
// "sandbox=on" requests run unsandboxed without notice.
//
// NetworkAllowlist accepts the broader B3b-4 grammar (loopback IPs,
// non-loopback IPs, domain entries, scoped wildcards). The factory
// inspects each entry's shape: a loopback-only allowlist is honored
// by Seatbelt directly; any non-loopback IP entry or domain entry
// requires the netproxy proxy substrate, which the substrate-wiring
// lanes (B3b-4-2 macOS, B3b-4-3 Linux) ship later. Until those lanes
// land the factory rejects such entries LOUDLY at construction with
// an error naming the in-progress lane and the restart-required
// disclosure — never a silent fallback to DirectRunner.
//
// NetworkDenylist holds explicit deny entries with the same grammar.
// Denylist matches always win over allowlist matches (deny-wins, per
// netproxy_policy.go EvaluateWithDenylist). Like NetworkAllowlist
// entries, non-loopback denylist entries currently require proxy
// wiring; until that ships, a non-empty denylist on Seatbelt or
// Bwrap is rejected at the factory.
//
// NetworkProxyConnectTimeout is the operator-tunable upper bound on
// initial proxy handshake reads. Zero falls back to the default
// (netproxyDefaultConnectTimeout). The field is accepted at parse
// time so config can be written ahead of substrate wiring; the value
// is NOT yet wired into the substrate (B3b-4-2 / B3b-4-3 will thread
// it through to the netproxy listeners).
//
// LLM/tool-param input MUST NOT populate this struct. Per the v41 partner
// verdict, only user-side configuration (config file, CLI flag, or
// interactive approval) constructs SandboxConfig. Bash command parameters
// have no field that influences the runner backend.
//
// There is intentionally NO ProxyEnabled flag. Proxy need is INFERRED
// from the allowlist/denylist shape (presence of any non-loopback IP
// or any domain entry). Adding a boolean would create a footgun: an
// operator could enable the proxy without configuring entries, or
// vice versa. Inference closes the gap.
type SandboxConfig struct {
	// Mode selects the runner backend. Empty string is treated as "direct".
	Mode string

	// NetworkAllowlist holds "host:port" entries permitted for
	// outbound TCP. Grammar mirrors netproxy_policy.go ParseAllowlist
	// (loopback IPs, non-loopback IPs, domain entries, *.host
	// subdomain wildcards, **.host apex+sub wildcards). Only the
	// substrate runners read this field; DirectRunner ignores it
	// because the host-process backend has no policy to enforce.
	NetworkAllowlist []string

	// NetworkDenylist holds "host:port" entries denied even when the
	// allowlist would permit them. Same grammar as NetworkAllowlist.
	// Denylist always wins over allowlist (deny-wins).
	NetworkDenylist []string

	// NetworkProxyConnectTimeout bounds initial proxy handshake reads
	// inside the netproxy listener. Zero uses the package default
	// (netproxyDefaultConnectTimeout). Substrate-wiring lanes thread
	// this value into the proxy listener; B3b-4-1 only validates it.
	NetworkProxyConnectTimeout time.Duration
}

// netproxyDefaultConnectTimeout is the fallback for
// SandboxConfig.NetworkProxyConnectTimeout. Mirrors the existing
// netproxy_proxy.go connectIOTimeout constant so callers picking up
// the default see the same value the in-tree proxy listeners use.
const netproxyDefaultConnectTimeout = 30 * time.Second

// ResolvedNetworkProxyConnectTimeout returns the operator-supplied
// timeout when set, or the package default otherwise. Used by future
// substrate-wiring lanes to plumb the value into the netproxy listener.
func (c SandboxConfig) ResolvedNetworkProxyConnectTimeout() time.Duration {
	if c.NetworkProxyConnectTimeout > 0 {
		return c.NetworkProxyConnectTimeout
	}
	return netproxyDefaultConnectTimeout
}

// networkProxyDisclosure returns the partner-locked disclosure
// sentences appended to factory rejection errors and substrate probe
// messages so operators always learn three Phase 1 invariants in the
// same words: restart on config edit, UDP/QUIC blocked, DNS rebinding
// not fully defended.
func networkProxyDisclosure() string {
	return strings.Join([]string{
		"Network allowlist changes require Elnath restart.",
		"UDP and QUIC egress are blocked in this sandbox version.",
		"DNS rebinding is not fully defended; for hostile DNS threat models, enforce egress at a lower layer.",
	}, " ")
}

// NewBashRunnerForConfig returns a BashRunner for the given config or an
// error describing why the requested mode is unavailable. Callers MUST
// surface the error to the user instead of substituting DirectRunner —
// silent fallback would defeat the purpose of asking for a sandbox.
//
// For substrate modes (seatbelt, bwrap) the factory probes the runner at
// construction and refuses to return one whose Probe reports
// Available=false. The probe message becomes part of the returned error
// so the user sees the concrete reason (wrong platform, missing binary,
// etc.) rather than a generic "unsupported".
//
// Allowlist/denylist shape determines whether substrate-wiring lanes
// must be present. Loopback-only entries on Seatbelt are honored
// directly by the SBPL `(remote ip "localhost:<port>")` rule; any
// entry that requires the proxy substrate (non-loopback IP, domain)
// is rejected here with explicit B3b-4-2 / B3b-4-3 wording until the
// proxy is wired through. Bwrap rejects every non-empty allowlist
// because the substrate has no equivalent rule yet.
func NewBashRunnerForConfig(cfg SandboxConfig) (BashRunner, error) {
	switch cfg.Mode {
	case "", "direct":
		return NewDirectRunner(), nil
	case "seatbelt":
		if err := rejectIfRequiresProxy(cfg.NetworkAllowlist, cfg.NetworkDenylist, "seatbelt", "B3b-4-2"); err != nil {
			return nil, err
		}
		r, err := NewSeatbeltRunnerWithAllowlist(cfg.NetworkAllowlist)
		if err != nil {
			return nil, fmt.Errorf("sandbox mode %q: %w", cfg.Mode, err)
		}
		p := r.Probe(context.Background())
		if !p.Available {
			return nil, fmt.Errorf("sandbox mode %q unavailable: %s", cfg.Mode, p.Message)
		}
		return r, nil
	case "bwrap":
		// B3b-4-3 wires the netproxy substrate to BwrapRunner. Loopback
		// IP entries on bwrap remain rejected because bwrap has no
		// SBPL-equivalent loopback rule and the netns blocks all egress
		// — proxy-required entries (domain or non-loopback IP) flow
		// through the netproxy bridge. A pure loopback-only allowlist
		// is therefore still a misconfiguration on bwrap; the operator
		// should remove the entry to fall back to default-deny.
		if err := rejectIfBwrapLoopbackOnly(cfg.NetworkAllowlist, cfg.NetworkDenylist); err != nil {
			return nil, err
		}
		r, err := NewBwrapRunnerWithAllowlist(cfg.NetworkAllowlist)
		if err != nil {
			return nil, fmt.Errorf("sandbox mode %q: %w", cfg.Mode, err)
		}
		p := r.Probe(context.Background())
		if !p.Available {
			return nil, fmt.Errorf("sandbox mode %q unavailable: %s", cfg.Mode, p.Message)
		}
		return r, nil
	default:
		return nil, fmt.Errorf("unknown sandbox mode %q", cfg.Mode)
	}
}

// rejectIfRequiresProxy returns an explicit B3b-4 deferral error when
// any allowlist or denylist entry needs the proxy substrate. Loopback
// IP entries are accepted on Seatbelt because the SBPL filter handles
// them directly; the same is NOT true for bwrap (handled separately).
//
// The error wording is partner-locked: it MUST cite the in-progress
// lane (B3b-4-2 macOS, B3b-4-3 Linux), MUST forbid silent fallback to
// DirectRunner, and MUST include the restart-required +
// UDP/QUIC-blocked + DNS-rebinding disclosure so operators learn the
// Phase 1 invariants without consulting external docs.
func rejectIfRequiresProxy(allowlist, denylist []string, _ string, lane string) error {
	check := func(entries []string, kind string) error {
		for _, raw := range entries {
			needsProxy, parseErr := entryRequiresProxy(raw)
			if parseErr != nil {
				return fmt.Errorf("network %s entry %q invalid: %w", kind, raw, parseErr)
			}
			if !needsProxy {
				continue
			}
			return fmt.Errorf(
				"network %s entry %q requires B3b-4 proxy wiring; "+
					"Seatbelt/Bwrap proxy wiring is not available in this lane yet (%s macOS, B3b-4-3 Linux). "+
					"Loopback-only allowlist entries (e.g. 127.0.0.1:8080, [::1]:8080) are still accepted on Seatbelt; "+
					"or remove the entry to fall back to default-deny. %s",
				kind, raw, lane, networkProxyDisclosure(),
			)
		}
		return nil
	}
	if err := check(allowlist, "allowlist"); err != nil {
		return err
	}
	if err := check(denylist, "denylist"); err != nil {
		return err
	}
	return nil
}

// entryRequiresProxy reports whether a single allowlist/denylist
// entry needs the netproxy substrate to enforce. Loopback IP literal
// entries do NOT require proxy (Seatbelt handles them at the SBPL
// layer); domain entries and non-loopback IP entries DO.
//
// Parsing is delegated to ParseAllowlist (the netproxy_policy.go
// grammar) so the factory keeps a single source of truth for the
// host:port grammar and stays in lock-step with substrate evaluation.
func entryRequiresProxy(entry string) (bool, error) {
	parsed, err := ParseAllowlist([]string{entry})
	if err != nil {
		return false, err
	}
	rules := parsed.Rules()
	if len(rules) == 0 {
		return false, nil
	}
	r := rules[0]
	if !r.IsIP {
		return true, nil
	}
	return !r.IP.IsLoopback(), nil
}

// firstNetEntry returns the first non-empty entry across allowlist
// and denylist for diagnostic message formatting in the bwrap
// rejection path. Local helper because the existing firstNonEmpty
// elsewhere has a different signature.
func firstNetEntry(allowlist, denylist []string) string {
	if len(allowlist) > 0 {
		return allowlist[0]
	}
	if len(denylist) > 0 {
		return denylist[0]
	}
	return ""
}

// rejectIfBwrapLoopbackOnly errors when the allowlist or denylist on
// bwrap contains a loopback IP entry (e.g. 127.0.0.1:8080). Bwrap has
// no SBPL-equivalent loopback rule and the netns blocks all egress, so
// a loopback-only entry cannot be honored by either the substrate or
// the netproxy bridge. The factory rejects rather than silently
// degrading to default-deny.
//
// Proxy-required entries (domain, non-loopback IP) flow through the
// netproxy substrate and are accepted; an empty config is the
// default-deny path and is also accepted.
func rejectIfBwrapLoopbackOnly(allowlist, denylist []string) error {
	check := func(entries []string, kind string) error {
		for _, raw := range entries {
			needsProxy, parseErr := entryRequiresProxy(raw)
			if parseErr != nil {
				return fmt.Errorf("network %s entry %q invalid: %w", kind, raw, parseErr)
			}
			if needsProxy {
				continue
			}
			return fmt.Errorf(
				"network %s entry %q is a loopback IP literal which bwrap cannot honor; "+
					"bwrap has no SBPL-equivalent loopback rule and the netns blocks all egress. "+
					"Remove the entry to fall back to default-deny on bwrap. %s",
				kind, raw, networkProxyDisclosure(),
			)
		}
		return nil
	}
	if err := check(allowlist, "allowlist"); err != nil {
		return err
	}
	return check(denylist, "denylist")
}

// validateNetworkAllowlist checks each entry is "<IP>:<port>" with a
// valid loopback IP and a non-zero port < 65536. Domain names AND
// non-loopback IPs are rejected with an explicit B3b-4 deferral
// message so config that expected broader allowlist semantics cannot
// silently degrade to "no network".
//
// Phase 1 supports only loopback (127.0.0.1, ::1) because Seatbelt's
// SBPL `(remote ip ...)` filter accepts only "*" or "localhost" as the
// host portion — arbitrary IPv4/IPv6 host filtering requires a
// userspace network proxy (the B3b-4 lane). The validator surfaces
// this constraint at construction so a caller asking for
// "10.0.0.5:5000" gets a clear error instead of a silently-broken
// profile.
//
// Returns a defensive copy so callers cannot mutate the live policy
// after the runner captures it.
func validateNetworkAllowlist(allowlist []string) ([]string, error) {
	if len(allowlist) == 0 {
		return nil, nil
	}
	cleaned := make([]string, 0, len(allowlist))
	for _, entry := range allowlist {
		host, portStr, err := net.SplitHostPort(entry)
		if err != nil {
			return nil, fmt.Errorf("network allowlist entry %q must be IP:port: %w", entry, err)
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return nil, fmt.Errorf("network allowlist entry %q must use an IP address; domain matching is deferred to B3b-4", entry)
		}
		if !ip.IsLoopback() {
			return nil, fmt.Errorf("network allowlist entry %q must target loopback (127.0.0.1 or ::1); non-loopback allowlists require the network proxy substrate (B3b-4)", entry)
		}
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil || port == 0 {
			return nil, fmt.Errorf("network allowlist entry %q has invalid port", entry)
		}
		cleaned = append(cleaned, entry)
	}
	return cleaned, nil
}
