package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// Cross-platform SeatbeltRunner tests. Runtime tests live in
// bash_runner_seatbelt_darwin_test.go behind a build tag because they
// need actual sandbox-exec invocation. Tests in this file verify the
// contract surface (Name, Close, Probe shape, factory wiring, label
// discipline) and run on every platform.

func TestSeatbeltRunner_NameAndCloseAndInterfaceCompliance(t *testing.T) {
	r := NewSeatbeltRunner()
	if r.Name() != "seatbelt" {
		t.Errorf("Name = %q, want %q", r.Name(), "seatbelt")
	}
	if err := r.Close(context.Background()); err != nil {
		t.Errorf("Close should be no-op, got %v", err)
	}
	// Compile-time assertion: the runner satisfies BashRunner on every
	// platform, even when the impl is the non-darwin stub.
	var _ BashRunner = (*SeatbeltRunner)(nil)
}

func TestSeatbeltRunner_ProbeShape(t *testing.T) {
	r := NewSeatbeltRunner()
	p := r.Probe(context.Background())

	if p.Name != "seatbelt" {
		t.Errorf("probe.Name = %q, want %q", p.Name, "seatbelt")
	}
	if p.Platform != runtime.GOOS {
		t.Errorf("probe.Platform = %q, want %q", p.Platform, runtime.GOOS)
	}
	if p.PolicyName != "seatbelt" {
		t.Errorf("probe.PolicyName = %q, want %q", p.PolicyName, "seatbelt")
	}
	if p.ExecutionMode != "macos_seatbelt" {
		t.Errorf("probe.ExecutionMode = %q, want %q", p.ExecutionMode, "macos_seatbelt")
	}

	if runtime.GOOS == "darwin" {
		if !p.Available {
			t.Errorf("expected Available=true on darwin, got %+v", p)
		}
		if !p.FilesystemEnforced {
			t.Error("FilesystemEnforced must be true on darwin (Seatbelt FS profile)")
		}
		if !p.NetworkEnforced {
			t.Error("NetworkEnforced must be true after B3b-2.5 (default-deny + IP:port allowlist)")
		}
		if !p.SandboxEnforced {
			t.Error("SandboxEnforced must be true after B3b-2.5 (FS+Net both enforced)")
		}
		if p.Message == "" {
			t.Error("Probe message should describe the runner")
		}
	} else {
		if p.Available {
			t.Errorf("expected Available=false on %s, got %+v", runtime.GOOS, p)
		}
		// Off-darwin the substrate cannot enforce anything.
		if p.FilesystemEnforced || p.NetworkEnforced || p.SandboxEnforced {
			t.Error("non-darwin stub must report all enforcement flags false")
		}
		if !strings.Contains(p.Message, "darwin") {
			t.Errorf("probe message should name darwin requirement, got %q", p.Message)
		}
	}
}

func TestSeatbeltRunner_OffDarwinRunReturnsUnsupported(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("test exercises the non-darwin stub path")
	}
	r := NewSeatbeltRunner()
	res, err := r.Run(context.Background(), BashRunRequest{
		Command:    "echo hi",
		WorkDir:    "/tmp",
		SessionDir: "/tmp",
		DisplayCWD: ".",
	})
	if err != nil {
		t.Fatalf("Run should not return Go-level error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError on unsupported platform")
	}
	if res.Classification != "sandbox_setup_failed" {
		t.Errorf("expected sandbox_setup_failed classification, got %q", res.Classification)
	}
}

func TestSeatbeltRunner_LabelDisciplineFollowsEnforcement(t *testing.T) {
	// SandboxEnforced is reserved for the configuration where BOTH
	// filesystem AND network policies are actually enforced. This
	// regression test locks the invariant: the probe's
	// SandboxEnforced flag must equal FilesystemEnforced &&
	// NetworkEnforced — partial enforcement (e.g., a future profile
	// that opens network) must immediately flip the SandboxEnforced
	// label back to false rather than drift silently.
	r := NewSeatbeltRunner()
	p := r.Probe(context.Background())
	want := p.FilesystemEnforced && p.NetworkEnforced
	if p.SandboxEnforced != want {
		t.Fatalf("SandboxEnforced=%v but FilesystemEnforced=%v && NetworkEnforced=%v (want %v)",
			p.SandboxEnforced, p.FilesystemEnforced, p.NetworkEnforced, want)
	}
}

func TestNewSeatbeltRunnerWithAllowlist_ValidatesEntries(t *testing.T) {
	cases := []struct {
		name      string
		allowlist []string
		wantErr   bool
	}{
		{"empty", nil, false},
		{"valid ipv4", []string{"127.0.0.1:8080"}, false},
		{"valid ipv6", []string{"[::1]:8080"}, false},
		{"multiple valid loopback", []string{"127.0.0.1:80", "[::1]:443"}, false},
		{"non-loopback rejected", []string{"10.0.0.1:443"}, true},
		{"domain rejected", []string{"github.com:443"}, true},
		{"missing port", []string{"127.0.0.1"}, true},
		{"port zero rejected", []string{"127.0.0.1:0"}, true},
		{"port out of range", []string{"127.0.0.1:99999"}, true},
		{"empty entry", []string{""}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewSeatbeltRunnerWithAllowlist(tc.allowlist)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for %v", tc.allowlist)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for %v: %v", tc.allowlist, err)
			}
		})
	}
}
