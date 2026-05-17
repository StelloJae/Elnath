package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/learning"
	basetools "github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

func cmdExplain(_ context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		return printExplainUsage()
	}

	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("explain: load config: %w", err)
	}

	outcomePath := filepath.Join(cfg.DataDir, "outcomes.jsonl")
	outcomeStore := learning.NewOutcomeStore(outcomePath)
	routingAdvisor := learning.NewRoutingAdvisor(outcomeStore)

	var wikiStore *wiki.Store
	if cfg.WikiDir != "" {
		if ws, err := wiki.NewStore(cfg.WikiDir); err == nil {
			wikiStore = ws
		}
	}

	switch args[0] {
	case "last":
		return explainLast(outcomeStore, wikiStore, routingAdvisor)
	case "history":
		n := 10
		if len(args) > 1 {
			parsed, err := strconv.Atoi(args[1])
			if err != nil || parsed <= 0 {
				return fmt.Errorf("explain: history: invalid count %q", args[1])
			}
			n = parsed
		}
		return explainHistory(outcomeStore, n)
	case "timeouts":
		return explainTimeouts(cfg, args[1:])
	case "control-surfaces":
		return explainControlSurfaces(args[1:])
	case "pending-questions":
		return explainPendingQuestions(outcomeStore, args[1:])
	default:
		return fmt.Errorf("explain: unknown subcommand %q (try: elnath explain help)", args[0])
	}
}

func printExplainUsage() error {
	fmt.Fprintf(os.Stdout, `Usage: elnath explain <subcommand>

Subcommands:
  last              Show the most recent routing decision
  history [n]       Show recent n routing decisions (default 10)
  timeouts [--json] Show configured timeout and retry policy
  control-surfaces [--json]
                    Show implemented model-callable control surfaces
  pending-questions [--json] [--session ID] [--limit N]
                    Show unanswered user-input requests from outcome receipts
  help              Show this help
`)
	return nil
}

type pendingQuestionsView struct {
	Pending []learning.PendingUserQuestion `json:"pending"`
	Count   int                            `json:"count"`
}

func explainPendingQuestions(outcomeStore *learning.OutcomeStore, args []string) error {
	jsonOut := false
	sessionID := ""
	limit := 20
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--session":
			value, next, err := parseStringFlag(args, i, "--session")
			if err != nil {
				return err
			}
			sessionID = value
			i = next
		case "--limit":
			value, next, err := parseIntFlag(args, i, "--limit")
			if err != nil {
				return err
			}
			if value <= 0 {
				return fmt.Errorf("explain: pending-questions: --limit must be positive")
			}
			limit = value
			i = next
		case "help", "-h", "--help":
			return printExplainUsage()
		default:
			return fmt.Errorf("explain: pending-questions: unknown flag %q", args[i])
		}
	}
	records, err := outcomeStore.Recent(0)
	if err != nil {
		return fmt.Errorf("explain: pending-questions: %w", err)
	}
	pending := learning.PendingUserQuestions(records, sessionID, limit)
	view := pendingQuestionsView{Pending: pending, Count: len(pending)}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(view)
	}
	if len(pending) == 0 {
		fmt.Fprintln(os.Stdout, "No pending user questions.")
		return nil
	}
	fmt.Fprintln(os.Stdout, "Pending user questions:")
	for _, item := range pending {
		session := item.SessionID
		if session == "" {
			session = "(none)"
		}
		fmt.Fprintf(os.Stdout, "  - %s session=%s asked=%s question=%q\n",
			item.RequestID,
			session,
			item.AskedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
			item.Question,
		)
		if len(item.Options) > 0 {
			fmt.Fprintf(os.Stdout, "    options: %s\n", quotedPendingQuestionOptions(item.Options))
		}
		if item.AnswerCommand != "" {
			fmt.Fprintf(os.Stdout, "    answer: %s\n", item.AnswerCommand)
		}
	}
	return nil
}

func quotedPendingQuestionOptions(options []string) string {
	quoted := make([]string, 0, len(options))
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		quoted = append(quoted, fmt.Sprintf("%q", option))
	}
	return strings.Join(quoted, ", ")
}

