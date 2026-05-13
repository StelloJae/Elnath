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

func TestCompletionContractSummaryDetectsFailedBashVerification(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("check the project status and run tests"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "bash-1", Name: "bash", Input: json.RawMessage(`{"command":"go test ./internal/llm -count=1"}`)},
			}},
			llm.NewToolResultMessage("bash-1", "FAIL", true),
			llm.NewAssistantMessage("Done."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(&orchestrator.RoutingContext{VerificationHint: true}, orchestrator.WorkflowConfig{}, result)

	if summary.VerificationObserved == nil || !*summary.VerificationObserved {
		t.Fatalf("VerificationObserved = %v, want true for executed verification command", summary.VerificationObserved)
	}
	if summary.VerificationCommand != "go test ./internal/llm -count=1" {
		t.Fatalf("VerificationCommand = %q", summary.VerificationCommand)
	}
	if summary.CompletionWarning != "verification_command_failed" {
		t.Fatalf("CompletionWarning = %q, want verification_command_failed", summary.CompletionWarning)
	}
	if summary.RetryDecision != completionRetryDecisionRetrySmallerScope || summary.RetryReason != "verification_command_failed" {
		t.Fatalf("retry plan = %q/%q, want retry_smaller_scope/verification_command_failed", summary.RetryDecision, summary.RetryReason)
	}
}

func TestCompletionContractSummaryWarnsOnUnsupportedVerificationSuccessClaim(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("fix the bug in the daemon"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "edit-1", Name: "edit_file", Input: json.RawMessage(`{"file_path":"internal/daemon/daemon.go","old_string":"old","new_string":"new"}`)},
			}},
			llm.NewToolResultMessage("edit-1", "ok", false),
			llm.NewAssistantMessage("Done. All tests pass."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if summary.VerificationCommand != "" {
		t.Fatalf("VerificationCommand = %q, want empty", summary.VerificationCommand)
	}
	if summary.CompletionWarning != "unsupported_verification_success_claim" {
		t.Fatalf("CompletionWarning = %q, want unsupported_verification_success_claim", summary.CompletionWarning)
	}
	if summary.RetryDecision != completionRetryDecisionRetrySmallerScope || summary.RetryReason != "unsupported_verification_success_claim" {
		t.Fatalf("retry plan = %q/%q, want retry_smaller_scope/unsupported_verification_success_claim", summary.RetryDecision, summary.RetryReason)
	}
}

func TestCompletionContractSummaryUsesLatestBashVerificationResult(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("check the project status and run tests"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "bash-1", Name: "bash", Input: json.RawMessage(`{"command":"go test ./internal/llm -count=1"}`)},
			}},
			llm.NewToolResultMessage("bash-1", "FAIL", true),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "bash-2", Name: "bash", Input: json.RawMessage(`{"command":"go test ./internal/llm -count=1"}`)},
			}},
			llm.NewToolResultMessage("bash-2", "ok", false),
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
		t.Fatalf("CompletionWarning = %q, want latest passing verification to clear warning", summary.CompletionWarning)
	}
	if summary.RetryDecision != "" || summary.RetryReason != "" {
		t.Fatalf("retry plan = %q/%q, want empty after latest passing verification", summary.RetryDecision, summary.RetryReason)
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

func TestCompletionContractSummaryDetectsWorktreeRunMutation(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("fix the bug in a managed worktree and run tests"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "worktree-run-1", Name: "worktree_run", Input: json.RawMessage(`{"worktree_id":"wt-1","command":"touch internal/daemon/.elnath-test-marker"}`)},
			}},
			llm.NewToolResultMessage("worktree-run-1", `{"exit_code":0}`, false),
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

func TestCompletionContractSummaryDoesNotCountVerificationOnlyWorktreeRunAsMutation(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("fix the bug in a managed worktree and run tests"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "worktree-run-1", Name: "worktree_run", Input: json.RawMessage(`{"worktree_id":"wt-1","command":"go test ./cmd/elnath -count=1"}`)},
			}},
			llm.NewToolResultMessage("worktree-run-1", `{"exit_code":0}`, false),
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
		t.Fatal("EditObserved = true, want verification-only worktree_run not counted as mutation")
	}
	if summary.CompletionWarning != "edit_intent_without_mutation" {
		t.Fatalf("CompletionWarning = %q, want edit_intent_without_mutation", summary.CompletionWarning)
	}
}

