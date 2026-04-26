//go:build !linux

package main

import (
	"context"
	"fmt"
)

// cmdNetproxyBridge is the non-Linux stub. The production bridge
// substrate (bwrap + netns + UDS) is Linux-only, so this build target
// errors out cleanly rather than fabricating partial behavior. The
// subcommand exists on every platform so help output, command
// dispatch, and tab-completion remain consistent across builds; the
// real implementation lives in cmd_netproxy_bridge_linux.go.
//
// Distinct from cmdNetproxyBridgeSpike which is the v41 / B3b-4-S0
// spike. This is the production wiring used by BwrapRunner under
// B3b-4-3.
func cmdNetproxyBridge(_ context.Context, _ []string) error {
	return fmt.Errorf("netproxy-bridge is linux-only (requires bwrap)")
}