type controlSurfacePolicyView struct {
	ProductComplete    bool                                        `json:"product_complete"`
	Surfaces           []controlSurfacePolicyEntry                 `json:"surfaces"`
	DiagnosticAdapters []basetools.MutationDiagnosticAdapterPolicy `json:"diagnostic_adapters,omitempty"`
	RemainingGaps      []string                                    `json:"remaining_gaps"`
	ProductBoundaries  []string                                    `json:"product_boundaries,omitempty"`
}

type controlSurfacePolicyEntry struct {
	Name                   string   `json:"name"`
	Status                 string   `json:"status"`
	Tools                  []string `json:"tools"`
	ToolSearchDiscoverable bool     `json:"tool_search_discoverable"`
	ReceiptBacked          bool     `json:"receipt_backed"`
	ProductBoundary        string   `json:"product_boundary,omitempty"`
	ReplacementPath        []string `json:"replacement_path,omitempty"`
	Notes                  string   `json:"notes,omitempty"`
}

type controlSurfaceManifestEntry struct {
	Name            string
	Status          string
	Tools           []string
	ReceiptBacked   bool
	ProductBoundary string
	ReplacementPath []string
	Notes           string
}

func explainControlSurfaces(args []string) error {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "help", "-h", "--help":
			return printExplainUsage()
		default:
			return fmt.Errorf("explain: control-surfaces: unknown flag %q", arg)
		}
	}

	view := controlSurfacePolicyViewForRuntime()
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(view)
	}

	fmt.Fprintln(os.Stdout, "Control surfaces:")
	for _, surface := range view.Surfaces {
		fmt.Fprintf(os.Stdout, "  - %s: %s tools=%s tool_search=%t receipts=%t\n",
			surface.Name,
			surface.Status,
			strings.Join(surface.Tools, ","),
			surface.ToolSearchDiscoverable,
			surface.ReceiptBacked,
		)
	}
	if len(view.DiagnosticAdapters) > 0 {
		fmt.Fprintln(os.Stdout, "Diagnostic adapters:")
		for _, adapter := range view.DiagnosticAdapters {
			fmt.Fprintf(os.Stdout, "  - %s: %s adapter=%s command=%s timeout_ms=%d scope=%s\n",
				adapter.Language,
				adapter.Status,
				adapter.Adapter,
				adapter.Command,
				adapter.TimeoutMS,
				adapter.Scope,
			)
		}
	}
	fmt.Fprintln(os.Stdout, "Remaining gaps:")
	if len(view.RemainingGaps) == 0 {
		fmt.Fprintln(os.Stdout, "  - none")
	} else {
		for _, gap := range view.RemainingGaps {
			fmt.Fprintf(os.Stdout, "  - %s\n", gap)
		}
	}
	if len(view.ProductBoundaries) > 0 {
		fmt.Fprintln(os.Stdout, "Product boundaries:")
		for _, boundary := range view.ProductBoundaries {
			fmt.Fprintf(os.Stdout, "  - %s\n", boundary)
		}
	}
	return nil
}

func controlSurfacePolicyViewForRuntime() controlSurfacePolicyView {
	surfaces := make([]controlSurfacePolicyEntry, 0, len(controlSurfaceManifest()))
	var productBoundaries []string
	for _, surface := range controlSurfaceManifest() {
		tools := append([]string(nil), surface.Tools...)
		replacementPath := append([]string(nil), surface.ReplacementPath...)
		surfaces = append(surfaces, controlSurfacePolicyEntry{
			Name:                   surface.Name,
			Status:                 surface.Status,
			Tools:                  tools,
			ToolSearchDiscoverable: controlSurfaceToolsMatchRouting(surface.Name, tools),
			ReceiptBacked:          surface.ReceiptBacked,
			ProductBoundary:        surface.ProductBoundary,
			ReplacementPath:        replacementPath,
			Notes:                  surface.Notes,
		})
		if surface.ProductBoundary != "" {
			productBoundaries = append(productBoundaries, surface.ProductBoundary)
		}
	}
	boundaries := controlSurfaceBoundaryReasons()
	productBoundaries = append(productBoundaries, boundaries["self_correction"], boundaries["status"])
	return controlSurfacePolicyView{
		ProductComplete:    true,
		Surfaces:           surfaces,
		DiagnosticAdapters: basetools.MutationDiagnosticAdapterPolicies(),
		RemainingGaps:      []string{},
		ProductBoundaries:  productBoundaries,
	}
}

