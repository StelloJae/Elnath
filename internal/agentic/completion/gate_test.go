package completion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/daemon"
	_ "modernc.org/sqlite"
)

func TestCompletionGate_ExplicitGateRequiresPassedVerification(t *testing.T) {
	ctx := context.Background()
	db, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	run := createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))

	decision, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !decision.Passed || decision.VerificationRunID != run.ID || decision.Status != agentic.CompletionGateStatusPassed {
		t.Fatalf("decision = %+v, want passed with verifier %d", decision, run.ID)
	}
	assertCompletionGateCount(t, db, 1)
}

func TestCompletionGate_BlocksWithoutVerification(t *testing.T) {
	ctx := context.Background()
	db, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)

	decision, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, time.Now().UTC()), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Passed || decision.Status != agentic.CompletionGateStatusBlocked || !strings.Contains(decision.Reason, "missing verifier") {
		t.Fatalf("decision = %+v, want missing verifier block", decision)
	}
	assertCompletionGateCount(t, db, 1)
}

func TestCompletionGate_ConfigObserveRejectsGateRequest(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)

	err := NewGate(store, ModeObserve).Validate(ctx, completionQueueTask(task.ID, time.Now().UTC()), task.ID)
	if err == nil {
		t.Fatal("Validate error = nil, want config maximum rejection")
	}
	if !strings.Contains(err.Error(), "config maximum") {
		t.Fatalf("Validate error = %q, want config maximum", err.Error())
	}
}

func TestCompletionGate_MissingAgenticTaskIDFailsClosed(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)

	err := NewGate(store, ModeVerification).Validate(ctx, completionQueueTask(0, time.Now().UTC()), 0)
	if !errors.Is(err, ErrMissingTaskID) {
		t.Fatalf("Validate error = %v, want ErrMissingTaskID", err)
	}
}

func TestCompletionGate_BlocksFailedVerification(t *testing.T) {
	assertCompletionGateBlocksVerdict(t, agentic.VerificationVerdictFailed)
}

func TestCompletionGate_BlocksInconclusiveVerification(t *testing.T) {
	assertCompletionGateBlocksVerdict(t, agentic.VerificationVerdictInconclusive)
}

func TestCompletionGate_UsesLatestRelevantVerificationRun(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().UTC()
	createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(-time.Second))

	decision, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Passed || !strings.Contains(decision.Reason, "stale verifier") {
		t.Fatalf("decision = %+v, want stale verifier block", decision)
	}

	run := createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))
	decision, err = NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID)
	if err != nil {
		t.Fatalf("Evaluate after fresh verifier: %v", err)
	}
	if !decision.Passed || decision.VerificationRunID != run.ID {
		t.Fatalf("decision = %+v, want latest fresh verifier %d", decision, run.ID)
	}
}

func TestCompletionGate_NonTerminalReceiptBlocksCompletion(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))
	createCompletionTestReceipt(t, ctx, store, task.ID, agentic.ReceiptStatusStarted)

	decision, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Passed || !strings.Contains(decision.Reason, "non-terminal receipt") {
		t.Fatalf("decision = %+v, want non-terminal receipt block", decision)
	}
}

func TestCompletionGate_CompletionWarningBlocksCompletion(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	run := createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))

	gate := NewGate(store, ModeVerification, WithCompletionContextProvider(completionContextProviderFunc(
		func(context.Context, daemon.Task, int64) (CompletionContext, error) {
			return CompletionContext{CompletionWarning: "unsupported_verification_success_claim"}, nil
		},
	)))
	decision, err := gate.Evaluate(ctx, completionQueueTask(task.ID, started), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Passed || decision.Status != agentic.CompletionGateStatusBlocked || decision.VerificationRunID != run.ID || !strings.Contains(decision.Reason, "completion warning") {
		t.Fatalf("decision = %+v, want completion warning block with verifier %d", decision, run.ID)
	}
}

func TestCompletionGate_TerminalReceiptsDoNotBypassVerifier(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	createCompletionTestReceipt(t, ctx, store, task.ID, agentic.ReceiptStatusFailed)
	createCompletionTestReceipt(t, ctx, store, task.ID, agentic.ReceiptStatusDenied)
	createCompletionTestReceipt(t, ctx, store, task.ID, agentic.ReceiptStatusApprovalRequired)

	decision, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, time.Now().UTC()), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Passed || !strings.Contains(decision.Reason, "missing verifier") {
		t.Fatalf("decision = %+v, want verifier requirement to remain blocking", decision)
	}
}

