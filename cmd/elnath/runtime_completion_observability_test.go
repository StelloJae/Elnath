package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agentic"
	agenticcompletion "github.com/stello/elnath/internal/agentic/completion"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/orchestrator"
)

func TestCompletionContractSummaryRecordsMissingVerification(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("check the project status and run tests"),
			llm.NewAssistantMessage("I changed the code."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(&orchestrator.RoutingContext{VerificationHint: true}, orchestrator.WorkflowConfig{}, result)

	if !summary.VerificationHint {
		t.Fatal("VerificationHint = false, want true")
	}
	if summary.VerificationObserved == nil {
		t.Fatal("VerificationObserved = nil, want explicit false")
	}
	if *summary.VerificationObserved {
		t.Fatal("VerificationObserved = true, want false")
	}
	if summary.RetryDecision != completionRetryDecisionRunVerification || summary.RetryReason != "verification_hint_not_observed" {
		t.Fatalf("retry plan = %q/%q, want run_verification/verification_hint_not_observed", summary.RetryDecision, summary.RetryReason)
	}
}

func TestCompletionContractSummaryDetectsBashVerification(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("check the project status and run tests"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "bash-1", Name: "bash", Input: json.RawMessage(`{"command":"go test ./internal/llm -count=1"}`)},
			}},
			llm.NewToolResultMessage("bash-1", "ok", false),
			llm.NewAssistantMessage("Done."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(&orchestrator.RoutingContext{VerificationHint: true}, orchestrator.WorkflowConfig{}, result)

	if summary.VerificationObserved == nil || !*summary.VerificationObserved {
		t.Fatalf("VerificationObserved = %v, want true", summary.VerificationObserved)
	}
	if summary.VerificationCommand != "go test ./internal/llm -count=1" {
		t.Fatalf("VerificationCommand = %q", summary.VerificationCommand)
	}
	if summary.CompletionWarning != "" {
		t.Fatalf("CompletionWarning = %q, want empty", summary.CompletionWarning)
	}
	if summary.RetryDecision != "" || summary.RetryReason != "" {
		t.Fatalf("retry plan = %q/%q, want empty", summary.RetryDecision, summary.RetryReason)
	}
}

func TestCompletionContractSummaryDetectsIncompleteFinalResponse(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("check the project status and run tests"),
			llm.NewAssistantMessage("I could not finish the regression test before stopping."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(&orchestrator.RoutingContext{VerificationHint: true}, orchestrator.WorkflowConfig{}, result)

	if summary.CompletionWarning != "final_response_reports_incomplete" {
		t.Fatalf("CompletionWarning = %q, want final_response_reports_incomplete", summary.CompletionWarning)
	}
	if summary.RetryDecision != completionRetryDecisionRetrySmallerScope || summary.RetryReason != "final_response_reports_incomplete" {
		t.Fatalf("retry plan = %q/%q, want retry_smaller_scope/final_response_reports_incomplete", summary.RetryDecision, summary.RetryReason)
	}
}

func TestCompletionContractSummaryDetectsEditIntentWithoutMutation(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("fix the bug in the daemon and run tests"),
			llm.NewAssistantMessage("Done."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(&orchestrator.RoutingContext{VerificationHint: true}, orchestrator.WorkflowConfig{}, result)

	if !summary.EditIntent {
		t.Fatal("EditIntent = false, want true")
	}
	if summary.EditObserved == nil {
		t.Fatal("EditObserved = nil, want explicit false")
	}
	if *summary.EditObserved {
		t.Fatal("EditObserved = true, want false")
	}
	if summary.CompletionWarning != "edit_intent_without_mutation" {
		t.Fatalf("CompletionWarning = %q, want edit_intent_without_mutation", summary.CompletionWarning)
	}
	if summary.RetryDecision != completionRetryDecisionRetrySmallerScope || summary.RetryReason != "edit_intent_without_mutation" {
		t.Fatalf("retry plan = %q/%q, want retry_smaller_scope/edit_intent_without_mutation", summary.RetryDecision, summary.RetryReason)
	}
}

func TestCompletionContractSummaryDetectsEditToolMutation(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("fix the bug in the daemon and run tests"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "edit-1", Name: "edit_file", Input: json.RawMessage(`{"file_path":"internal/daemon/daemon.go","old_string":"old","new_string":"new"}`)},
			}},
			llm.NewToolResultMessage("edit-1", "ok", false),
			llm.NewAssistantMessage("Done."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(&orchestrator.RoutingContext{VerificationHint: true}, orchestrator.WorkflowConfig{}, result)

	if !summary.EditIntent {
		t.Fatal("EditIntent = false, want true")
	}
	if summary.EditObserved == nil || !*summary.EditObserved {
		t.Fatalf("EditObserved = %v, want true", summary.EditObserved)
	}
	if summary.CompletionWarning != "" {
		t.Fatalf("CompletionWarning = %q, want empty", summary.CompletionWarning)
	}
}

func TestCompletionContractSummaryRecordsReasoningConfig(t *testing.T) {
	result := &orchestrator.WorkflowResult{Messages: []llm.Message{llm.NewAssistantMessage("Done.")}, FinishReason: "stop"}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{
		ReasoningEffort:     "high",
		ReasoningEffortMode: "auto",
	}, result)

	if summary.ReasoningEffort != "high" || summary.ReasoningEffortMode != "auto" {
		t.Fatalf("reasoning = effort %q mode %q, want high/auto", summary.ReasoningEffort, summary.ReasoningEffortMode)
	}
}