func TestCompletionContractSummaryDoesNotCountFailedWorktreeRunAsMutation(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("fix the bug in a managed worktree and run tests"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "worktree-run-1", Name: "worktree_run", Input: json.RawMessage(`{"worktree_id":"wt-1","command":"touch internal/daemon/.elnath-test-marker"}`)},
			}},
			llm.NewToolResultMessage("worktree-run-1", `{"exit_code":1}`, true),
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
		t.Fatal("EditObserved = true, want failed worktree_run not counted as mutation")
	}
	if summary.CompletionWarning != "edit_intent_without_mutation" {
		t.Fatalf("CompletionWarning = %q, want edit_intent_without_mutation", summary.CompletionWarning)
	}
	if summary.RetryDecision != completionRetryDecisionRetrySmallerScope || summary.RetryReason != "edit_intent_without_mutation" {
		t.Fatalf("retry plan = %q/%q, want retry_smaller_scope/edit_intent_without_mutation", summary.RetryDecision, summary.RetryReason)
	}
}

func TestCompletionContractSummaryDoesNotCountFailedEditToolAsMutation(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("fix the bug in the daemon and run tests"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "edit-1", Name: "edit_file", Input: json.RawMessage(`{"file_path":"internal/daemon/daemon.go","old_string":"old","new_string":"old"}`)},
			}},
			llm.NewToolResultMessage("edit-1", "edit_file: old_string and new_string are identical", true),
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
		t.Fatal("EditObserved = true, want failed edit tool not counted as mutation")
	}
	if summary.CompletionWarning != "edit_intent_without_mutation" {
		t.Fatalf("CompletionWarning = %q, want edit_intent_without_mutation", summary.CompletionWarning)
	}
	if summary.RetryDecision != completionRetryDecisionRetrySmallerScope || summary.RetryReason != "edit_intent_without_mutation" {
		t.Fatalf("retry plan = %q/%q, want retry_smaller_scope/edit_intent_without_mutation", summary.RetryDecision, summary.RetryReason)
	}
}

func TestCompletionContractSummaryRecordsReasoningConfig(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages:              []llm.Message{llm.NewAssistantMessage("Done.")},
		FinishReason:          "stop",
		ReasoningEffort:       "low",
		ReasoningEffortMode:   "auto",
		ReasoningEffortReason: "simple_keyword",
		LoadedDeferredTools:   []string{"mcp_github_issue"},
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{
		ReasoningEffort:     "high",
		ReasoningEffortMode: "auto",
	}, result)

	if summary.ReasoningEffort != "low" || summary.ReasoningEffortMode != "auto" || summary.ReasoningEffortReason != "simple_keyword" {
		t.Fatalf("reasoning = effort %q mode %q reason %q", summary.ReasoningEffort, summary.ReasoningEffortMode, summary.ReasoningEffortReason)
	}
	if len(summary.LoadedDeferredTools) != 1 || summary.LoadedDeferredTools[0] != "mcp_github_issue" {
		t.Fatalf("LoadedDeferredTools = %v", summary.LoadedDeferredTools)
	}
}

func TestCompletionContractSummaryRecordsConditionalSkillMatches(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("review internal/skill/skill.go"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "skill-1", Name: "skill_catalog", Input: json.RawMessage(`{"action":"match_paths","paths":["internal/skill/skill.go"]}`)},
			}},
			llm.NewToolResultMessage("skill-1", `{"action":"match_paths","matches":[{"skill_name":"go-review","pattern":"internal/**/*.go","path":"internal/skill/skill.go","source":"claude-skill","trust_level":"local_compatible","external":false}]}`, false),
			llm.NewAssistantMessage("Done."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if len(summary.ConditionalSkillMatches) != 1 {
		t.Fatalf("ConditionalSkillMatches = %#v, want one match", summary.ConditionalSkillMatches)
	}
	match := summary.ConditionalSkillMatches[0]
	if match.SkillName != "go-review" || match.Pattern != "internal/**/*.go" || match.Path != "internal/skill/skill.go" {
		t.Fatalf("match = %+v, want go-review/internal/**/*.go/internal/skill/skill.go", match)
	}
	if match.Source != "claude-skill" || match.TrustLevel != "local_compatible" || match.External {
		t.Fatalf("match trust metadata = %+v, want claude-skill/local_compatible/non-external", match)
	}
}

