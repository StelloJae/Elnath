# Sandbox Starter Allowlist

Elnath can print starter `sandbox.network_allowlist` YAML for common development workflows. This is a read-only helper: it prints a snippet for you to review and paste, and it does not write config or enable network access automatically.

## Print a Starter Snippet

Show available groups:

```bash
elnath sandbox print-starter-allowlist --list-groups
```

Print a macOS Seatbelt snippet for Git hosting and Go module downloads:

```bash
elnath sandbox print-starter-allowlist --mode seatbelt --group git-hosting,go
```

Print a Linux bwrap snippet for Python and Node package downloads:

```bash
elnath sandbox print-starter-allowlist --mode bwrap --group python,node
```

The command requires both `--mode` and `--group` when printing active YAML. It does not print an active all-groups allowlist by default.

## Paste into Config

Paste the printed YAML into your Elnath config file. The default config path is:

```text
~/.elnath/config.yaml
```

If you run Elnath with `--config <path>`, paste the snippet into that config file instead.

Example:

```yaml
sandbox:
  mode: seatbelt
  network_allowlist:
    # git-hosting
    - github.com:443
    - gitlab.com:443
    - bitbucket.org:443
    # go
    - proxy.golang.org:443
    - sum.golang.org:443
```

Use `mode: seatbelt` on macOS and `mode: bwrap` on Linux. Only paste groups you actually need. Starter groups are suggestions, not safe defaults.

## Restart Required

Network allowlist changes require an Elnath restart. For daemon use, stop and start the daemon after editing config:

```bash
elnath daemon stop
elnath daemon start
```

For interactive CLI use, exit the current Elnath process and start a new one.

## Group Notes

- `git-hosting`: common HTTPS Git hosting domains.
- `python`: public PyPI package index and file host.
- `node`: public npm registry.
- `go`: public Go module proxy and checksum database.
- `rust`: public crates.io registry and static crate host.
- `containers`: advanced explicit opt-in. Registry workflows may require additional domains, and this group is not shown as active YAML unless requested.

If a package manager fetches from a private registry, mirror, postinstall asset host, Git dependency host, or CDN not listed here, add that exact `host:port` only after you trust it.

## Blocked Connection Reasons

When sandbox network policy blocks a connection, Elnath may report a `network_proxy` or `dns_resolver` violation. Common reasons:

| Reason | Meaning | Typical next step |
| --- | --- | --- |
| `not_in_allowlist` | The destination did not match any configured allowlist entry. | If you trust the destination, add its exact `host:port` to `sandbox.network_allowlist` and restart Elnath. |
| `denied_by_rule` | The destination matched `sandbox.network_denylist`. Denylist wins over allowlist. | Keep the block, or remove the denylist entry only if it was intentional to allow it. |
| `dns_resolution_blocked` | DNS lookup failed or resolver output violated policy. Source may be `dns_resolver`. | Check the hostname and resolver environment. Do not treat this as a reason to broaden allowlists blindly. |
| `local_binding_disabled` | A loopback, private, or otherwise special-range target was blocked by default. | Add an explicit per-port local/IP allowlist entry only if you intend to permit that local service. |

## DNS and Lower-Layer Caveat

DNS rebinding is still not fully defended. Sustained DNS hijack or malicious DNS responses at policy-resolution time remain in scope. If hostile DNS is in scope, enforce egress at a lower layer such as firewall, VPC, corporate proxy, endpoint policy, or equivalent network controls.

The starter allowlist printer is not a DNS proxy and does not authenticate DNS answers.

## Safety Boundaries

- No silent default allowlist.
- No automatic network permission expansion.
- No config mutation.
- No `install-defaults` behavior.
- No sandbox-by-default behavior.
- No separate proxy-enable flag.
- UDP and QUIC are unsupported in this sandbox version.
