package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSystemResolver_LookupLocalhost(t *testing.T) {
	r := NewSystemResolver()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ips, err := r.LookupHost(ctx, "localhost")
	if err != nil {
		t.Fatalf("LookupHost(localhost): %v", err)
	}
	if len(ips) == 0 {
		t.Fatalf("expected at least one IP for localhost; got none")
	}
	// localhost should resolve to a loopback address. The exact
	// representation varies (127.0.0.1, ::1, depending on DNS
	// configuration) so we just check that *something* came back.
}

func TestMockResolver_ReturnsCannedIPs(t *testing.T) {
	r := NewMockResolver(map[string][]string{
		"github.com":     {"140.82.112.5", "140.82.114.5"},
		"api.github.com": {"140.82.112.6"},
	})
	ips, err := r.LookupHost(context.Background(), "github.com")
	if err != nil {
		t.Fatalf("LookupHost: %v", err)
	}
	if len(ips) != 2 {
		t.Errorf("expected 2 IPs; got %d", len(ips))
	}
	if ips[0] != "140.82.112.5" || ips[1] != "140.82.114.5" {
		t.Errorf("unexpected IPs: %v", ips)
	}
}

func TestMockResolver_UnknownHostReturnsError(t *testing.T) {
	r := NewMockResolver(map[string][]string{"github.com": {"1.2.3.4"}})
	_, err := r.LookupHost(context.Background(), "unknown.example.com")
	if err == nil {
		t.Fatalf("expected error for unknown host")
	}
}

func TestMockResolver_PreconfiguredErrorReturned(t *testing.T) {
	wantErr := errors.New("simulated dns failure")
	r := MockResolver{Err: wantErr}
	_, err := r.LookupHost(context.Background(), "github.com")
	if !errors.Is(err, wantErr) {
		t.Errorf("expected %v; got %v", wantErr, err)
	}
}

func TestMockResolver_NilEntryReturnsEmptyNotError(t *testing.T) {
	// Returning an empty slice (rather than error) lets tests model
	// "host had no A or AAAA records" cleanly.
	r := NewMockResolver(map[string][]string{"empty.example.com": {}})
	ips, err := r.LookupHost(context.Background(), "empty.example.com")
	if err != nil {
		t.Fatalf("expected nil error; got %v", err)
	}
	if len(ips) != 0 {
		t.Errorf("expected empty slice; got %v", ips)
	}
}

func TestSystemResolver_HonorsContextCancellation(t *testing.T) {
	r := NewSystemResolver()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.LookupHost(ctx, "this-domain-should-trigger-network.invalid")
	if err == nil {
		t.Errorf("expected error from canceled context")
	}
}

// Sanity: SystemResolver and MockResolver both satisfy Resolver.
func TestResolverInterface(t *testing.T) {
	var _ Resolver = NewSystemResolver()
	var _ Resolver = NewMockResolver(nil)
	var _ Resolver = MockResolver{}
}