func controlSurfaceBoundaryReasons() map[string]string {
	return map[string]string{
		"user_input":        "UI-level modal answer collection is outside the Go runtime boundary; runtime/CLI/gateway request, list, wait, answer, cancel, timeout, and receipt paths are implemented.",
		"process":           "process_wait intentionally supports literal watch_text; full async line-watch is deferred to a future streaming UX layer.",
		"code_intelligence": "full multi-language LSP lifecycle is product-excluded for this runtime closeout; Go-native code_symbols plus mutation diagnostic adapters are the replacement path.",
		"self_correction":   "bounded self-correction is intentionally closed-enum and receipt-backed; broad silent self-healing is product-excluded.",
		"status":            "runtime /status reports registry/control-surface coverage; deeper registry diagnostics are future polish, not a product-runtime gate.",
	}
}

func controlSurfaceReplacementPaths() map[string][]string {
	return map[string][]string{
		"user_input": {
			"ask_user_question",
			"user_question_list",
			"user_question_wait",
			"user_question_answer",
			"user_question_cancel",
			"Telegram/operator gateway answer path",
		},
		"process": {
			"process_start",
			"process_monitor",
			"process_wait watch_text",
			"process_stop",
		},
		"code_intelligence": {
			"code_symbols document_symbols/workspace_symbols",
			"code_symbols definition/references/hover",
			"code_symbols diagnostics/diagnostics_delta",
			"structured mutation diagnostics for Go and Python syntax",
			"future plugin/provider adapters for non-Go language servers",
		},
	}
}

func controlSurfaceManifest() []controlSurfaceManifestEntry {
	return []controlSurfaceManifestEntry{
		{
			Name:          "discovery",
			Status:        "implemented",
			Tools:         []string{"tool_search"},
			ReceiptBacked: true,
			Notes:         "deferred tool catalog and selection receipts",
		},
		{
			Name:          "task",
			Status:        "implemented",
			Tools:         []string{"task_create", "task_list", "task_get", "task_stop", "task_output", "task_monitor", "task_update"},
			ReceiptBacked: true,
			Notes:         "daemon queue task lifecycle",
		},
		{
			Name:            "user_input",
			Status:          "implemented_with_product_boundary",
			Tools:           []string{"ask_user_question", "user_question_list", "user_question_wait", "user_question_answer", "user_question_cancel"},
			ReceiptBacked:   true,
			ProductBoundary: controlSurfaceBoundaryReasons()["user_input"],
			ReplacementPath: controlSurfaceReplacementPaths()["user_input"],
			Notes:           "structured question receipts, pending lookup/wait, strict answer enqueue, CLI answer surface, and gateway/operator answer path",
		},
		{
			Name:          "schedule",
			Status:        "implemented",
			Tools:         []string{"schedule_create", "schedule_list", "schedule_delete"},
			ReceiptBacked: true,
			Notes:         "static scheduled daemon tasks",
		},
		{
			Name:          "plan",
			Status:        "implemented",
			Tools:         []string{"enter_plan_mode", "exit_plan_mode"},
			ReceiptBacked: true,
			Notes:         "session permission-mode transition",
		},
		{
			Name:          "worktree",
			Status:        "implemented",
			Tools:         []string{"enter_worktree", "worktree_list", "worktree_run", "worktree_prune", "exit_worktree"},
			ReceiptBacked: true,
			Notes:         "managed git worktree lifecycle and bounded run",
		},
		{
			Name:            "process",
			Status:          "implemented_with_product_boundary",
			Tools:           []string{"process_start", "process_monitor", "process_wait", "process_stop"},
			ReceiptBacked:   true,
			ProductBoundary: controlSurfaceBoundaryReasons()["process"],
			ReplacementPath: controlSurfaceReplacementPaths()["process"],
			Notes:           "session-scoped long-running command observation with bounded literal watch_text support",
		},
		{
			Name:          "skill",
			Status:        "implemented",
			Tools:         []string{"skill_catalog", "skill", "create_skill"},
			ReceiptBacked: true,
			Notes:         "SKILL.md-compatible discovery and execution",
		},
		{
			Name:          "command",
			Status:        "implemented",
			Tools:         []string{"command_catalog", "runtime_command"},
			ReceiptBacked: true,
			Notes:         "read-only command catalog and safe runtime slash execution",
		},
		{
			Name:          "scratchpad",
			Status:        "implemented",
			Tools:         []string{"todo_write"},
			ReceiptBacked: true,
			Notes:         "session task scratchpad with single in_progress and active_form guards plus verification nudge receipt",
		},
		{
			Name:            "code_intelligence",
			Status:          "implemented_with_product_boundary",
			Tools:           []string{"code_symbols"},
			ReceiptBacked:   true,
			ProductBoundary: controlSurfaceBoundaryReasons()["code_intelligence"],
			ReplacementPath: controlSurfaceReplacementPaths()["code_intelligence"],
			Notes:           "Go-native symbols, definitions, references, hover signatures, syntax diagnostics, edit-aware diagnostic deltas, and Python syntax mutation diagnostics",
		},
	}
}

