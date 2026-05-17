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
		Process struct {
			DefaultTimeoutMS    int      `json:"default_timeout_ms"`
			MaxTimeoutMS        int      `json:"max_timeout_ms"`
			DefaultWaitMS       int      `json:"default_wait_ms"`
			MaxWaitMS           int      `json:"max_wait_ms"`
			KillGraceMS         int      `json:"kill_grace_ms"`
			DefaultTailBytes    int      `json:"default_tail_bytes"`
			MaxTailBytes        int      `json:"max_tail_bytes"`
			MonitorFollowupTool string   `json:"monitor_followup_tool"`
			WaitFollowupTool    string   `json:"wait_followup_tool"`
			ReceiptFields       []string `json:"receipt_fields"`
		} `json:"process"`
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
	if out.Process.DefaultTimeoutMS != 600000 || out.Process.MaxTimeoutMS != 3600000 || out.Process.KillGraceMS != 2000 {
		t.Fatalf("process timeout policy = %+v, want default/max/kill-grace milliseconds", out.Process)
	}
	if out.Process.DefaultWaitMS != 1000 || out.Process.MaxWaitMS != 60000 {
		t.Fatalf("process wait policy = %+v, want default/max wait milliseconds", out.Process)
	}
	if out.Process.DefaultTailBytes != 4000 || out.Process.MaxTailBytes != 20000 || out.Process.MonitorFollowupTool != basetools.ProcessMonitorToolName || out.Process.WaitFollowupTool != basetools.ProcessWaitToolName {
		t.Fatalf("process tail/followup policy = %+v", out.Process)
	}
	for _, want := range []string{"status", "terminal", "timed_out", "timeout_ms", "followup_tool", "command_intent", "intent_source", "wait_ms", "wait_elapsed_ms", "wait_timed_out"} {
		found := false
		for _, got := range out.Process.ReceiptFields {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("process receipt_fields = %v, missing %q", out.Process.ReceiptFields, want)
		}
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
		"Process tools: default_timeout=600000ms max_timeout=3600000ms default_wait=1000ms max_wait=60000ms kill_grace=2000ms tail=4000/20000",
		"receipt_fields=status,terminal,timed_out,timeout_ms,followup_tool,command_intent,intent_source,tail_bytes,stdout_raw_bytes,stderr_raw_bytes,wait_ms,wait_elapsed_ms,wait_timed_out",
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
		ProductComplete bool `json:"product_complete"`
		Surfaces        []struct {
			Name                   string   `json:"name"`
			Status                 string   `json:"status"`
			Tools                  []string `json:"tools"`
			ToolSearchDiscoverable bool     `json:"tool_search_discoverable"`
			ReceiptBacked          bool     `json:"receipt_backed"`
			ProductBoundary        string   `json:"product_boundary"`
			ReplacementPath        []string `json:"replacement_path"`
			Notes                  string   `json:"notes"`
		} `json:"surfaces"`
		DiagnosticAdapters []struct {
			Language  string `json:"language"`
			Status    string `json:"status"`
			Adapter   string `json:"adapter"`
			Command   string `json:"command"`
			TimeoutMS int    `json:"timeout_ms"`
		} `json:"diagnostic_adapters"`
		RemainingGaps     []string `json:"remaining_gaps"`
		ProductBoundaries []string `json:"product_boundaries"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	byName := map[string]struct {
		status                 string
		toolSearchDiscoverable bool
		receiptBacked          bool
		toolCount              int
		boundary               string
		replacementCount       int
		notes                  string
	}{}
	for _, surface := range out.Surfaces {
		byName[surface.Name] = struct {
			status                 string
			toolSearchDiscoverable bool
			receiptBacked          bool
			toolCount              int
			boundary               string
			replacementCount       int
			notes                  string
		}{
			status:                 surface.Status,
			toolSearchDiscoverable: surface.ToolSearchDiscoverable,
			receiptBacked:          surface.ReceiptBacked,
			toolCount:              len(surface.Tools),
			boundary:               surface.ProductBoundary,
			replacementCount:       len(surface.ReplacementPath),
			notes:                  surface.Notes,
		}
	}
	if !out.ProductComplete {
		t.Fatalf("product_complete = false; out=%+v", out)
	}
	for _, name := range []string{"discovery", "task", "schedule", "plan", "worktree", "skill", "command", "scratchpad", "agentic"} {
		entry, ok := byName[name]
		if !ok {
			t.Fatalf("missing control surface %q in %+v", name, byName)
		}
		if entry.status != "implemented" || !entry.toolSearchDiscoverable || !entry.receiptBacked || entry.toolCount == 0 {
			t.Fatalf("surface %s = %+v, want implemented ToolSearch-discoverable receipt-backed with tools", name, entry)
		}
	}
	commandSurface := byName["command"]
	if !strings.Contains(commandSurface.notes, "provider route") {
		t.Fatalf("command surface notes = %q, want provider route explainability", commandSurface.notes)
	}
	agenticSurface := byName["agentic"]
	if agenticSurface.toolCount != 8 || !strings.Contains(agenticSurface.notes, "delegation") || !strings.Contains(agenticSurface.notes, "evidence") {
		t.Fatalf("agentic surface = %+v, want eight agentic delegation/evidence tools", agenticSurface)
	}
	userInput, ok := byName["user_input"]
	if !ok {
		t.Fatalf("missing control surface user_input in %+v", byName)
	}
	if userInput.status != "implemented_with_product_boundary" || !userInput.toolSearchDiscoverable || !userInput.receiptBacked || userInput.toolCount != 5 || userInput.boundary == "" || userInput.replacementCount == 0 {
		t.Fatalf("user_input surface = %+v, want product-boundary implemented ToolSearch-discoverable receipt-backed with five tools", userInput)
	}
	process, ok := byName["process"]
	if !ok {
		t.Fatalf("missing control surface process in %+v", byName)
	}
	if process.status != "implemented_with_product_boundary" || !process.toolSearchDiscoverable || !process.receiptBacked || process.toolCount != 4 || process.boundary == "" || process.replacementCount == 0 {
		t.Fatalf("process surface = %+v, want product-boundary implemented process tools", process)
	}
	codeIntelligence, ok := byName["code_intelligence"]
	if !ok {
		t.Fatalf("missing control surface code_intelligence in %+v", byName)
	}
	if codeIntelligence.status != "implemented_with_product_boundary" || !codeIntelligence.toolSearchDiscoverable || !codeIntelligence.receiptBacked || codeIntelligence.toolCount != 1 || codeIntelligence.boundary == "" || codeIntelligence.replacementCount == 0 {
		t.Fatalf("code_intelligence surface = %+v, want product-boundary implemented ToolSearch-discoverable receipt-backed with one tool", codeIntelligence)
	}
	if !strings.Contains(codeIntelligence.notes, "references") {
		t.Fatalf("code_intelligence notes = %q, want references capability", codeIntelligence.notes)
	}
	if !strings.Contains(codeIntelligence.notes, "hover") {
		t.Fatalf("code_intelligence notes = %q, want hover capability", codeIntelligence.notes)
	}
	if !strings.Contains(codeIntelligence.notes, "diagnostics") {
		t.Fatalf("code_intelligence notes = %q, want diagnostics capability", codeIntelligence.notes)
	}
	if !strings.Contains(codeIntelligence.notes, "diagnostic deltas") {
		t.Fatalf("code_intelligence notes = %q, want diagnostic delta capability", codeIntelligence.notes)
	}
	adaptersByLanguage := make(map[string]struct {
		status    string
		adapter   string
		command   string
		timeoutMS int
	})
	for _, adapter := range out.DiagnosticAdapters {
		adaptersByLanguage[adapter.Language] = struct {
			status    string
			adapter   string
			command   string
			timeoutMS int
		}{
			status:    adapter.Status,
			adapter:   adapter.Adapter,
			command:   adapter.Command,
			timeoutMS: adapter.TimeoutMS,
		}
	}
	goAdapter, ok := adaptersByLanguage["go"]
	if !ok || goAdapter.status != "available" || goAdapter.adapter != "go/parser" {
		t.Fatalf("go diagnostic adapter = %+v, ok=%t, want available go/parser", goAdapter, ok)
	}
	pythonAdapter, ok := adaptersByLanguage["python"]
	if !ok || pythonAdapter.adapter != "python/py_compile" || pythonAdapter.command != "python3" || pythonAdapter.timeoutMS != 2000 {
		t.Fatalf("python diagnostic adapter = %+v, ok=%t, want py_compile command with 2000ms timeout", pythonAdapter, ok)
	}
	if pythonAdapter.status != "available" && pythonAdapter.status != "diagnostics_not_configured" {
		t.Fatalf("python diagnostic adapter status = %q, want available or diagnostics_not_configured", pythonAdapter.status)
	}
	tsAdapter, ok := adaptersByLanguage["typescript"]
	if !ok || tsAdapter.adapter != "typescript/transpileModule" || tsAdapter.command != "node" || tsAdapter.timeoutMS != 2000 {
		t.Fatalf("typescript diagnostic adapter = %+v, ok=%t, want transpileModule command with 2000ms timeout", tsAdapter, ok)
	}
	if tsAdapter.status != "conditional" && tsAdapter.status != "diagnostics_not_configured" {
		t.Fatalf("typescript diagnostic adapter status = %q, want conditional or diagnostics_not_configured", tsAdapter.status)
	}
	jsAdapter, ok := adaptersByLanguage["javascript"]
	if !ok || jsAdapter.adapter != "typescript/transpileModule" || jsAdapter.command != "node" || jsAdapter.timeoutMS != 2000 {
		t.Fatalf("javascript diagnostic adapter = %+v, ok=%t, want transpileModule command with 2000ms timeout", jsAdapter, ok)
	}
	if jsAdapter.status != "conditional" && jsAdapter.status != "diagnostics_not_configured" {
		t.Fatalf("javascript diagnostic adapter status = %q, want conditional or diagnostics_not_configured", jsAdapter.status)
	}
	scratchpad, ok := byName["scratchpad"]
	if !ok {
		t.Fatalf("missing control surface scratchpad in %+v", byName)
	}
	if !strings.Contains(scratchpad.notes, "single in_progress") {
		t.Fatalf("scratchpad notes = %q, want single in_progress guard", scratchpad.notes)
	}
	if !strings.Contains(scratchpad.notes, "active_form") {
		t.Fatalf("scratchpad notes = %q, want active_form guard", scratchpad.notes)
	}
	for _, gap := range out.RemainingGaps {
		if strings.Contains(gap, "surface status is static") {
			t.Fatalf("remaining gap %q should be replaced after manifest-backed control-surface metadata", gap)
		}
		if strings.Contains(gap, "blocking wait state") {
			t.Fatalf("remaining gap %q is stale after process_wait and user_question_wait", gap)
		}
		if strings.Contains(gap, "full runtime registry introspection remains future polish") {
			t.Fatalf("remaining gap %q is stale after runtime status registry introspection", gap)
		}
	}
	if len(out.RemainingGaps) != 0 {
		t.Fatalf("remaining_gaps = %+v, want true blockers empty after product boundaries are explicit", out.RemainingGaps)
	}
	if len(out.ProductBoundaries) < 5 {
		t.Fatalf("product_boundaries = %+v, want explicit boundary list", out.ProductBoundaries)
	}
	if !stringSliceContainsSubstring(out.ProductBoundaries, "Provider route") {
		t.Fatalf("product_boundaries = %+v, want provider route planning-only boundary", out.ProductBoundaries)
	}
}

func stringSliceContainsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
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
		"discovery: implemented",
		"task: implemented",
		"user_input: implemented_with_product_boundary",
		"worktree: implemented",
		"scratchpad: implemented",
		"agentic: implemented",
		"code_intelligence: implemented_with_product_boundary",
		"Remaining gaps:",
		"none",
		"Product boundaries:",
		"full multi-language LSP lifecycle",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestCmdExplainCodeIntelligenceJSONRunsGoDiagnostics(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "valid.go"), []byte("package demo\n\nfunc OK() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(valid.go): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package demo\n\nfunc Broken( {\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(broken.go): %v", err)
	}
	t.Chdir(root)

	stdout, _ := captureOutput(t, func() {
		if err := cmdExplain(context.Background(), []string{"code-intelligence", "--json", "--path", "."}); err != nil {
			t.Fatalf("cmdExplain(code-intelligence --json): %v", err)
		}
	})

	var out struct {
		ProductBoundary string `json:"product_boundary"`
		GoDiagnostics   struct {
			Status string `json:"status"`
			Path   string `json:"path"`
			Count  int    `json:"count"`
			Errors []struct {
				FilePath string `json:"file_path"`
				Error    string `json:"error"`
				Source   string `json:"source"`
			} `json:"errors"`
		} `json:"go_diagnostics"`
		Receipt struct {
			Tool               string `json:"tool"`
			Action             string `json:"action"`
			ReadOnly           bool   `json:"read_only"`
			DiagnosticsChecked bool   `json:"diagnostics_checked"`
			Status             string `json:"status"`
			Count              int    `json:"count"`
		} `json:"receipt"`
		RepairHints []struct {
			FilePath       string   `json:"file_path"`
			Line           int      `json:"line"`
			Column         int      `json:"column"`
			SuggestedTools []string `json:"suggested_tools"`
		} `json:"repair_hints"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if !strings.Contains(out.ProductBoundary, "full multi-language LSP lifecycle") {
		t.Fatalf("product_boundary = %q, want LSP boundary", out.ProductBoundary)
	}
	if out.GoDiagnostics.Status != "diagnostics_found" || out.GoDiagnostics.Count == 0 {
		t.Fatalf("go_diagnostics = %+v, want diagnostics_found", out.GoDiagnostics)
	}
	if out.GoDiagnostics.Path != "." {
		t.Fatalf("go_diagnostics.path = %q, want .", out.GoDiagnostics.Path)
	}
	if len(out.GoDiagnostics.Errors) == 0 || out.GoDiagnostics.Errors[0].FilePath != "broken.go" || out.GoDiagnostics.Errors[0].Source != "go/parser" {
		t.Fatalf("diagnostic errors = %+v, want broken.go parser error", out.GoDiagnostics.Errors)
	}
	if out.Receipt.Tool != "explain_code_intelligence" || out.Receipt.Action != "status" || !out.Receipt.ReadOnly || !out.Receipt.DiagnosticsChecked || out.Receipt.Status != "diagnostics_found" || out.Receipt.Count == 0 {
		t.Fatalf("receipt = %+v, want read-only checked diagnostics receipt", out.Receipt)
	}
	if len(out.RepairHints) == 0 || out.RepairHints[0].FilePath != "broken.go" || out.RepairHints[0].Line == 0 || out.RepairHints[0].Column == 0 {
		t.Fatalf("repair_hints = %+v, want broken.go located hint", out.RepairHints)
	}
	for _, want := range []string{"read_file", "edit_file", "code_symbols diagnostics_delta"} {
		if !stringSliceContainsSubstring(out.RepairHints[0].SuggestedTools, want) {
			t.Fatalf("repair hint tools = %+v, missing %q", out.RepairHints[0].SuggestedTools, want)
		}
	}
}

