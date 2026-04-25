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
	if p.PolicyName != "seatbelt-fs" {
		t.Errorf("probe.PolicyName = %q, want %q", p.PolicyName, "seatbelt-fs")
	}
	if p.ExecutionMode != "macos_seatbelt_fs" {
		t.Errorf("probe.ExecutionMode = %q, want %q", p.ExecutionMode, "macos_seatbelt_fs")
	}

	// Label discipline: B3b-2 is filesystem-only. SandboxEnforced and
	// NetworkEnforced MUST stay false until B3b-2.5 wires network policy.
	if p.SandboxEnforced {
		t.Error("SandboxEnforced must be false in B3b-2 (filesystem-only prototype)")
	}
	if p.NetworkEnforced {
		t.Error("NetworkEnforced must be false in B3b-2 (network policy is B3b-2.5)")
	}

	if runtime.GOOS == "darwin" {
		if !p.Available {
			t.Errorf("expected Available=true on darwin, got %+v", p)
		}
		if !p.FilesystemEnforced {
			t.Error("FilesystemEnforced must be true on darwin (Seatbelt FS profile)")
		}
		if p.Message == "" {
			t.Error("Probe message should describe the runner")
		}
	} else {
		if p.Available {
			t.Errorf("expected Available=false on %s, got %+v", runtime.GOOS, p)
		}
		// Off-darwin the substrate cannot enforce anything.
		if p.FilesystemEnforced {
			t.Error("FilesystemEnforced must be false off darwin")
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

func TestSeatbeltRunner_NeverClaimsSandboxEnforcedOnPartialEnforcement(t *testing.T) {
	// This is the label-discipline regression test: B3b-2 ships
	// filesystem isolation only. SandboxEnforced is reserved for the
	// configuration where filesystem AND network are both enforced.
	// Until B3b-2.5 ships, the probe must NEVER report SandboxEnforced=true.
	r := NewSeatbeltRunner()
	p := r.Probe(context.Background())
	if p.SandboxEnforced {
		t.Fatal("SeatbeltRunner B3b-2 must not report SandboxEnforced=true (network not yet enforced)")
	}
}
