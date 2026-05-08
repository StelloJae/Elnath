# Sandbox Network Operator Guide

This guide summarizes Elnath's current sandbox/network posture for operators. It covers what is available, how to configure it, how to read network events, and which security claims remain out of scope.

## Current Capabilities

- `DirectRunner` remains the non-sandbox host-process runner. It is useful for normal tool execution with guardrails, but it is not a sandbox.
- On macOS, `SeatbeltRunner` supports filesystem confinement, default-deny network policy, proxy-mediated domain/IP allowlists, structured `network_proxy` violation reporting, and permitted connection audit metadata.
- On Linux, `BwrapRunner` supports filesystem confinement, default-deny network policy, proxy-mediated domain/IP allowlists, structured `network_proxy` violation reporting, and permitted connection audit metadata.
- Production config can select sandboxed Bash/Git runners through `sandbox.mode`, `sandbox.network_allowlist`, and `sandbox.network_denylist`.
- `BashTool` and `GitTool` use the configured runner path when sandbox config is present.
- Stateful sandbox runners are scoped per run so proxy decisions, audit events, and violation surfaces do not rely on one global runner instance.

## Configure Sandbox Network Policy

Use the `sandbox` section in your Elnath config file:

```yaml
sandbox:
  mode: seatbelt
  network_allowlist:
    - github.com:443
    - proxy.golang.org:443
  network_denylist:
    - example.com:443
```

Use `mode: seatbelt` on macOS and `mode: bwrap` on Linux. The default no-config path remains `DirectRunner`.

If sandbox mode or network policy is configured, Elnath must be able to enforce it. Unsupported substrates, invalid allowlist entries, direct mode with network policy, or missing mode with network policy fail loudly instead of falling back to `DirectRunner`.

`sandbox.network_denylist` wins over `sandbox.network_allowlist`. Keep denylist entries specific and review them when a connection appears blocked unexpectedly.

## Starter Allowlist Printer

Elnath provides a read-only starter allowlist printer for explicit opt-in sandbox network configuration:

```bash
elnath sandbox print-starter-allowlist --list-groups
elnath sandbox print-starter-allowlist --mode seatbelt --group git-hosting,go
elnath sandbox print-starter-allowlist --mode bwrap --group python,node
```

The command prints YAML only. It does not write config, install defaults, enable network access automatically, or change runtime policy.

Paste the printed YAML into your Elnath config file, usually:

```text
~/.elnath/config.yaml
```

If you run Elnath with `--config <path>`, paste the snippet into that config file instead. Network allowlist changes require an Elnath restart. For daemon use:

```bash
elnath daemon stop
elnath daemon start
```

Starter groups are suggestions for common development workflows, not safe defaults. Only paste groups you actually need. The `containers` group is advanced explicit opt-in; registry workflows may require additional domains.

## Operating Blocked And Permitted Events

When sandbox network policy blocks a connection, Elnath may report a `network_proxy` or `dns_resolver` violation.

| Reason | Meaning | Typical operator response |
| --- | --- | --- |
| `not_in_allowlist` | The destination did not match any configured allowlist entry. | If you trust the destination, add its exact `host:port` to `sandbox.network_allowlist` and restart Elnath. |
| `denied_by_rule` | The destination matched `sandbox.network_denylist`. | Keep the block, or remove the denylist entry only if the destination is intentionally allowed. |
| `dns_resolution_blocked` | DNS lookup failed or resolver output violated policy. Source may be `dns_resolver`. | Check the hostname and resolver environment. Do not broaden allowlists blindly. |
| `local_binding_disabled` | A loopback, private, or otherwise special-range target was blocked by default. | Add an explicit per-port local/IP allowlist entry only if you intend to permit that local service. |

Permitted connection audit records are for metadata review. They help answer which host/port/protocol was allowed and why. They should not be treated as packet capture, request payload logging, TLS inspection, or proof that a broader hostile network is safe.

When a host is blocked, prefer the narrowest fix:

1. Confirm the tool really needs network access.
2. Confirm the destination and port are trusted.
3. Add the exact `host:port` entry if needed.
4. Restart Elnath.
5. Re-run the task and confirm the resulting event source and reason.

## Security Posture And Non-Goals