func TestCompletionGate_NoAutonomousSideEffects(t *testing.T) {
	ctx := context.Background()
	db, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))

	if _, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	for _, table := range []string{"approval_requests", "memory_updates", "followups"} {
		if got := completionCountRows(t, db, table); got != 0 {
			t.Fatalf("%s = %d, want 0", table, got)
		}
	}
}

func TestCompletionGate_ReceiptSummaryIncludesOptionalCompletionContext(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))
	createCompletionTestReceipt(t, ctx, store, task.ID, agentic.ReceiptStatusSucceeded)
	observed := false

	gate := NewGate(store, ModeVerification, WithCompletionContextProvider(completionContextProviderFunc(
		func(context.Context, daemon.Task, int64) (CompletionContext, error) {
			return CompletionContext{
				VerificationHint:     true,
				VerificationObserved: &observed,
				VerificationCommand:  "go test ./internal/agentic/completion -count=1",
				CompletionWarning:    "final_response_reports_incomplete",
				UserInputRequired:    true,
				EditIntent:           true,
				EditObserved:         &observed,
				ReasoningEffort:      "high",
				ReasoningEffortMode:  "auto",
				ConditionalSkillMatches: []ConditionalSkillMatch{
					{SkillName: "go-review", Pattern: "internal/**/*.go", Path: "internal/skill/skill.go", Source: "claude-skill", TrustLevel: "local_compatible", External: false},
				},
				SkillCatalogReceipts: []SkillCatalogReceipt{{
					Tool:              "skill_catalog",
					Action:            "recommend",
					ReadOnly:          true,
					RegistryAvailable: true,
					TotalSkills:       2,
					ReturnedSkills:    1,
					MaxResults:        5,
					Query:             "review code",
				}},
				SkillExecutionReceipts: []SkillExecutionReceipt{{
					Tool:                "skill",
					Action:              "execute",
					Skill:               "review-pr",
					Status:              "completed",
					Provider:            "openai-responses",
					Model:               "gpt-5.5",
					ReasoningEffort:     "high",
					ReasoningEffortMode: "manual",
					PermissionMode:      "bypass",
					MaxIterations:       8,
					RequiredTools:       []string{"read_file"},
					AvailableTools:      []string{"read_file", "grep"},
					ToolFilterApplied:   true,
					BaseDir:             "/tmp/skills/review-pr",
					Source:              "codex-plugin-skill",
					TrustLevel:          "plugin_cache",
					External:            true,
					UserInvocable:       true,
				}},
				CommandCatalogReceipts: []CommandCatalogReceipt{{
					Tool:                  "command_catalog",
					Action:                "recommend",
					ReadOnly:              true,
					RegistryAvailable:     true,
					ExecutionAvailable:    false,
					ExecutionPolicy:       "metadata_only",
					TotalCommands:         12,
					ReturnedCommands:      1,
					ExecutableCommands:    11,
					ModelCallableCommands: 1,
					MaxResults:            2,
					Query:                 "commands",
					FollowupTool:          "skill",
				}},
				ToolSearchReceipts: []ToolSearchReceipt{{
					Tool:               "tool_search",
					Action:             "search",
					ReadOnly:           true,
					RegistryAvailable:  true,
					ExecutionAvailable: false,
					ExecutionPolicy:    "metadata_only",
					TotalTools:         12,
					ReturnedMatches:    1,
					DeferredMatches:    1,
					MaxResults:         3,
					Query:              "task",
				}},
				ControlToolReceipts: []ControlToolReceipt{{
					Tool:            "task_create",
					Action:          "create",
					Persistent:      true,
					QueueBacked:     true,
					ExecutionPolicy: "daemon_queue_enqueue",
					FollowupTool:    "task_monitor",
					TaskID:          7,
					Status:          "pending",
				}, {
					Tool:            "agentic_delegate_enqueue",
					Action:          "enqueue",
					Persistent:      true,
					ExecutionPolicy: "agentic_delegation_enqueue",
					FollowupTool:    "agentic_delegate_status",
					ParentTaskID:    3,
					ChildTaskID:     9,
					QueueTaskID:     44,
					DecisionID:      7,
					DecisionStatus:  "enqueued",
					Enqueued:        true,
				}, {
					Tool:            "process_monitor",
					Action:          "monitor",
					ReadOnly:        true,
					ExecutionPolicy: "session_process_observation",
					CommandIntent:   "focused_verify",
					IntentSource:    "explicit",
					ProcessID:       4,
					Status:          "completed",
					Terminal:        true,
					TimedOut:        true,
					TimeoutMS:       50,
					Found:           true,
					TailBytes:       4000,
					StdoutRawBytes:  5,
					StderrRawBytes:  4,
					StderrTruncated: true,
					CWD:             "/tmp/work",
				}, {
					Tool:            "ask_user_question",
					Action:          "request",
					ReadOnly:        true,
					ExecutionPolicy: "user_input_request",
					QuestionChars:   13,
					OptionCount:     2,
					AllowFreeText:   false,
					TimeoutSeconds:  120,
				}},
				DiagnosticDeltaReceipts: []DiagnosticDeltaReceipt{{
					Tool:               "code_symbols",
					Action:             "diagnostics_delta",
					ReadOnly:           true,
					ExecutionPolicy:    "code_symbols_observation",
					Operation:          "diagnostics_delta",
					Status:             "new_diagnostics_found",
					Language:           "go",
					FilePath:           "internal/parser/parser.go",
					Count:              1,
					ErrorCount:         1,
					NewDiagnosticCount: 1,
				}},
				CorrectionAttempts:    1,
				CorrectionMaxAttempts: 1,
				CorrectionAttemptDetails: []CorrectionAttemptReceipt{{
					Attempt:           1,
					Decision:          "retry_smaller_scope",
					Reason:            "final_response_reports_incomplete",
					Status:            "failed",
					FailureFamily:     "workflow_error",
					CompletionWarning: "final_response_reports_incomplete",
				}},
				RetryDecision: "retry_smaller_scope",
				RetryReason:   "final_response_reports_incomplete",
			}, nil
		},
	)))

	if _, err := gate.Evaluate(ctx, completionQueueTask(task.ID, started), task.ID); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	gates, err := store.ListCompletionGatesByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListCompletionGatesByTask: %v", err)
	}
	if len(gates) != 1 {
		t.Fatalf("completion gates = %d, want 1", len(gates))
	}

	var summary map[string]any
	if err := json.Unmarshal([]byte(gates[0].ReceiptSummaryJSON), &summary); err != nil {
		t.Fatalf("summary json: %v", err)
	}
	if summary[agentic.ReceiptStatusSucceeded] != float64(1) {
		t.Fatalf("succeeded count = %v, want 1; summary=%v", summary[agentic.ReceiptStatusSucceeded], summary)
	}
	if summary["verification_hint"] != true {
		t.Fatalf("verification_hint = %v, want true; summary=%v", summary["verification_hint"], summary)
	}
	if summary["verification_observed"] != false {
		t.Fatalf("verification_observed = %v, want false; summary=%v", summary["verification_observed"], summary)
	}
	if summary["verification_command"] != "go test ./internal/agentic/completion -count=1" {
		t.Fatalf("verification_command = %v; summary=%v", summary["verification_command"], summary)
	}
	if summary["completion_warning"] != "final_response_reports_incomplete" {
		t.Fatalf("completion_warning = %v; summary=%v", summary["completion_warning"], summary)
	}
	if summary["user_input_required"] != true {
		t.Fatalf("user_input_required = %v; summary=%v", summary["user_input_required"], summary)
	}
	if summary["edit_intent"] != true || summary["edit_observed"] != false {
		t.Fatalf("edit fields missing: summary=%v", summary)
	}
	if summary["reasoning_effort"] != "high" || summary["reasoning_effort_mode"] != "auto" {
		t.Fatalf("reasoning fields missing: summary=%v", summary)
	}
	matches, ok := summary["conditional_skill_matches"].([]any)
	if !ok || len(matches) != 1 {
		t.Fatalf("conditional_skill_matches missing: summary=%v", summary)
	}
	match, ok := matches[0].(map[string]any)
	if !ok || match["source"] != "claude-skill" || match["trust_level"] != "local_compatible" || match["external"] != false {
		t.Fatalf("conditional_skill_matches trust metadata missing: match=%v summary=%v", matches[0], summary)
	}
	if summary["retry_decision"] != "retry_smaller_scope" || summary["retry_reason"] != "final_response_reports_incomplete" {
		t.Fatalf("retry fields missing: summary=%v", summary)
	}
	receipts, ok := summary["skill_catalog_receipts"].([]any)
	if !ok || len(receipts) != 1 {
		t.Fatalf("skill_catalog_receipts missing: summary=%v", summary)
	}
	skillExecutionReceipts, ok := summary["skill_execution_receipts"].([]any)
	if !ok || len(skillExecutionReceipts) != 1 {
		t.Fatalf("skill_execution_receipts missing: summary=%v", summary)
	}
	skillExecutionReceipt, ok := skillExecutionReceipts[0].(map[string]any)
	if !ok || skillExecutionReceipt["skill"] != "review-pr" || skillExecutionReceipt["model"] != "gpt-5.5" || skillExecutionReceipt["tool_filter_applied"] != true {
		t.Fatalf("skill execution receipt missing fields: receipt=%v summary=%v", skillExecutionReceipts[0], summary)
	}
	commandReceipts, ok := summary["command_catalog_receipts"].([]any)
	if !ok || len(commandReceipts) != 1 {
		t.Fatalf("command_catalog_receipts missing: summary=%v", summary)
	}
	commandReceipt, ok := commandReceipts[0].(map[string]any)
	if !ok || commandReceipt["executable_commands"] != float64(11) || commandReceipt["model_callable_commands"] != float64(1) {
		t.Fatalf("command_catalog_receipt execution counts missing: receipt=%v summary=%v", commandReceipts[0], summary)
	}
	if commandReceipt["followup_tool"] != "skill" {
		t.Fatalf("command_catalog_receipt followup missing: receipt=%v summary=%v", commandReceipts[0], summary)
	}
	toolSearchReceipts, ok := summary["tool_search_receipts"].([]any)
	if !ok || len(toolSearchReceipts) != 1 {
		t.Fatalf("tool_search_receipts missing: summary=%v", summary)
	}
	controlToolReceipts, ok := summary["control_tool_receipts"].([]any)
	if !ok || len(controlToolReceipts) != 4 {
		t.Fatalf("control_tool_receipts missing: summary=%v", summary)
	}
	delegateReceipt, ok := controlToolReceipts[1].(map[string]any)
	if !ok || delegateReceipt["tool"] != "agentic_delegate_enqueue" || delegateReceipt["parent_task_id"] != float64(3) || delegateReceipt["child_task_id"] != float64(9) || delegateReceipt["queue_task_id"] != float64(44) || delegateReceipt["decision_id"] != float64(7) || delegateReceipt["decision_status"] != "enqueued" || delegateReceipt["enqueued"] != true {
		t.Fatalf("delegation control receipt missing fields: receipt=%v summary=%v", controlToolReceipts[1], summary)
	}
	processReceipt, ok := controlToolReceipts[2].(map[string]any)
	if !ok || processReceipt["tool"] != "process_monitor" || processReceipt["command_intent"] != "focused_verify" || processReceipt["intent_source"] != "explicit" || processReceipt["timed_out"] != true || processReceipt["timeout_ms"] != float64(50) || processReceipt["process_id"] != float64(4) || processReceipt["tail_bytes"] != float64(4000) || processReceipt["stdout_raw_bytes"] != float64(5) || processReceipt["stderr_truncated"] != true || processReceipt["cwd"] != "/tmp/work" {
		t.Fatalf("process control receipt missing fields: receipt=%v summary=%v", controlToolReceipts[2], summary)
	}
	askReceipt, ok := controlToolReceipts[3].(map[string]any)
	if !ok || askReceipt["tool"] != "ask_user_question" || askReceipt["question_chars"] != float64(13) || askReceipt["option_count"] != float64(2) || askReceipt["timeout_seconds"] != float64(120) {
		t.Fatalf("ask_user_question control receipt missing fields: receipt=%v summary=%v", controlToolReceipts[3], summary)
	}
	diagnosticDeltaReceipts, ok := summary["diagnostic_delta_receipts"].([]any)
	if !ok || len(diagnosticDeltaReceipts) != 1 {
		t.Fatalf("diagnostic_delta_receipts missing: summary=%v", summary)
	}
	diagnosticDeltaReceipt, ok := diagnosticDeltaReceipts[0].(map[string]any)
	if !ok || diagnosticDeltaReceipt["tool"] != "code_symbols" || diagnosticDeltaReceipt["operation"] != "diagnostics_delta" || diagnosticDeltaReceipt["new_diagnostic_count"] != float64(1) {
		t.Fatalf("diagnostic_delta_receipt missing fields: receipt=%v summary=%v", diagnosticDeltaReceipts[0], summary)
	}
	if summary["correction_attempts"] != float64(1) || summary["correction_max_attempts"] != float64(1) {
		t.Fatalf("correction budget fields missing: summary=%v", summary)
	}
	attemptDetails, ok := summary["correction_attempt_details"].([]any)
	if !ok || len(attemptDetails) != 1 {
		t.Fatalf("correction attempt details missing: summary=%v", summary)
	}
	attemptDetail, ok := attemptDetails[0].(map[string]any)
	if !ok || attemptDetail["attempt"] != float64(1) || attemptDetail["failure_family"] != "workflow_error" {
		t.Fatalf("correction attempt detail missing fields: detail=%v summary=%v", attemptDetails[0], summary)
	}
}