func TestCompletionContractSummaryRecordsProviderCapabilities(t *testing.T) {
	summary := withProviderCapabilities(completionContractSummary{}, &capabilityCountingProvider{})

	if summary.ProviderName != "openai-responses" {
		t.Fatalf("ProviderName = %q", summary.ProviderName)
	}
	if summary.ProviderEffort != llm.ReasoningEffortNativeWithUnsupportedRetry {
		t.Fatalf("ProviderEffort = %q", summary.ProviderEffort)
	}
	if !strings.Contains(summary.ProviderEffortNote, "retry_without_reasoning") {
		t.Fatalf("ProviderEffortNote = %q", summary.ProviderEffortNote)
	}
}

func TestRecordOutcomePersistsCompletionObservability(t *testing.T) {
	ctx := context.Background()
	rt := newTestExecutionRuntime(t, &countingProvider{})
	observed := false

	rt.recordOutcome(ctx, outcomeInput{
		routeCtx:     &orchestrator.RoutingContext{ProjectID: "elnath", VerificationHint: true},
		intent:       conversation.IntentComplexTask,
		workflow:     "single",
		finishReason: "stop",
		success:      true,
		userInput:    "fix regression and run tests",
		completion: completionContractSummary{
			VerificationHint:     true,
			VerificationObserved: &observed,
			CompletionWarning:    "final_response_reports_incomplete",
			ReasoningEffort:      "high",
			ReasoningEffortMode:  "auto",
			ProviderName:         "openai-responses",
			ProviderEffort:       llm.ReasoningEffortNativeWithUnsupportedRetry,
			ProviderEffortNote:   "retry_without_reasoning_on_400_or_422_unsupported_effort",
			RetryDecision:        completionRetryDecisionRetrySmallerScope,
			RetryReason:          "final_response_reports_incomplete",
		},
	})

	records, err := rt.outcomeStore.ForProject("elnath", 1)
	if err != nil {
		t.Fatalf("ForProject: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.Intent != string(conversation.IntentComplexTask) || rec.Workflow != "single" {
		t.Fatalf("unexpected outcome identity: %+v", rec)
	}
	assertCompletionOutcome(t, rec)
}

func TestCompletionGateContextProviderConsumesRuntimeSummary(t *testing.T) {
	ctx := context.Background()
	rt := newTestExecutionRuntime(t, &countingProvider{})
	observed := false

	rt.rememberAgenticCompletionContext(42, completionContractSummary{
		VerificationHint:     true,
		VerificationObserved: &observed,
		VerificationCommand:  "go test ./cmd/elnath -count=1",
		CompletionWarning:    "final_response_reports_incomplete",
		EditIntent:           true,
		EditObserved:         &observed,
		ReasoningEffort:      "high",
		ReasoningEffortMode:  "auto",
		ProviderName:         "openai-responses",
		ProviderEffort:       llm.ReasoningEffortNativeWithUnsupportedRetry,
		ProviderEffortNote:   "retry_without_reasoning_on_400_or_422_unsupported_effort",
		RetryDecision:        completionRetryDecisionRetrySmallerScope,
		RetryReason:          "final_response_reports_incomplete",
	})

	summary, err := rt.CompletionContext(ctx, daemon.Task{ID: 7}, 42)
	if err != nil {
		t.Fatalf("CompletionContext: %v", err)
	}
	if !summary.VerificationHint {
		t.Fatal("VerificationHint = false, want true")
	}
	if summary.VerificationObserved == nil || *summary.VerificationObserved {
		t.Fatalf("VerificationObserved = %v, want explicit false", summary.VerificationObserved)
	}
	if summary.VerificationCommand != "go test ./cmd/elnath -count=1" {
		t.Fatalf("VerificationCommand = %q", summary.VerificationCommand)
	}
	if summary.CompletionWarning != "final_response_reports_incomplete" {
		t.Fatalf("CompletionWarning = %q", summary.CompletionWarning)
	}
	if !summary.EditIntent || summary.EditObserved == nil || *summary.EditObserved {
		t.Fatalf("edit context = intent %v observed %v, want true/false", summary.EditIntent, summary.EditObserved)
	}
	if summary.ReasoningEffort != "high" || summary.ReasoningEffortMode != "auto" {
		t.Fatalf("reasoning = effort %q mode %q, want high/auto", summary.ReasoningEffort, summary.ReasoningEffortMode)
	}
	if summary.ProviderName != "openai-responses" || summary.ProviderEffort != llm.ReasoningEffortNativeWithUnsupportedRetry || !strings.Contains(summary.ProviderEffortNote, "retry_without_reasoning") {
		t.Fatalf("provider context = name %q effort %q note %q", summary.ProviderName, summary.ProviderEffort, summary.ProviderEffortNote)
	}
	if summary.RetryDecision != completionRetryDecisionRetrySmallerScope || summary.RetryReason != "final_response_reports_incomplete" {
		t.Fatalf("retry plan = %q/%q", summary.RetryDecision, summary.RetryReason)
	}

	empty, err := rt.CompletionContext(ctx, daemon.Task{ID: 7}, 42)
	if err != nil {
		t.Fatalf("CompletionContext second call: %v", err)
	}
	if empty.VerificationHint || empty.VerificationObserved != nil || empty.CompletionWarning != "" {
		t.Fatalf("context should be consumed after first read: %+v", empty)
	}
}

func TestCompletionGateReceiptSummaryIncludesRuntimeContext(t *testing.T) {
	ctx := context.Background()
	rt := newTestExecutionRuntime(t, &countingProvider{})
	task, err := rt.agenticStore.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "completion gated runtime task",
		Prompt:             "fix and verify",
		Status:             agentic.TaskStatusRunning,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	started := time.Now().Add(-time.Minute).UTC()
	run, err := rt.agenticStore.CreateVerificationRun(ctx, agentic.VerificationRun{
		TaskID:           task.ID,
		CriteriaJSON:     `["verified"]`,
		EvidenceRefsJSON: `["receipt:1"]`,
		Verdict:          agentic.VerificationVerdictPassed,
		Reason:           "verified",
		CreatedAt:        started.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("CreateVerificationRun: %v", err)
	}
	if _, err := rt.agenticStore.CreateToolActionReceipt(ctx, agentic.ToolActionReceipt{
		TaskID:    task.ID,
		ToolName:  "bash",
		InputHash: "input",
		Status:    agentic.ReceiptStatusSucceeded,
	}); err != nil {
		t.Fatalf("CreateToolActionReceipt: %v", err)
	}
	observed := false
	rt.rememberAgenticCompletionContext(task.ID, completionContractSummary{
		VerificationHint:     true,
		VerificationObserved: &observed,
		VerificationCommand:  "go test ./cmd/elnath -count=1",
		CompletionWarning:    "final_response_reports_incomplete",
		EditIntent:           true,
		EditObserved:         &observed,
		ReasoningEffort:      "medium",
		ReasoningEffortMode:  "manual",
		ProviderName:         "openai-responses",
		ProviderEffort:       llm.ReasoningEffortNativeWithUnsupportedRetry,
		ProviderEffortNote:   "retry_without_reasoning_on_400_or_422_unsupported_effort",
		RetryDecision:        completionRetryDecisionRetrySmallerScope,
		RetryReason:          "final_response_reports_incomplete",
	})

	gate := agenticcompletion.NewGate(rt.agenticStore, agenticcompletion.ModeVerification,
		agenticcompletion.WithCompletionContextProvider(rt))
	decision, err := gate.Evaluate(ctx, daemon.Task{
		ID:        101,
		Status:    daemon.StatusRunning,
		StartedAt: started,
		Payload: daemon.EncodeTaskPayload(daemon.TaskPayload{
			Prompt:                "fix and verify",
			AgenticCompletionGate: agenticcompletion.ModeVerification,
		}),
	}, task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !decision.Passed || decision.VerificationRunID != run.ID {
		t.Fatalf("decision = %+v, want passed with verification run %d", decision, run.ID)
	}
	gates, err := rt.agenticStore.ListCompletionGatesByTask(ctx, task.ID)
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
	if summary["verification_hint"] != true || summary["verification_observed"] != false {
		t.Fatalf("verification context missing from gate summary: %v", summary)
	}
	if summary["verification_command"] != "go test ./cmd/elnath -count=1" {
		t.Fatalf("verification command missing from gate summary: %v", summary)
	}
	if summary["completion_warning"] != "final_response_reports_incomplete" {
		t.Fatalf("completion warning missing from gate summary: %v", summary)
	}
	if summary["edit_intent"] != true || summary["edit_observed"] != false {
		t.Fatalf("edit context missing from gate summary: %v", summary)
	}
	if summary["reasoning_effort"] != "medium" || summary["reasoning_effort_mode"] != "manual" {
		t.Fatalf("reasoning context missing from gate summary: %v", summary)
	}
	if summary["provider_name"] != "openai-responses" || summary["provider_effort"] != llm.ReasoningEffortNativeWithUnsupportedRetry {
		t.Fatalf("provider context missing from gate summary: %v", summary)
	}
	if note, _ := summary["provider_effort_note"].(string); !strings.Contains(note, "retry_without_reasoning") {
		t.Fatalf("provider note missing from gate summary: %v", summary)
	}
	if summary["retry_decision"] != completionRetryDecisionRetrySmallerScope || summary["retry_reason"] != "final_response_reports_incomplete" {
		t.Fatalf("retry context missing from gate summary: %v", summary)
	}
}

func assertCompletionOutcome(t *testing.T, rec learning.OutcomeRecord) {
	t.Helper()
	if !rec.VerificationHint {
		t.Fatal("VerificationHint = false, want true")
	}
	if rec.VerificationObserved == nil {
		t.Fatal("VerificationObserved = nil, want explicit false")
	}
	if *rec.VerificationObserved {
		t.Fatal("VerificationObserved = true, want false")
	}
	if rec.CompletionWarning != "final_response_reports_incomplete" {
		t.Fatalf("CompletionWarning = %q", rec.CompletionWarning)
	}
	if rec.ReasoningEffort != "high" || rec.ReasoningEffortMode != "auto" {
		t.Fatalf("reasoning = effort %q mode %q", rec.ReasoningEffort, rec.ReasoningEffortMode)
	}
	if rec.ProviderName != "openai-responses" || rec.ProviderEffort != llm.ReasoningEffortNativeWithUnsupportedRetry || !strings.Contains(rec.ProviderEffortNote, "retry_without_reasoning") {
		t.Fatalf("provider = name %q effort %q note %q", rec.ProviderName, rec.ProviderEffort, rec.ProviderEffortNote)
	}
	if rec.RetryDecision != completionRetryDecisionRetrySmallerScope || rec.RetryReason != "final_response_reports_incomplete" {
		t.Fatalf("retry = decision %q reason %q", rec.RetryDecision, rec.RetryReason)
	}
}