- Sandbox network policy is default-deny when configured.
- Denylist entries override allowlist entries.
- Starter allowlist groups are suggestions, not safe defaults.
- There is no silent default allowlist.
- Elnath does not enable sandbox mode by default.
- `DirectRunner` is not a sandbox.
- The starter allowlist printer is read-only; mutating default installation is not available.
- UDP and QUIC traffic are not supported in this sandbox version.
- Hot reload for sandbox network config is not available; restart Elnath after changes.

## DNS Posture

v42-3 resolve-pin hardening resolves once during policy evaluation and dials the pinned IP literal for that connection. This gives each accepted connection a single policy-evaluated upstream target, but it is not a hostile-DNS defense.

DNS rebinding is not fully defended. If hostile DNS is in scope, use lower-layer controls.

Lower-layer controls include firewall rules, VPC policy, corporate proxy enforcement, endpoint policy, or equivalent OS/network controls. Elnath does not ship DNS proxying in this phase, and hostile-resolver resistance remains outside the local sandbox claim.

## Proxy Compatibility Matrix

Elnath keeps both HTTP CONNECT and SOCKS5 listeners because common development tools do not share one reliable proxy interface.

| Tool | `HTTP_PROXY` / `HTTPS_PROXY` | `ALL_PROXY=socks5h://` | Practical implication |
| --- | --- | --- | --- |
| `git` | Supported for HTTP(S) remotes through Git/curl proxy configuration. | Supported indirectly through libcurl-backed configurations; exact SOCKS behavior is build-dependent. | Keep HTTP CONNECT as the primary Git path; SOCKS5 remains useful but should not be the only path. |
| `curl` | Supported. | Supported when curl is built with SOCKS support. | Useful for validating both listener types. |
| `wget` | Supported for HTTP/HTTPS proxy workflows. | Not a reliable default across Wget workflows. | HTTP CONNECT support is needed. |
| `go mod` | Supported through Go's `net/http` proxy-from-environment path for HTTP(S) module downloads. | Not documented as a first-class Go module proxy environment path. | HTTP(S) proxy support is required for Go module fetching. |
| `npm` | Supported through npm proxy / https-proxy config and HTTP(S) proxy environment handling. | Not a documented primary npm proxy path. | HTTP(S) proxy support is the stable Node package workflow. |
| `pip` | Supported through `--proxy`, config, and HTTP(S) proxy environment variables. | SOCKS support is dependency/configuration dependent rather than the baseline path. | HTTP(S) proxy support is the safer Python package workflow. |
| `cargo` | Supported through Cargo HTTP proxy configuration and HTTP(S) proxy environment variables. | SOCKS behavior is not documented as the baseline Cargo path. | HTTP(S) proxy support is required for Rust package workflows. |

Conclusion: dual HTTP CONNECT plus SOCKS5 remains justified. SOCKS-only is not enough for the target workflow set.

## Linux Bridge Evidence Note

The current repository includes Linux bridge spike and production bridge surfaces, but the latest residual audit ran on `darwin/arm64` without `bwrap`. That audit could inspect the code and test names, but it could not freshly prove the Linux bridge bind/connect/teardown lifecycle.

Fresh Linux bridge lifecycle evidence remains an optional follow-up when a Linux host or CI environment with `bwrap` is available. Until then, do not describe the Linux bridge lifecycle as freshly proven by the macOS audit.

## Claim Grammar

Allowed claims:

- CI-backed macOS/Linux sandbox substrates.
- Proxy-mediated domain/IP allowlists.
- Structured `network_proxy` violations.
- Permitted connection metadata audit.
- Production config reaches the sandbox runner path.
- Read-only starter allowlist printer.

Mandatory caveat:

> DNS rebinding is not fully defended. If hostile DNS is in scope, use lower-layer controls.

Do not claim:

- DNS rebinding is fully defended.
- Allowlists are inherently safe against hostile DNS.
- Elnath ships DNS proxying.
- Elnath defends hostile DNS inside the local proxy.
- This is a complete egress-security boundary.
- Starter entries are installed automatically.
- Sandbox network defaults are applied without explicit config.
- Mutating starter-default installation is available.
- UDP or QUIC traffic is available in this sandbox version.