func controlSurfaceToolsMatchRouting(surface string, names []string) bool {
	if surface == "" || len(names) == 0 {
		return false
	}
	for _, name := range names {
		routing := basetools.ToolRoutingMetadataForName(name)
		if routing.Category != surface || routing.Surface == "" {
			return false
		}
	}
	return true
}

type timeoutPolicyView struct {
	ProviderRequestTimeouts []providerTimeoutPolicyView  `json:"provider_request_timeouts"`
	Daemon                  daemonTimeoutPolicyView      `json:"daemon"`
	SelfHealing             selfHealingTimeoutPolicyView `json:"self_healing"`
	Process                 processTimeoutPolicyView     `json:"process"`
	Telegram                telegramTimeoutPolicyView    `json:"telegram"`
}

type providerTimeoutPolicyView struct {
	Provider       string `json:"provider"`
	ConfigKey      string `json:"config_key"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type daemonTimeoutPolicyView struct {
	InactivityTimeoutSeconds int    `json:"inactivity_timeout_seconds"`
	WallClockTimeoutSeconds  int    `json:"wall_clock_timeout_seconds"`
	MaxRecoveries            int    `json:"max_recoveries"`
	WorkspaceRetention       string `json:"workspace_retention"`
}

type selfHealingTimeoutPolicyView struct {
	Enabled                                    bool     `json:"enabled"`
	ObserveOnly                                bool     `json:"observe_only"`
	TimeoutSeconds                             int      `json:"timeout_seconds"`
	CompletionRetryMax                         int      `json:"completion_retry_max"`
	CompletionRetrySupportedMax                int      `json:"completion_retry_supported_max"`
	CompletionRetryDecisions                   []string `json:"completion_retry_decisions"`
	VerificationRetryRequiresStandaloneCommand bool     `json:"verification_retry_requires_standalone_command"`
	VerificationRetryInfersCommandFromProse    bool     `json:"verification_retry_infers_command_from_prose"`
}

type telegramTimeoutPolicyView struct {
	PollTimeoutSeconds int `json:"poll_timeout_seconds"`
}

type processTimeoutPolicyView struct {
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
}

func explainTimeouts(cfg *config.Config, args []string) error {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "help", "-h", "--help":
			return printExplainUsage()
		default:
			return fmt.Errorf("explain: timeouts: unknown flag %q", arg)
		}
	}

	view := timeoutPolicyViewForConfig(cfg)
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(view)
	}

	fmt.Fprintln(os.Stdout, "Timeout policy:")
	fmt.Fprintln(os.Stdout, "  Provider request timeouts:")
	for _, entry := range view.ProviderRequestTimeouts {
		fmt.Fprintf(os.Stdout, "    - %s: %ds (%s)\n", entry.Provider, entry.TimeoutSeconds, entry.ConfigKey)
	}
	fmt.Fprintf(os.Stdout, "  Daemon: inactivity=%ds wall_clock=%ds max_recoveries=%d workspace_retention=%s\n",
		view.Daemon.InactivityTimeoutSeconds,
		view.Daemon.WallClockTimeoutSeconds,
		view.Daemon.MaxRecoveries,
		view.Daemon.WorkspaceRetention,
	)
	fmt.Fprintf(os.Stdout, "  Self-healing: enabled=%t observe_only=%t timeout=%ds completion_retry_max=%d supported_max=%d decisions=%s verification_retry=standalone_command_only\n",
		view.SelfHealing.Enabled,
		view.SelfHealing.ObserveOnly,
		view.SelfHealing.TimeoutSeconds,
		view.SelfHealing.CompletionRetryMax,
		view.SelfHealing.CompletionRetrySupportedMax,
		strings.Join(view.SelfHealing.CompletionRetryDecisions, ","),
	)
	fmt.Fprintf(os.Stdout, "  Process tools: default_timeout=%dms max_timeout=%dms default_wait=%dms max_wait=%dms kill_grace=%dms tail=%d/%d monitor_followup=%s wait_followup=%s receipt_fields=%s\n",
		view.Process.DefaultTimeoutMS,
		view.Process.MaxTimeoutMS,
		view.Process.DefaultWaitMS,
		view.Process.MaxWaitMS,
		view.Process.KillGraceMS,
		view.Process.DefaultTailBytes,
		view.Process.MaxTailBytes,
		view.Process.MonitorFollowupTool,
		view.Process.WaitFollowupTool,
		strings.Join(view.Process.ReceiptFields, ","),
	)
	fmt.Fprintf(os.Stdout, "  Telegram: poll_timeout=%ds\n", view.Telegram.PollTimeoutSeconds)
	return nil
}

func timeoutPolicyViewForConfig(cfg *config.Config) timeoutPolicyView {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	workspaceRetention := strings.TrimSpace(cfg.Daemon.WorkspaceRetention)
	if workspaceRetention == "" {
		workspaceRetention = "immediate"
	}
	processPolicy := basetools.ProcessExecutionPolicySnapshot()
	return timeoutPolicyView{
		ProviderRequestTimeouts: []providerTimeoutPolicyView{
			{Provider: "anthropic", ConfigKey: "anthropic.timeout_seconds", TimeoutSeconds: cfg.Anthropic.Timeout},
			{Provider: "openai", ConfigKey: "openai.timeout_seconds", TimeoutSeconds: cfg.OpenAI.Timeout},
			{Provider: "openai_responses", ConfigKey: "openai_responses.timeout_seconds", TimeoutSeconds: cfg.OpenAIResponses.Timeout},
			{Provider: "codex_oauth", ConfigKey: "openai_responses.timeout_seconds", TimeoutSeconds: cfg.OpenAIResponses.Timeout},
		},
		Daemon: daemonTimeoutPolicyView{
			InactivityTimeoutSeconds: cfg.Daemon.InactivityTimeout,
			WallClockTimeoutSeconds:  cfg.Daemon.WallClockTimeout,
			MaxRecoveries:            cfg.Daemon.MaxRecoveries,
			WorkspaceRetention:       workspaceRetention,
		},
		SelfHealing: selfHealingTimeoutPolicyView{
			Enabled:                     cfg.SelfHealing.Enabled,
			ObserveOnly:                 cfg.SelfHealing.ObserveOnly,
			TimeoutSeconds:              cfg.SelfHealing.TimeoutSeconds,
			CompletionRetryMax:          cfg.SelfHealing.CompletionRetryMax,
			CompletionRetrySupportedMax: maxCompletionRetryAttempts,
			CompletionRetryDecisions: []string{
				completionRetryDecisionRetrySmallerScope,
				completionRetryDecisionRunVerification,
			},
			VerificationRetryRequiresStandaloneCommand: true,
			VerificationRetryInfersCommandFromProse:    false,
		},
		Process: processTimeoutPolicyView{
			DefaultTimeoutMS:    processPolicy.DefaultTimeoutMS,
			MaxTimeoutMS:        processPolicy.MaxTimeoutMS,
			DefaultWaitMS:       processPolicy.DefaultWaitMS,
			MaxWaitMS:           processPolicy.MaxWaitMS,
			KillGraceMS:         processPolicy.KillGraceMS,
			DefaultTailBytes:    processPolicy.DefaultTailBytes,
			MaxTailBytes:        processPolicy.MaxTailBytes,
			MonitorFollowupTool: processPolicy.MonitorFollowupTool,
			WaitFollowupTool:    processPolicy.WaitFollowupTool,
			ReceiptFields:       append([]string(nil), processPolicy.ReceiptFields...),
		},
		Telegram: telegramTimeoutPolicyView{
			PollTimeoutSeconds: cfg.Telegram.PollTimeoutSeconds,
		},
	}
}

func explainLast(outcomeStore *learning.OutcomeStore, wikiStore *wiki.Store, advisor *learning.RoutingAdvisor) error {
	records, err := outcomeStore.Recent(1)
	if err != nil {
		return fmt.Errorf("explain: %w", err)
	}
	if len(records) == 0 {
		fmt.Fprintf(os.Stdout, "No routing decisions recorded yet.\n")
		return nil
	}

	r := records[0]

	pref, _ := wiki.LoadWorkflowPreference(wikiStore, r.ProjectID)

	stats, _ := advisor.ProjectStats(r.ProjectID, 30)

	resultMark := "x"
	if r.Success {
		resultMark = "✓"
	}

	fmt.Fprintf(os.Stdout, "Last routing decision (%s)\n\n", r.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"))

	if r.InputSnippet != "" {
		snippet := r.InputSnippet
		if len([]rune(snippet)) == 100 {
			snippet += "..."
		}
		fmt.Fprintf(os.Stdout, "  Input:     %q\n", snippet)
	}
	fmt.Fprintf(os.Stdout, "  Intent:    %s\n", r.Intent)
	fmt.Fprintf(os.Stdout, "  Workflow:  %s\n", r.Workflow)
	fmt.Fprintf(os.Stdout, "  Result:    %s %s (%d iterations, %.1fs)\n\n",
		resultMark, finishLabel(r.Success, r.FinishReason), r.Iterations, r.Duration)

	fmt.Fprintf(os.Stdout, "  Why this workflow?\n")
	if r.PreferenceUsed && pref != nil {
		if pw := pref.PreferredWorkflow(r.Intent); pw != "" {
			fmt.Fprintf(os.Stdout, "    • Preference: %s → %s\n", r.Intent, pw)
		} else {
			fmt.Fprintf(os.Stdout, "    • Preference applied for intent %s\n", r.Intent)
		}
	}
	fmt.Fprintf(os.Stdout, "    • Context: existing_code=%v, estimated_files=%d\n\n",
		r.ExistingCode, r.EstimatedFiles)

	if len(stats) > 0 {
		fmt.Fprintf(os.Stdout, "  Project %q routing stats (last 30):\n", r.ProjectID)
		intents := make([]string, 0, len(stats))
		for intent := range stats {
			intents = append(intents, intent)
		}
		sort.Strings(intents)
		for _, intent := range intents {
			wfStats := stats[intent]
			wfNames := make([]string, 0, len(wfStats))
			for wf := range wfStats {
				wfNames = append(wfNames, wf)
			}
			sort.Slice(wfNames, func(i, j int) bool {
				si, sj := wfStats[wfNames[i]], wfStats[wfNames[j]]
				ri := float64(si.Success) / float64(si.Total)
				rj := float64(sj.Success) / float64(sj.Total)
				return ri > rj
			})
			parts := make([]string, 0, len(wfNames))
			for _, wf := range wfNames {
				s := wfStats[wf]
				pct := int(100 * float64(s.Success) / float64(s.Total))
				parts = append(parts, fmt.Sprintf("%s %d%% (%d/%d)", wf, pct, s.Success, s.Total))
			}
			fmt.Fprintf(os.Stdout, "    %-14s %s\n", intent+":", strings.Join(parts, ", "))
		}
	}

	return nil
}

func explainHistory(outcomeStore *learning.OutcomeStore, n int) error {
	records, err := outcomeStore.Recent(n)
	if err != nil {
		return fmt.Errorf("explain: %w", err)
	}
	if len(records) == 0 {
		fmt.Fprintf(os.Stdout, "No routing decisions recorded yet.\n")
		return nil
	}

	fmt.Fprintf(os.Stdout, "Recent routing decisions:\n\n")
	fmt.Fprintf(os.Stdout, "  %2s  %-20s  %-14s  %-10s  %-8s  %s\n",
		"#", "Time", "Intent", "Workflow", "Result", "Duration")

	for i, r := range records {
		result := "success"
		if !r.Success {
			result = "failure"
		}
		fmt.Fprintf(os.Stdout, "  %2d  %-20s  %-14s  %-10s  %-8s  %.1fs\n",
			i+1,
			r.Timestamp.UTC().Format("2006-01-02 15:04:05"),
			r.Intent,
			r.Workflow,
			result,
			r.Duration,
		)
	}
	return nil
}

func finishLabel(success bool, reason string) string {
	if success {
		if reason == "unverified_inline" {
			return "success (unverified inline)"
		}
		return "success"
	}
	if reason != "" {
		return "failure (" + reason + ")"
	}
	return "failure"
}