func TestExplainCodeIntelligenceText(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "valid.go"), []byte("package demo\n\nfunc OK() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(valid.go): %v", err)
	}
	t.Chdir(root)

	stdout, _ := captureOutput(t, func() {
		if err := explainCodeIntelligence([]string{"--path", "."}); err != nil {
			t.Fatalf("explainCodeIntelligence: %v", err)
		}
	})

	for _, want := range []string{
		"Code intelligence:",
		"boundary: full multi-language LSP lifecycle",
		"replacement: code_symbols diagnostics/diagnostics_delta",
		"Go diagnostics: success count=0 path=.",
		"Diagnostic adapters:",
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
			Options:   []string{"main", "new branch"},
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
			RequestID      string `json:"request_id"`
			SessionID      string `json:"session_id"`
			Question       string `json:"question"`
			Answerable     bool   `json:"answerable"`
			AnswerCommand  string `json:"answer_command"`
			PendingCommand string `json:"pending_command"`
		} `json:"pending"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if out.Count != 1 || len(out.Pending) != 1 || out.Pending[0].RequestID != "req-1" || out.Pending[0].SessionID != "sess-1" || out.Pending[0].Question != "Which branch?" {
		t.Fatalf("pending output = %+v, want req-1", out)
	}
	if !out.Pending[0].Answerable || out.Pending[0].AnswerCommand != "elnath task answer --session 'sess-1' --request 'req-1' --answer 'ANSWER_TEXT'" || out.Pending[0].PendingCommand != "elnath explain pending-questions --session 'sess-1'" {
		t.Fatalf("pending handoff = %+v, want answerable CLI commands", out.Pending[0])
	}
}

func TestExplainPendingQuestionsTextShowsAnswerCommand(t *testing.T) {
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
			Options:   []string{"main", "new branch"},
		}},
	}); err != nil {
		t.Fatalf("Append ask record: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := explainPendingQuestions(store, []string{"--session", "sess-1"}); err != nil {
			t.Fatalf("explainPendingQuestions: %v", err)
		}
	})
	if !strings.Contains(stdout, "answer: elnath task answer --session 'sess-1' --request 'req-1' --answer 'ANSWER_TEXT'") {
		t.Fatalf("stdout missing answer command:\n%s", stdout)
	}
	if !strings.Contains(stdout, `options: 1. "main", 2. "new branch"`) {
		t.Fatalf("stdout missing structured options:\n%s", stdout)
	}
	for _, want := range []string{
		"choose 1: elnath task answer --session 'sess-1' --request 'req-1' --choice 1",
		"choose 2: elnath task answer --session 'sess-1' --request 'req-1' --choice 2",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing numeric choice command %q:\n%s", want, stdout)
		}
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