func TestCompletionGate_ReceiptSummaryOmitsEmptyCompletionContext(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))

	if _, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	gates, err := store.ListCompletionGatesByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListCompletionGatesByTask: %v", err)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(gates[0].ReceiptSummaryJSON), &summary); err != nil {
		t.Fatalf("summary json: %v", err)
	}
	if _, ok := summary["verification_hint"]; ok {
		t.Fatalf("verification_hint should be omitted for empty context: %v", summary)
	}
	if _, ok := summary[agentic.ReceiptStatusStarted]; !ok {
		t.Fatalf("receipt count keys should remain present: %v", summary)
	}
}

func TestCompletionGate_ContextProviderErrorDoesNotBlockGate(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	run := createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))

	gate := NewGate(store, ModeVerification, WithCompletionContextProvider(completionContextProviderFunc(
		func(context.Context, daemon.Task, int64) (CompletionContext, error) {
			return CompletionContext{}, errors.New("context unavailable")
		},
	)))
	decision, err := gate.Evaluate(ctx, completionQueueTask(task.ID, started), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !decision.Passed || decision.VerificationRunID != run.ID {
		t.Fatalf("decision = %+v, want pass despite optional context failure", decision)
	}
}

func assertCompletionGateBlocksVerdict(t *testing.T, verdict string) {
	t.Helper()
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	run := createCompletionTestVerificationAt(t, ctx, store, task.ID, verdict, started.Add(time.Second))

	decision, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Passed || decision.VerificationRunID != run.ID || !strings.Contains(decision.Reason, verdict) {
		t.Fatalf("decision = %+v, want %s verifier block", decision, verdict)
	}
}

