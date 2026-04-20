package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agent/reflection"
	"github.com/stello/elnath/internal/config"
)

func TestPrintSelfHealStatus_Empty(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		SelfHealing: config.SelfHealingConfig{
			Enabled: true,
			Path:    filepath.Join(dir, "self_heal_attempts.jsonl"),
		},
	}
	stdout, _ := captureOutput(t, func() {
		if err := printSelfHealStatus(cfg); err != nil {
			t.Fatalf("printSelfHealStatus: %v", err)
		}
	})
	if !strings.Contains(stdout, "Self-Heal Observations (Phase 0)") {
		t.Fatalf("missing header:\n%s", stdout)
	}
	if !strings.Contains(stdout, "total attempts:         0") {
		t.Fatalf("missing zero-count line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "(no observations recorded yet)") {
		t.Fatalf("missing empty-state hint:\n%s", stdout)
	}
	if !strings.Contains(stdout, "status:                 enabled") {
		t.Fatalf("missing status line:\n%s", stdout)
	}
}

func TestPrintSelfHealStatus_Populated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "self_heal_attempts.jsonl")
	store := reflection.NewFileStore(path)

	reps := []reflection.Report{
		{Fingerprint: "A", FinishReason: "error", ErrorCategory: "server_error", SuggestedStrategy: reflection.StrategyRetrySmallerScope},
		{Fingerprint: "B", FinishReason: "budget_exceeded", ErrorCategory: "timeout", SuggestedStrategy: reflection.StrategyCompressContext},
		{Fingerprint: "C", FinishReason: "ack_loop", ErrorCategory: "unknown", SuggestedStrategy: reflection.StrategyUnknown},
	}
	for i, r := range reps {
		meta := reflection.StoreMeta{
			TS:        time.Date(2026, 4, 20, 17+i, 0, 0, 0, time.UTC),
			SessionID: "sess",
			Principal: "jay@workstation",
			ProjectID: "elnath",
		}
		if err := store.Append(context.Background(), r, meta); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	cfg := &config.Config{
		SelfHealing: config.SelfHealingConfig{Enabled: true, Path: path},
	}
	stdout, _ := captureOutput(t, func() {
		if err := printSelfHealStatus(cfg); err != nil {
			t.Fatalf("printSelfHealStatus: %v", err)
		}
	})
	if !strings.Contains(stdout, "total attempts:         3") {
		t.Fatalf("total count missing:\n%s", stdout)
	}
	if !strings.Contains(stdout, "error=1") || !strings.Contains(stdout, "budget_exceeded=1") || !strings.Contains(stdout, "ack_loop=1") {
		t.Fatalf("finish_reason breakdown missing:\n%s", stdout)
	}
	if !strings.Contains(stdout, "retry_smaller_scope=1") ||
		!strings.Contains(stdout, "compress_context=1") ||
		!strings.Contains(stdout, "unknown=1") {
		t.Fatalf("strategy breakdown missing:\n%s", stdout)
	}
	if !strings.Contains(stdout, "schema fail rate:       1/3 = 33.3%") {
		t.Fatalf("schema fail rate missing:\n%s", stdout)
	}
	if !strings.Contains(stdout, "sample window:") {
		t.Fatalf("sample window missing:\n%s", stdout)
	}
}

func TestPrintSelfHealStatus_Disabled(t *testing.T) {
	cfg := &config.Config{SelfHealing: config.SelfHealingConfig{Enabled: false}}
	stdout, _ := captureOutput(t, func() {
		if err := printSelfHealStatus(cfg); err != nil {
			t.Fatalf("printSelfHealStatus: %v", err)
		}
	})
	if !strings.Contains(stdout, "status:                 disabled") {
		t.Fatalf("disabled status line missing:\n%s", stdout)
	}
}
