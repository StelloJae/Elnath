package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/learning"
	basetools "github.com/stello/elnath/internal/tools"
)

func TestCmdExplainTimeoutsJSON(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.yaml")
	dataDir := filepath.Join(root, "data")
	wikiDir := filepath.Join(root, "wiki")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(data): %v", err)
	}
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(wiki): %v", err)
	}
	raw := "data_dir: " + dataDir + "\n" +
		"wiki_dir: " + wikiDir + "\n" +
		"locale: en\n" +
		"anthropic:\n  timeout_seconds: 90\n" +
		"openai:\n  timeout_seconds: 45\n" +
		"openai_responses:\n  timeout_seconds: 77\n" +
		"daemon:\n" +
		"  inactivity_timeout_seconds: 12\n" +
		"  wall_clock_timeout_seconds: 34\n" +
		"  max_recoveries: 5\n" +
		"  workspace_retention: keep\n" +
		"self_healing:\n" +
		"  enabled: true\n" +
		"  observe_only: false\n" +
		"  timeout_seconds: 17\n" +
		"  completion_retry_max: 1\n" +
		"telegram:\n  poll_timeout_seconds: 11\n"
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdExplain(context.Background(), []string{"timeouts", "--json"}); err != nil {
			t.Fatalf("cmdExplain(timeouts --json): %v", err)
		}
	})

	var out struct {
		ProviderRequestTimeouts []struct {
			Provider       string `json:"provider"`
			ConfigKey      string `json:"config_key"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		} `json:"provider_request_timeouts"`
		Daemon struct {
			InactivityTimeoutSeconds int    `json:"inactivity_timeout_seconds"`
			WallClockTimeoutSeconds  int    `json:"wall_clock_timeout_seconds"`
			MaxRecoveries            int    `json:"max_recoveries"`
			WorkspaceRetention       string `json:"workspace_retention"`
		} `json:"daemon"`
		SelfHealing struct {
			Enabled                                    bool     `json:"enabled"`
			ObserveOnly                                bool     `json:"observe_only"`
			TimeoutSeconds                             int      `json:"timeout_seconds"`
			CompletionRetryMax                         int      `json:"completion_retry_max"`
			CompletionRetrySupportedMax                int      `json:"completion_retry_supported_max"`
			CompletionRetryDecisions                   []string `json:"completion_retry_decisions"`
			VerificationRetryRequiresStandaloneCommand bool     `json:"verification_retry_requires_standalone_command"`
			VerificationRetryInfersCommandFromProse    bool     `json:"verification_retry_infers_command_from_prose"`
		} `json:"self_healing"`
		Telegram struct {
			PollTimeoutSeconds int `json:"poll_timeout_seconds"`
		} `json:"telegram"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}

	seen := map[string]int{}
	for _, entry := range out.ProviderRequestTimeouts {
		seen[entry.Provider] = entry.TimeoutSeconds
		if entry.ConfigKey == "" {
			t.Fatalf("provider timeout entry missing config_key: %+v", entry)
		}
	}
	if seen["anthropic"] != 90 || seen["openai"] != 45 || seen["openai_responses"] != 77 || seen["codex_oauth"] != 77 {
		t.Fatalf("provider timeouts = %+v, want anthropic/openai/openai_responses/codex_oauth values", seen)
	}
	if out.Daemon.InactivityTimeoutSeconds != 12 || out.Daemon.WallClockTimeoutSeconds != 34 || out.Daemon.MaxRecoveries != 5 || out.Daemon.WorkspaceRetention != "keep" {
		t.Fatalf("daemon policy = %+v, want configured daemon timeout policy", out.Daemon)
	}
	if !out.SelfHealing.Enabled || out.SelfHealing.ObserveOnly || out.SelfHealing.TimeoutSeconds != 17 || out.SelfHealing.CompletionRetryMax != 1 {
		t.Fatalf("self_healing policy = %+v, want configured self-healing timeout policy", out.SelfHealing)
	}
	if out.SelfHealing.CompletionRetrySupportedMax != maxCompletionRetryAttempts {
		t.Fatalf("completion_retry_supported_max = %d, want %d", out.SelfHealing.CompletionRetrySupportedMax, maxCompletionRetryAttempts)
	}
	wantDecisions := []string{completionRetryDecisionRetrySmallerScope, completionRetryDecisionRunVerification}
	if !reflect.DeepEqual(out.SelfHealing.CompletionRetryDecisions, wantDecisions) {
		t.Fatalf("completion_retry_decisions = %v, want %v", out.SelfHealing.CompletionRetryDecisions, wantDecisions)
	}
	if !out.SelfHealing.VerificationRetryRequiresStandaloneCommand || out.SelfHealing.VerificationRetryInfersCommandFromProse {
		t.Fatalf("self_healing verification retry policy = %+v, want standalone-only without prose inference", out.SelfHealing)
	}
	if out.Telegram.PollTimeoutSeconds != 11 {
		t.Fatalf("telegram poll timeout = %d, want 11", out.Telegram.PollTimeoutSeconds)
	}
}

