package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// B3b-4-2 Phase A: production `elnath netproxy ...` subcommand handler.
// Cross-platform (no build tag). The handler is a thin wrapper around
// tools.RunProxyChildMain that wires stderr to os.Stderr so --help text
// reaches the operator. Direct unit test of the handler covers the
// happy path (--help) and the bad-args path (parser failure surfaces a
// non-nil error).

func TestCmdNetproxy_HelpFlagPrintsUsageAndReturnsNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, stderr := captureOutput(t, func() {
		if err := cmdNetproxy(ctx, []string{"--help"}); err != nil {
			t.Fatalf("cmdNetproxy(--help): %v", err)
		}
	})
	if !strings.Contains(stderr, "usage: elnath netproxy") {
		t.Errorf("stderr missing usage line; got: %q", stderr)
	}
}

func TestCmdNetproxy_NoListenersOrInvalidConfigReturnsError(t *testing.T) {
	// Without --http-listen / --socks-listen / pre-bound listeners
	// (cross-process invocation has no in-Go listener handles), the
	// child must refuse to start and surface a non-nil error.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := cmdNetproxy(ctx, []string{"--allow", "github.com:443"})
	if err == nil {
		t.Errorf("expected error when no listeners specified")
	}
}

func TestCmdNetproxy_BindFailureReturnsError(t *testing.T) {
	// Use an unparseable address so the child's listener bind step
	// fails. The handler must return a non-nil error.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := cmdNetproxy(ctx, []string{
		"--http-listen", "not-a-real-host:99999",
		"--allow", "github.com:443",
	})
	if err == nil {
		t.Errorf("expected error on bind failure")
	}
}
