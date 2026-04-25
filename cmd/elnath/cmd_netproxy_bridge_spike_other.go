//go:build !linux

package main

import (
	"context"
	"fmt"
)

// cmdNetproxyBridgeSpike is the non-Linux stub. The spike substrate
// (bwrap + netns + UDS bind) is Linux-only, so this build target
// errors out cleanly rather than fabricating partial behavior. The
// subcommand exists on every platform purely so help output, command
// dispatch, and tab-completion remain consistent across builds; the
// real implementation lives in cmd_netproxy_bridge_spike_linux.go.
func cmdNetproxyBridgeSpike(_ context.Context, _ []string) error {
	return fmt.Errorf("netproxy-bridge-spike is linux-only (requires bwrap)")
}
