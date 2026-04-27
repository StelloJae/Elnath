package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// Cross-platform BwrapRunner tests. Linux runtime tests live in
// bash_runner_bwrap_linux_test.go behind a build tag; this file
// verifies the contract surface (Name, Close, Probe shape, factory
// wiring, label discipline) on every platform.

func TestBwrapRunner_NameAndCloseAndInterfaceCompliance(t *testing.T) {
	r := NewBwrapRunner()
	if r.Name() != "bwrap" {
		t.Errorf("Name = %q, want %q", r.Name(), "bwrap")
	}
	if err := r.Close(context.Background()); err != nil {
		t.Errorf("Close should be no-op, got %v", err)
	}
	var _ BashRunner = (*BwrapRunner)(nil)
}

func TestBwrapRunner_ProbeShape(t *testing.T) {
	r := NewBwrapRunner()
	p := r.Probe(context.Background())

	if p.Name != "bwrap" {
		t.Errorf("probe.Name = %q, want %q", p.Name, "bwrap")
	}
	if p.Platform != runtime.GOOS {
		t.Errorf("probe.Platform = %q, want %q", p.Platform, runtime.GOOS)
	}
	if p.PolicyName != "bwrap" {
		t.Errorf("probe.PolicyName = %q, want %q", p.PolicyName, "bwrap")
	}
	if p.ExecutionMode != "linux_bwrap" {
		t.Errorf("probe.ExecutionMode = %q, want %q", p.ExecutionMode, "linux_bwrap")
	}

	if runtime.GOOS == "linux" {
		// On linux Available depends on whether bwrap is installed and
		// userns is functional. Either outcome is valid for this test;
		// runtime tests in the linux-tagged file exercise the
		// available-and-working path.
		if p.Available {
			if !p.FilesystemEnforced {
				t.Error("FilesystemEnforced must be true when bwrap is available")
			}
			if !p.NetworkEnforced {
				t.Error("NetworkEnforced must be true when bwrap is available (--unshare-net)")
			}
			if !p.SandboxEnforced {
				t.Error("SandboxEnforced must be true when bwrap is available (FS+Net both enforced)")
			}
		} else {
			if p.FilesystemEnforced || p.NetworkEnforced || p.SandboxEnforced {
				t.Error("unavailable bwrap must not claim enforcement")
			}
			if p.Message == "" {
				t.Error("unavailable probe must surface a diagnostic message")
			}
		}
	} else {
		if p.Available {
			t.Errorf("expected Available=false on %s", runtime.GOOS)
		}
		if p.FilesystemEnforced || p.NetworkEnforced || p.SandboxEnforced {
			t.Error("non-linux stub must report all enforcement flags false")
		}
		if !strings.Contains(p.Message, "linux") {
			t.Errorf("probe message should name linux requirement, got %q", p.Message)
		}
	}
}

func TestBwrapRunner_OffLinuxRunReturnsUnsupported(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("test exercises the non-linux stub path")
	}
	r := NewBwrapRunner()
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

func TestNewBashRunnerForConfig_BwrapOnCurrentPlatform(t *testing.T) {
	runner, err := NewBashRunnerForConfig(SandboxConfig{Mode: "bwrap"})
	if runtime.GOOS == "linux" {
		// Linux factory may succeed (bwrap installed) or fail-closed
		// (bwrap missing); either is correct provided there is no
		// silent fallback to DirectRunner.
		if err == nil {
			if runner == nil || runner.Name() != "bwrap" {
				t.Fatalf("expected BwrapRunner on linux, got %v", runner)
			}
			return
		}
		if runner != nil {
			t.Errorf("factory returned a runner alongside an error — silent fallback risk")
		}
		return
	}
	if err == nil {
		t.Fatalf("expected unsupported error on %s", runtime.GOOS)
	}
	if runner != nil {
		t.Errorf("expected nil runner on unsupported platform, got %v", runner)
	}
	if !strings.Contains(err.Error(), "linux") && !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("expected platform-specific error, got: %v", err)
	}
}

func TestNewBashRunnerForConfig_BwrapRejectsNetworkAllowlist(t *testing.T) {
	// Bwrap still rejects loopback-only allowlists because it has no
	// SBPL-equivalent loopback rule and must not silently broaden local
	// access. Domain/non-loopback proxy-required entries are covered by
	// linux proxy factory tests.
	_, err := NewBashRunnerForConfig(SandboxConfig{
		Mode:             "bwrap",
		NetworkAllowlist: []string{"127.0.0.1:8080"},
	})
	if err == nil {
		t.Fatalf("expected factory error when bwrap mode is given a loopback allowlist")
	}
	if !strings.Contains(err.Error(), "loopback") && !strings.Contains(err.Error(), "127.0.0.1:8080") {
		t.Errorf("expected loopback rejection message, got: %v", err)
	}
}

func TestBwrapRunner_LabelDisciplineFollowsEnforcement(t *testing.T) {
	r := NewBwrapRunner()
	p := r.Probe(context.Background())
	want := p.FilesystemEnforced && p.NetworkEnforced
	if p.SandboxEnforced != want {
		t.Fatalf("SandboxEnforced=%v but FilesystemEnforced=%v && NetworkEnforced=%v (want %v)",
			p.SandboxEnforced, p.FilesystemEnforced, p.NetworkEnforced, want)
	}
}