type completionContextProviderFunc func(context.Context, daemon.Task, int64) (CompletionContext, error)

func (f completionContextProviderFunc) CompletionContext(ctx context.Context, task daemon.Task, agenticTaskID int64) (CompletionContext, error) {
	return f(ctx, task, agenticTaskID)
}

func newCompletionTestStore(t *testing.T) (*sql.DB, *agentic.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := agentic.InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	if _, err := daemon.NewApprovalStore(db); err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	return db, agentic.NewStore(db)
}

func createCompletionTestTask(t *testing.T, ctx context.Context, store *agentic.Store) *agentic.AgenticTask {
	t.Helper()
	task, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "completion gated task",
		Prompt:             "verify before completion",
		Status:             agentic.TaskStatusRunning,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return task
}

func createCompletionTestVerificationAt(t *testing.T, ctx context.Context, store *agentic.Store, taskID int64, verdict string, createdAt time.Time) *agentic.VerificationRun {
	t.Helper()
	run, err := store.CreateVerificationRun(ctx, agentic.VerificationRun{
		TaskID:           taskID,
		CriteriaJSON:     `["done means verified"]`,
		EvidenceRefsJSON: `["receipt:1"]`,
		Verdict:          verdict,
		Reason:           verdict + " verifier",
		CreatedAt:        createdAt,
	})
	if err != nil {
		t.Fatalf("CreateVerificationRun: %v", err)
	}
	return run
}

func createCompletionTestReceipt(t *testing.T, ctx context.Context, store *agentic.Store, taskID int64, status string) {
	t.Helper()
	if _, err := store.CreateToolActionReceipt(ctx, agentic.ToolActionReceipt{
		TaskID:    taskID,
		ToolName:  "bash",
		InputHash: "input",
		Status:    status,
	}); err != nil {
		t.Fatalf("CreateToolActionReceipt: %v", err)
	}
}

func completionQueueTask(taskID int64, startedAt time.Time) daemon.Task {
	return daemon.Task{
		ID:        taskID + 100,
		Status:    daemon.StatusRunning,
		StartedAt: startedAt,
		Payload: daemon.EncodeTaskPayload(daemon.TaskPayload{
			Prompt:                "verify before completion",
			AgenticCompletionGate: ModeVerification,
		}),
	}
}

func assertCompletionGateCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	if got := completionCountRows(t, db, "completion_gates"); got != want {
		t.Fatalf("completion_gates = %d, want %d", got, want)
	}
}

func completionCountRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