func TestCompletionContractSummaryRecordsSkillCatalogReceipt(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("find a matching skill"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "skill-1", Name: "skill_catalog", Input: json.RawMessage(`{"action":"recommend","query":"review code"}`)},
			}},
			llm.NewToolResultMessage("skill-1", `{"action":"recommend","query":"review code","skills":[],"receipt":{"tool":"skill_catalog","action":"recommend","read_only":true,"registry_available":true,"total_skills":2,"returned_skills":0,"trust_filter_applied":false,"max_results":5,"query":"review code"}}`, false),
			llm.NewAssistantMessage("Done."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if len(summary.SkillCatalogReceipts) != 1 {
		t.Fatalf("SkillCatalogReceipts = %#v, want one receipt", summary.SkillCatalogReceipts)
	}
	receipt := summary.SkillCatalogReceipts[0]
	if receipt.Tool != "skill_catalog" || receipt.Action != "recommend" || !receipt.ReadOnly || !receipt.RegistryAvailable {
		t.Fatalf("receipt identity = %+v", receipt)
	}
	if receipt.TotalSkills != 2 || receipt.ReturnedSkills != 0 || receipt.MaxResults != 5 || receipt.Query != "review code" {
		t.Fatalf("receipt counts/bounds = %+v", receipt)
	}
}

