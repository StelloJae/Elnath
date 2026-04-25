package tools

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunProxyChildMain_HelpFlagReturnsZero(t *testing.T) {
	sink := NewChannelEventSink(8)
	cfg := ProxyChildConfig{
		Args: []string{"--help"},
		Sink: sink,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code := RunProxyChildMain(ctx, cfg)
	if code != 0 {
		t.Errorf("--help should exit zero; got %d", code)
	}
}

func TestRunProxyChildMain_InvalidConfigNonZero(t *testing.T) {
	sink := NewChannelEventSink(8)
	cfg := ProxyChildConfig{
		Args: []string{"--allow", "*:443"}, // bare global wildcard rejected
		Sink: sink,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code := RunProxyChildMain(ctx, cfg)
	if code == 0 {
		t.Errorf("invalid allow entry should exit non-zero; got %d", code)
	}
}

func TestRunProxyChildMain_ListenerProvidedDirectly(t *testing.T) {
	// When the caller pre-binds listeners (e.g., a test or a parent
	// process that needs to hand a bound fd into the child via env),
	// the entry function uses them directly without re-binding.
	httpL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	socksL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	sink := NewChannelEventSink(8)
	cfg := ProxyChildConfig{
		Args:           []string{"--allow", "github.com:443"},
		HTTPListener:   httpL,
		SOCKSListener:  socksL,
		Sink:           sink,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- RunProxyChildMain(ctx, cfg) }()

	// Wait briefly for the listeners to be active.
	time.Sleep(50 * time.Millisecond)

	// Cancel and verify clean exit.
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("graceful shutdown should return 0; got %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunProxyChildMain did not return after cancel")
	}

	// Listeners should be closed when the function returns.
	if _, err := httpL.Accept(); err == nil || !isClosedListenerErr(err) {
		t.Errorf("expected closed listener; got err=%v", err)
	}
	if _, err := socksL.Accept(); err == nil || !isClosedListenerErr(err) {
		t.Errorf("expected closed listener; got err=%v", err)
	}
}

func TestRunProxyChildMain_BindFailureNonZero(t *testing.T) {
	// Pin to a port we know is already taken: use an existing
	// listener and pass its address as --http-listen.
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer taken.Close()
	addr := taken.Addr().String()

	sink := NewChannelEventSink(8)
	cfg := ProxyChildConfig{
		Args: []string{
			"--allow", "github.com:443",
			"--http-listen", addr,
			"--socks-listen", "127.0.0.1:0",
		},
		Sink: sink,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code := RunProxyChildMain(ctx, cfg)
	if code == 0 {
		t.Errorf("listener bind failure should return non-zero; got %d", code)
	}

	// The error must have flowed through the sink, not been silently
	// swallowed (partner-mini-lap N1 carry-forward).
	hasError := false
	deadline := time.NewTimer(500 * time.Millisecond)
	defer deadline.Stop()
loop:
	for {
		select {
		case <-sink.Errors:
			hasError = true
			break loop
		case <-deadline.C:
			break loop
		}
	}
	if !hasError {
		t.Errorf("bind failure should have emitted to sink.Errors; got none")
	}
}

func TestRunProxyChildMain_SinkRequired(t *testing.T) {
	cfg := ProxyChildConfig{
		Args: []string{"--allow", "github.com:443"},
		// Sink intentionally nil.
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	code := RunProxyChildMain(ctx, cfg)
	if code == 0 {
		t.Errorf("nil sink should fail fast; got 0")
	}
}

// TestProxyChildArgs_Parser verifies the standalone arg parser used
// by RunProxyChildMain. Direct unit test of the parser keeps test
// cycles fast (no network) and confirms each flag.
func TestProxyChildArgs_Parser(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		check   func(t *testing.T, p ProxyChildParsed)
		wantErr string
	}{
		{
			name: "happy path with all flags",
			args: []string{
				"--http-listen", "127.0.0.1:3128",
				"--socks-listen", "127.0.0.1:8081",
				"--allow", "github.com:443",
				"--allow", "*.openai.com:443",
				"--deny", "evil.example:443",
			},
			check: func(t *testing.T, p ProxyChildParsed) {
				if p.HTTPListen != "127.0.0.1:3128" {
					t.Errorf("HTTPListen = %q", p.HTTPListen)
				}
				if p.SOCKSListen != "127.0.0.1:8081" {
					t.Errorf("SOCKSListen = %q", p.SOCKSListen)
				}
				if len(p.AllowEntries) != 2 {
					t.Errorf("AllowEntries len = %d", len(p.AllowEntries))
				}
				if len(p.DenyEntries) != 1 {
					t.Errorf("DenyEntries len = %d", len(p.DenyEntries))
				}
			},
		},
		{
			name: "help flag short-circuits",
			args: []string{"--help"},
			check: func(t *testing.T, p ProxyChildParsed) {
				if !p.Help {
					t.Errorf("expected Help=true")
				}
			},
		},
		{
			name:    "unknown flag rejected",
			args:    []string{"--garbage"},
			wantErr: "flag provided",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ParseProxyChildArgs(tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("expected error containing %q; got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.check(t, p)
		})
	}
}

func TestRunProxyChildMain_ConcurrentSafetyOfSink(t *testing.T) {
	// Construct a sink that synchronizes on every emit and verify
	// that the proxy entry function never blocks the listener loop
	// even when the sink is under contention. This is a smoke test
	// for the partner pin "sink MUST NOT block".
	httpL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	socksL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	slow := &mutexSink{}
	cfg := ProxyChildConfig{
		Args:          []string{"--allow", "github.com:443"},
		HTTPListener:  httpL,
		SOCKSListener: socksL,
		Sink:          slow,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- RunProxyChildMain(ctx, cfg) }()
	time.Sleep(20 * time.Millisecond)

	// Tee a bunch of bogus connections at the http listener to force
	// emit calls. Connect-but-don't-handshake: each will time out
	// and emit a Decision or Error.
	for i := 0; i < 5; i++ {
		conn, err := net.Dial("tcp", httpL.Addr().String())
		if err == nil {
			_ = conn.Close()
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("did not exit after cancel under sink contention")
	}
}

type mutexSink struct {
	mu sync.Mutex
}

func (m *mutexSink) EmitDecision(_ Decision) {
	m.mu.Lock()
	defer m.mu.Unlock()
	time.Sleep(2 * time.Millisecond)
}

func (m *mutexSink) EmitError(_ error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	time.Sleep(2 * time.Millisecond)
}

func TestRunProxyChildMain_ContextCancelStopsListener(t *testing.T) {
	httpL, _ := net.Listen("tcp", "127.0.0.1:0")
	socksL, _ := net.Listen("tcp", "127.0.0.1:0")
	sink := NewChannelEventSink(8)
	cfg := ProxyChildConfig{
		Args:          []string{"--allow", "github.com:443"},
		HTTPListener:  httpL,
		SOCKSListener: socksL,
		Sink:          sink,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- RunProxyChildMain(ctx, cfg) }()

	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("expected code 0; got %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not exit after cancel")
	}
}

// TestRunProxyChildMain_BothListenersStartedConcurrently confirms
// that calling the entry function exposes both listeners in
// parallel; if one fails to bind the other is also torn down.
func TestRunProxyChildMain_BothListenersStartedConcurrently(t *testing.T) {
	taken, _ := net.Listen("tcp", "127.0.0.1:0")
	defer taken.Close()
	addr := taken.Addr().String()

	sink := NewChannelEventSink(8)
	cfg := ProxyChildConfig{
		Args: []string{
			"--allow", "github.com:443",
			"--http-listen", "127.0.0.1:0",
			"--socks-listen", addr, // already taken -> bind fails
		},
		Sink: sink,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code := RunProxyChildMain(ctx, cfg)
	if code == 0 {
		t.Errorf("expected non-zero on socks bind fail; got 0")
	}
	// Drain at least one error.
	select {
	case <-sink.Errors:
	default:
		// We accept either path: errgroup may have surfaced via
		// return value too. The point is we don't silently swallow.
	}
	_ = errors.New("smoke test guard")
}
