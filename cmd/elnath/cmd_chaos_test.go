package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/fault"
)

func newTestChaosRuntime() *chaosRuntime {
	return &chaosRuntime{
		cfg:    config.DefaultConfig(),
		out:    &bytes.Buffer{},
		errOut: &bytes.Buffer{},
	}
}

func TestRunChaosHelp(t *testing.T) {
	rt := newTestChaosRuntime()
	if err := runChaos(rt, nil); err != nil {
		t.Fatalf("runChaos() error = %v", err)
	}
	if rt.out.(*bytes.Buffer).Len() == 0 {
		t.Fatal("help output is empty")
	}
}

func TestRunChaosList(t *testing.T) {
	rt := newTestChaosRuntime()
	if err := runChaos(rt, []string{"list"}); err != nil {
		t.Fatalf("runChaos(list) error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(rt.out.(*bytes.Buffer).String()), "\n")
	if len(lines) < 10 {
		t.Fatalf("list line count = %d, want >= 10", len(lines))
	}
}

func TestRunChaosUnknownSubcommand(t *testing.T) {
	rt := newTestChaosRuntime()
	if err := runChaos(rt, []string{"unknown"}); err == nil {
		t.Fatal("runChaos(unknown) error = nil, want error")
	}
}

func TestRunChaosUnknownScenario(t *testing.T) {
	rt := newTestChaosRuntime()
	if err := runChaos(rt, []string{"run", "nonexistent-scenario"}); err == nil {
		t.Fatal("runChaos(run nonexistent-scenario) error = nil, want error")
	}
}

func TestRunChaosRequiresGuardPass(t *testing.T) {
	t.Setenv("ELNATH_FAULT_PROFILE", "tool-bash-transient-fail")
	rt := newTestChaosRuntime()
	if err := runChaos(rt, []string{"run", "tool-bash-transient-fail"}); err == nil {
		t.Fatal("runChaos() error = nil, want guard failure")
	}
}

func TestRunChaosReportLatest(t *testing.T) {
	rt := newTestChaosRuntime()
	baseDir := t.TempDir()
	rt.cfg.FaultInjection.OutputDir = baseDir
	runDir := filepath.Join(baseDir, "00000000-0000-4000-8000-000000000000")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	report := []byte("# Fault Injection Report\nPASS\n")
	if err := os.WriteFile(filepath.Join(runDir, "report.md"), report, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := runChaos(rt, []string{"report", "latest"}); err != nil {
		t.Fatalf("runChaos(report latest) error = %v", err)
	}
	if !strings.Contains(rt.out.(*bytes.Buffer).String(), "PASS") {
		t.Fatalf("output = %q, want PASS", rt.out.(*bytes.Buffer).String())
	}
}

func TestRunChaosReportRegeneratesMarkdownFromJSONL(t *testing.T) {
	rt := newTestChaosRuntime()
	baseDir := t.TempDir()
	rt.cfg.FaultInjection.OutputDir = baseDir
	runDir := filepath.Join(baseDir, "00000000-0000-4000-8000-000000000000")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	rec := fault.RunRecord{Timestamp: time.Now().UTC(), Scenario: "tool-bash-transient-fail", FaultType: fault.FaultTransientError, RunID: "00000000-0000-4000-8000-000000000000", Outcome: "pass"}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "runs.jsonl"), append(data, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := runChaos(rt, []string{"report", "00000000-0000-4000-8000-000000000000"}); err != nil {
		t.Fatalf("runChaos(report) error = %v", err)
	}
	if !strings.Contains(rt.out.(*bytes.Buffer).String(), "PASS") {
		t.Fatalf("output = %q, want PASS", rt.out.(*bytes.Buffer).String())
	}
}

func TestChaosBaseDirUsesConfigDataDir(t *testing.T) {
	rt := newTestChaosRuntime()
	rt.cfg.DataDir = "/tmp/custom-data"
	if got := chaosBaseDir(rt.cfg, ""); got != "/tmp/custom-data/fault" {
		t.Fatalf("chaosBaseDir() = %q, want /tmp/custom-data/fault", got)
	}
}