func TestCompletionContractSummaryRecordsCommandCatalogReceipt(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("find command metadata"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "command-1", Name: "command_catalog", Input: json.RawMessage(`{"action":"recommend","query":"reasoning effort"}`)},
			}},
			llm.NewToolResultMessage("command-1", `{"action":"recommend","query":"reasoning effort","commands":[],"receipt":{"tool":"command_catalog","action":"recommend","read_only":true,"registry_available":true,"execution_available":false,"execution_policy":"metadata_only","total_commands":12,"returned_commands":0,"include_hidden":false,"max_results":5,"query":"reasoning effort"}}`, false),
			llm.NewAssistantMessage("Done."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if len(summary.CommandCatalogReceipts) != 1 {
		t.Fatalf("CommandCatalogReceipts = %#v, want one receipt", summary.CommandCatalogReceipts)
	}
	receipt := summary.CommandCatalogReceipts[0]
	if receipt.Tool != "command_catalog" || receipt.Action != "recommend" || !receipt.ReadOnly || !receipt.RegistryAvailable {
		t.Fatalf("receipt identity = %+v", receipt)
	}
	if receipt.ExecutionAvailable || receipt.ExecutionPolicy != "metadata_only" {
		t.Fatalf("receipt execution boundary = %+v", receipt)
	}
	if receipt.TotalCommands != 12 || receipt.ReturnedCommands != 0 || receipt.MaxResults != 5 || receipt.Query != "reasoning effort" {
		t.Fatalf("receipt counts/bounds = %+v", receipt)
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
			VerificationHint:      true,
			VerificationObserved:  &observed,
			VerificationCommand:   "go test ./cmd/elnath -count=1",
			CompletionWarning:     "final_response_reports_incomplete",
			ReasoningEffort:       "high",
			ReasoningEffortMode:   "auto",
			ReasoningEffortReason: "work_keyword",
			ProviderName:          "openai-responses",
			ProviderEffort:        llm.ReasoningEffortNativeWithUnsupportedRetry,
			ProviderEffortNote:    "retry_without_reasoning_on_400_or_422_unsupported_effort",
			LoadedDeferredTools:   []string{"mcp_github_issue"},
			SkillCatalogReceipts: []completionSkillCatalogReceipt{{
				Tool:              "skill_catalog",
				Action:            "recommend",
				ReadOnly:          true,
				RegistryAvailable: true,
				TotalSkills:       2,
				ReturnedSkills:    1,
				MaxResults:        5,
				Query:             "review code",
			}},
			CommandCatalogReceipts: []completionCommandCatalogReceipt{{
				Tool:               "command_catalog",
				Action:             "recommend",
				ReadOnly:           true,
				RegistryAvailable:  true,
				ExecutionAvailable: false,
				ExecutionPolicy:    "metadata_only",
				TotalCommands:      12,
				ReturnedCommands:   1,
				MaxResults:         2,
				Query:              "commands",
			}},
			CorrectionAttempted:     true,
			CorrectionAttempts:      1,
			CorrectionMaxAttempts:   1,
			CorrectionDecision:      completionRetryDecisionRetrySmallerScope,
			CorrectionReason:        "final_response_reports_incomplete",
			CorrectionStatus:        "failed",
			CorrectionFailureFamily: "workflow_error",
			RetryDecision:           completionRetryDecisionRetrySmallerScope,
			RetryReason:             "final_response_reports_incomplete",
			ConditionalSkillMatches: []completionConditionalSkillMatch{
				{SkillName: "go-review", Pattern: "internal/**/*.go", Path: "internal/skill/skill.go", Source: "claude-skill", TrustLevel: "local_compatible", External: false},
			},
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
		VerificationHint:      true,
		VerificationObserved:  &observed,
		VerificationCommand:   "go test ./cmd/elnath -count=1",
		CompletionWarning:     "final_response_reports_incomplete",
		EditIntent:            true,
		EditObserved:          &observed,
		ReasoningEffort:       "high",
		ReasoningEffortMode:   "auto",
		ReasoningEffortReason: "work_keyword",
		ProviderName:          "openai-responses",
		ProviderEffort:        llm.ReasoningEffortNativeWithUnsupportedRetry,
		ProviderEffortNote:    "retry_without_reasoning_on_400_or_422_unsupported_effort",
		LoadedDeferredTools:   []string{"mcp_github_issue"},
		SkillCatalogReceipts: []completionSkillCatalogReceipt{{
			Tool:              "skill_catalog",
			Action:            "recommend",
			ReadOnly:          true,
			RegistryAvailable: true,
			TotalSkills:       2,
			ReturnedSkills:    1,
			MaxResults:        5,
			Query:             "review code",
		}},
		CommandCatalogReceipts: []completionCommandCatalogReceipt{{
			Tool:               "command_catalog",
			Action:             "recommend",
			ReadOnly:           true,
			RegistryAvailable:  true,
			ExecutionAvailable: false,
			ExecutionPolicy:    "metadata_only",
			TotalCommands:      12,
			ReturnedCommands:   1,
			MaxResults:         2,
			Query:              "commands",
		}},
		ConditionalSkillMatches: []completionConditionalSkillMatch{
			{SkillName: "go-review", Pattern: "internal/**/*.go", Path: "internal/skill/skill.go", Source: "claude-skill", TrustLevel: "local_compatible", External: false},
		},
		CorrectionAttempted:     true,
		CorrectionAttempts:      1,
		CorrectionMaxAttempts:   1,
		CorrectionDecision:      completionRetryDecisionRetrySmallerScope,
		CorrectionReason:        "final_response_reports_incomplete",
		CorrectionStatus:        "failed",
		CorrectionFailureFamily: "workflow_error",
		RetryDecision:           completionRetryDecisionRetrySmallerScope,
		RetryReason:             "final_response_reports_incomplete",
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
	if summary.ReasoningEffortReason != "work_keyword" {
		t.Fatalf("ReasoningEffortReason = %q", summary.ReasoningEffortReason)
	}
	if summary.ProviderName != "openai-responses" || summary.ProviderEffort != llm.ReasoningEffortNativeWithUnsupportedRetry || !strings.Contains(summary.ProviderEffortNote, "retry_without_reasoning") {
		t.Fatalf("provider context = name %q effort %q note %q", summary.ProviderName, summary.ProviderEffort, summary.ProviderEffortNote)
	}
	if len(summary.LoadedDeferredTools) != 1 || summary.LoadedDeferredTools[0] != "mcp_github_issue" {
		t.Fatalf("LoadedDeferredTools = %v", summary.LoadedDeferredTools)
	}
	if len(summary.SkillCatalogReceipts) != 1 || summary.SkillCatalogReceipts[0].Action != "recommend" {
		t.Fatalf("SkillCatalogReceipts = %+v", summary.SkillCatalogReceipts)
	}
	if len(summary.CommandCatalogReceipts) != 1 || summary.CommandCatalogReceipts[0].ExecutionPolicy != "metadata_only" {
		t.Fatalf("CommandCatalogReceipts = %+v", summary.CommandCatalogReceipts)
	}
	if len(summary.ConditionalSkillMatches) != 1 || summary.ConditionalSkillMatches[0].SkillName != "go-review" {
		t.Fatalf("ConditionalSkillMatches = %+v", summary.ConditionalSkillMatches)
	}
	if summary.ConditionalSkillMatches[0].Source != "claude-skill" || summary.ConditionalSkillMatches[0].TrustLevel != "local_compatible" || summary.ConditionalSkillMatches[0].External {
		t.Fatalf("ConditionalSkillMatches[0] trust metadata = %+v", summary.ConditionalSkillMatches[0])
	}
	if !summary.CorrectionAttempted || summary.CorrectionAttempts != 1 || summary.CorrectionMaxAttempts != 1 || summary.CorrectionDecision != completionRetryDecisionRetrySmallerScope || summary.CorrectionReason != "final_response_reports_incomplete" {
		t.Fatalf("correction context = attempted %v attempts %d max %d decision %q reason %q", summary.CorrectionAttempted, summary.CorrectionAttempts, summary.CorrectionMaxAttempts, summary.CorrectionDecision, summary.CorrectionReason)
	}
	if summary.CorrectionStatus != "failed" || summary.CorrectionFailureFamily != "workflow_error" {
		t.Fatalf("correction failure context = status %q family %q", summary.CorrectionStatus, summary.CorrectionFailureFamily)
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
		VerificationHint:      true,
		VerificationObserved:  &observed,
		VerificationCommand:   "go test ./cmd/elnath -count=1",
		CompletionWarning:     "final_response_reports_incomplete",
		EditIntent:            true,
		EditObserved:          &observed,
		ReasoningEffort:       "medium",
		ReasoningEffortMode:   "manual",
		ReasoningEffortReason: "manual",
		ProviderName:          "openai-responses",
		ProviderEffort:        llm.ReasoningEffortNativeWithUnsupportedRetry,
		ProviderEffortNote:    "retry_without_reasoning_on_400_or_422_unsupported_effort",
		LoadedDeferredTools:   []string{"mcp_github_issue"},
		SkillCatalogReceipts: []completionSkillCatalogReceipt{{
			Tool:              "skill_catalog",
			Action:            "recommend",
			ReadOnly:          true,
			RegistryAvailable: true,
			TotalSkills:       2,
			ReturnedSkills:    1,
			MaxResults:        5,
			Query:             "review code",
		}},
		CommandCatalogReceipts: []completionCommandCatalogReceipt{{
			Tool:               "command_catalog",
			Action:             "recommend",
			ReadOnly:           true,
			RegistryAvailable:  true,
			ExecutionAvailable: false,
			ExecutionPolicy:    "metadata_only",
			TotalCommands:      12,
			ReturnedCommands:   1,
			MaxResults:         2,
			Query:              "commands",
		}},
		CorrectionAttempted:     true,
		CorrectionAttempts:      1,
		CorrectionMaxAttempts:   1,
		CorrectionDecision:      completionRetryDecisionRetrySmallerScope,
		CorrectionReason:        "final_response_reports_incomplete",
		CorrectionStatus:        "failed",
		CorrectionFailureFamily: "workflow_error",
		RetryDecision:           completionRetryDecisionRetrySmallerScope,
		RetryReason:             "final_response_reports_incomplete",
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
	if decision.Passed || decision.VerificationRunID != run.ID || decision.Status != agentic.CompletionGateStatusBlocked || !strings.Contains(decision.Reason, "completion warning") {
		t.Fatalf("decision = %+v, want completion warning block with verification run %d", decision, run.ID)
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
	if summary["reasoning_effort_reason"] != "manual" {
		t.Fatalf("reasoning reason missing from gate summary: %v", summary)
	}
	if summary["provider_name"] != "openai-responses" || summary["provider_effort"] != llm.ReasoningEffortNativeWithUnsupportedRetry {
		t.Fatalf("provider context missing from gate summary: %v", summary)
	}
	if note, _ := summary["provider_effort_note"].(string); !strings.Contains(note, "retry_without_reasoning") {
		t.Fatalf("provider note missing from gate summary: %v", summary)
	}
	loaded, ok := summary["loaded_deferred_tools"].([]any)
	if !ok || len(loaded) != 1 || loaded[0] != "mcp_github_issue" {
		t.Fatalf("loaded deferred tools missing from gate summary: %v", summary)
	}
	catalogReceipts, ok := summary["skill_catalog_receipts"].([]any)
	if !ok || len(catalogReceipts) != 1 {
		t.Fatalf("skill catalog receipts missing from gate summary: %v", summary)
	}
	commandCatalogReceipts, ok := summary["command_catalog_receipts"].([]any)
	if !ok || len(commandCatalogReceipts) != 1 {
		t.Fatalf("command catalog receipts missing from gate summary: %v", summary)
	}
	if summary["correction_attempted"] != true || summary["correction_attempts"] != float64(1) || summary["correction_max_attempts"] != float64(1) {
		t.Fatalf("correction attempt missing from gate summary: %v", summary)
	}
	if summary["correction_decision"] != completionRetryDecisionRetrySmallerScope || summary["correction_reason"] != "final_response_reports_incomplete" {
		t.Fatalf("correction reason missing from gate summary: %v", summary)
	}
	if summary["correction_status"] != "failed" || summary["correction_failure_family"] != "workflow_error" {
		t.Fatalf("correction failure missing from gate summary: %v", summary)
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
	if rec.VerificationCommand != "go test ./cmd/elnath -count=1" {
		t.Fatalf("VerificationCommand = %q", rec.VerificationCommand)
	}
	if rec.CompletionWarning != "final_response_reports_incomplete" {
		t.Fatalf("CompletionWarning = %q", rec.CompletionWarning)
	}
	if rec.ReasoningEffort != "high" || rec.ReasoningEffortMode != "auto" {
		t.Fatalf("reasoning = effort %q mode %q", rec.ReasoningEffort, rec.ReasoningEffortMode)
	}
	if rec.ReasoningEffortReason != "work_keyword" {
		t.Fatalf("ReasoningEffortReason = %q", rec.ReasoningEffortReason)
	}
	if rec.ProviderName != "openai-responses" || rec.ProviderEffort != llm.ReasoningEffortNativeWithUnsupportedRetry || !strings.Contains(rec.ProviderEffortNote, "retry_without_reasoning") {
		t.Fatalf("provider = name %q effort %q note %q", rec.ProviderName, rec.ProviderEffort, rec.ProviderEffortNote)
	}
	if len(rec.LoadedDeferredTools) != 1 || rec.LoadedDeferredTools[0] != "mcp_github_issue" {
		t.Fatalf("LoadedDeferredTools = %v", rec.LoadedDeferredTools)
	}
	if len(rec.SkillCatalogReceipts) != 1 || rec.SkillCatalogReceipts[0].Action != "recommend" {
		t.Fatalf("SkillCatalogReceipts = %+v", rec.SkillCatalogReceipts)
	}
	if len(rec.CommandCatalogReceipts) != 1 || rec.CommandCatalogReceipts[0].ExecutionPolicy != "metadata_only" {
		t.Fatalf("CommandCatalogReceipts = %+v", rec.CommandCatalogReceipts)
	}
	if len(rec.ConditionalSkillMatches) != 1 {
		t.Fatalf("ConditionalSkillMatches = %#v, want one match", rec.ConditionalSkillMatches)
	}
	if rec.ConditionalSkillMatches[0].SkillName != "go-review" || rec.ConditionalSkillMatches[0].Path != "internal/skill/skill.go" {
		t.Fatalf("ConditionalSkillMatches[0] = %+v", rec.ConditionalSkillMatches[0])
	}
	if rec.ConditionalSkillMatches[0].Source != "claude-skill" || rec.ConditionalSkillMatches[0].TrustLevel != "local_compatible" || rec.ConditionalSkillMatches[0].External {
		t.Fatalf("ConditionalSkillMatches[0] trust metadata = %+v", rec.ConditionalSkillMatches[0])
	}
	if !rec.CorrectionAttempted || rec.CorrectionAttempts != 1 || rec.CorrectionMaxAttempts != 1 || rec.CorrectionDecision != completionRetryDecisionRetrySmallerScope || rec.CorrectionReason != "final_response_reports_incomplete" {
		t.Fatalf("correction = attempted %v attempts %d max %d decision %q reason %q", rec.CorrectionAttempted, rec.CorrectionAttempts, rec.CorrectionMaxAttempts, rec.CorrectionDecision, rec.CorrectionReason)
	}
	if rec.CorrectionStatus != "failed" || rec.CorrectionFailureFamily != "workflow_error" {
		t.Fatalf("correction failure = status %q family %q", rec.CorrectionStatus, rec.CorrectionFailureFamily)
	}
	if rec.RetryDecision != completionRetryDecisionRetrySmallerScope || rec.RetryReason != "final_response_reports_incomplete" {
		t.Fatalf("retry = decision %q reason %q", rec.RetryDecision, rec.RetryReason)
	}
}