func TestCmdExplainTimeoutsTextShowsCorrectionPolicy(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.SelfHealing.Enabled = true
	cfg.SelfHealing.ObserveOnly = false
	cfg.SelfHealing.CompletionRetryMax = 2

	stdout, _ := captureOutput(t, func() {
		if err := explainTimeouts(cfg, nil); err != nil {
			t.Fatalf("explainTimeouts: %v", err)
		}
	})

	for _, want := range []string{
		"completion_retry_max=2",
		"supported_max=2",
		"decisions=retry_smaller_scope,run_verification",
		"verification_retry=standalone_command_only",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestExplainControlSurfacesJSON(t *testing.T) {
	stdout, _ := captureOutput(t, func() {
		if err := explainControlSurfaces([]string{"--json"}); err != nil {
			t.Fatalf("explainControlSurfaces(--json): %v", err)
		}
	})

	var out struct {
		Surfaces []struct {
			Name                   string   `json:"name"`
			Status                 string   `json:"status"`
			Tools                  []string `json:"tools"`
			ToolSearchDiscoverable bool     `json:"tool_search_discoverable"`
			ReceiptBacked          bool     `json:"receipt_backed"`
		} `json:"surfaces"`
		RemainingGaps []string `json:"remaining_gaps"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	byName := map[string]struct {
		status                 string
		toolSearchDiscoverable bool
		receiptBacked          bool
		toolCount              int
	}{}
	for _, surface := range out.Surfaces {
		byName[surface.Name] = struct {
			status                 string
			toolSearchDiscoverable bool
			receiptBacked          bool
			toolCount              int
		}{
			status:                 surface.Status,
			toolSearchDiscoverable: surface.ToolSearchDiscoverable,
			receiptBacked:          surface.ReceiptBacked,
			toolCount:              len(surface.Tools),
		}
	}
	for _, name := range []string{"task", "schedule", "plan", "worktree", "process", "skill", "command"} {
		entry, ok := byName[name]
		if !ok {
			t.Fatalf("missing control surface %q in %+v", name, byName)
		}
		if entry.status != "implemented" || !entry.toolSearchDiscoverable || !entry.receiptBacked || entry.toolCount == 0 {
			t.Fatalf("surface %s = %+v, want implemented ToolSearch-discoverable receipt-backed with tools", name, entry)
		}
	}
	userInput, ok := byName["user_input"]
	if !ok {
		t.Fatalf("missing control surface user_input in %+v", byName)
	}
	if userInput.status != "partial" || !userInput.toolSearchDiscoverable || !userInput.receiptBacked || userInput.toolCount != 3 {
		t.Fatalf("user_input surface = %+v, want partial ToolSearch-discoverable receipt-backed with three tools", userInput)
	}
	if len(out.RemainingGaps) == 0 {
		t.Fatal("remaining_gaps empty, want honest non-complete boundary")
	}
	for _, gap := range out.RemainingGaps {
		if strings.Contains(gap, "surface status is static") {
			t.Fatalf("remaining gap %q should be replaced after manifest-backed control-surface metadata", gap)
		}
	}
}

func TestControlSurfaceManifestMatchesToolSearchRouting(t *testing.T) {
	for _, surface := range controlSurfaceManifest() {
		if len(surface.Tools) == 0 {
			t.Fatalf("surface %s has no tools", surface.Name)
		}
		for _, name := range surface.Tools {
			routing := basetools.ToolRoutingMetadataForName(name)
			if routing.Category != surface.Name {
				t.Fatalf("tool %s routing category = %q, want surface %q", name, routing.Category, surface.Name)
			}
			if routing.Surface == "" {
				t.Fatalf("tool %s routing surface is empty", name)
			}
		}
	}
}

func TestExplainControlSurfacesText(t *testing.T) {
	stdout, _ := captureOutput(t, func() {
		if err := explainControlSurfaces(nil); err != nil {
			t.Fatalf("explainControlSurfaces: %v", err)
		}
	})

	for _, want := range []string{
		"Control surfaces:",
		"task: implemented",
		"user_input: partial",
		"worktree: implemented",
		"Remaining gaps:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestExplainPendingQuestionsJSON(t *testing.T) {
	store := learning.NewOutcomeStore(filepath.Join(t.TempDir(), "outcomes.jsonl"))
	askedAt := time.Date(2026, 5, 13, 7, 0, 0, 0, time.UTC)
	if err := store.Append(learning.OutcomeRecord{
		ProjectID: "elnath",
		Intent:    "complex_task",
		Workflow:  "single",
		Timestamp: askedAt,
		ControlToolReceipts: []learning.ControlToolReceipt{{
			Tool:      "ask_user_question",
			Action:    "request",
			RequestID: "req-1",
			SessionID: "sess-1",
			Question:  "Which branch?",
		}},
	}); err != nil {
		t.Fatalf("Append ask record: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := explainPendingQuestions(store, []string{"--json", "--session", "sess-1"}); err != nil {
			t.Fatalf("explainPendingQuestions: %v", err)
		}
	})

	var out struct {
		Count   int `json:"count"`
		Pending []struct {
			RequestID string `json:"request_id"`
			SessionID string `json:"session_id"`
			Question  string `json:"question"`
		} `json:"pending"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if out.Count != 1 || len(out.Pending) != 1 || out.Pending[0].RequestID != "req-1" || out.Pending[0].SessionID != "sess-1" || out.Pending[0].Question != "Which branch?" {
		t.Fatalf("pending output = %+v, want req-1", out)
	}
}

func TestExplainPendingQuestionsTextShowsNoPendingAfterAnswer(t *testing.T) {
	store := learning.NewOutcomeStore(filepath.Join(t.TempDir(), "outcomes.jsonl"))
	askedAt := time.Date(2026, 5, 13, 7, 0, 0, 0, time.UTC)
	answeredAt := askedAt.Add(time.Minute)
	if err := store.Append(learning.OutcomeRecord{
		ProjectID: "elnath",
		Intent:    "complex_task",
		Workflow:  "single",
		Timestamp: askedAt,
		ControlToolReceipts: []learning.ControlToolReceipt{{
			Tool:      "ask_user_question",
			Action:    "request",
			RequestID: "req-1",
			SessionID: "sess-1",
			Question:  "Which branch?",
		}},
	}); err != nil {
		t.Fatalf("Append ask record: %v", err)
	}
	if err := store.Append(learning.OutcomeRecord{
		ProjectID: "elnath",
		Intent:    "complex_task",
		Workflow:  "single",
		Timestamp: answeredAt,
		ControlToolReceipts: []learning.ControlToolReceipt{{
			Tool:      "user_question_answer",
			Action:    "answer",
			RequestID: "req-1",
			SessionID: "sess-1",
		}},
	}); err != nil {
		t.Fatalf("Append answer record: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := explainPendingQuestions(store, nil); err != nil {
			t.Fatalf("explainPendingQuestions: %v", err)
		}
	})
	if !strings.Contains(stdout, "No pending user questions.") {
		t.Fatalf("stdout = %q, want no pending message", stdout)
	}
}
