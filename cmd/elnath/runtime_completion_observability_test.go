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

func TestCompletionRetryPromptGuidesNoDiffCorrection(t *testing.T) {
	prompt := completionRetryPrompt(completionContractSummary{
		RetryDecision: completionRetryDecisionRetrySmallerScope,
		RetryReason:   "edit_intent_without_mutation",
	})

	for _, want := range []string{
		"Retry reason: edit_intent_without_mutation",
		"left no accepted mutation",
		"Make the smallest concrete file edit",
		"instead of claiming completion",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestCompletionRetryPromptGuidesBudgetAfterEditIntent(t *testing.T) {
	prompt := completionRetryPrompt(completionContractSummary{
		RetryDecision: completionRetryDecisionRetrySmallerScope,
		RetryReason:   "budget_exceeded_after_edit_intent",
	})

	for _, want := range []string{
		"Retry reason: budget_exceeded_after_edit_intent",
		"reached budget after edit intent",
		"do not restart broad investigation",
		"run the configured verification",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
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

func TestCompletionContractSummaryDetectsBudgetExceededAfterEditIntent(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("fix the bug in the daemon and add a regression test"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "bash-1", Name: "bash", Input: json.RawMessage(`{"command":"cat > internal/daemon/daemon.go <<'EOF'\npatched\nEOF"}`)},
			}},
			llm.NewToolResultMessage("bash-1", "ok", false),
			llm.NewAssistantMessage("I will add the regression test next."),
		},
		FinishReason: "budget_exceeded",
	}
	summary := summarizeCompletionContract(&orchestrator.RoutingContext{VerificationHint: true}, orchestrator.WorkflowConfig{}, result)

	if !summary.EditIntent {
		t.Fatal("EditIntent = false, want true")
	}
	if summary.EditObserved == nil || !*summary.EditObserved {
		t.Fatalf("EditObserved = %v, want true", summary.EditObserved)
	}
	if summary.CompletionWarning != "budget_exceeded_after_edit_intent" {
		t.Fatalf("CompletionWarning = %q, want budget_exceeded_after_edit_intent", summary.CompletionWarning)
	}
	if summary.RetryDecision != completionRetryDecisionRetrySmallerScope || summary.RetryReason != "budget_exceeded_after_edit_intent" {
		t.Fatalf("retry plan = %q/%q, want retry_smaller_scope/budget_exceeded_after_edit_intent", summary.RetryDecision, summary.RetryReason)
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

func TestCompletionContractSummaryDoesNotCountNoopBashMutationAsMutation(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("fix the bug in the daemon and run tests"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "bash-1", Name: "bash", Input: json.RawMessage(`{"command":"apply_patch <<'PATCH'\n*** Begin Patch\n*** End Patch\nPATCH"}`)},
			}},
			llm.NewToolResultMessage("bash-1", "No changes.", false),
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
		t.Fatal("EditObserved = true, want no-op bash mutation not counted as mutation")
	}
	if summary.CompletionWarning != "edit_intent_without_mutation" {
		t.Fatalf("CompletionWarning = %q, want edit_intent_without_mutation", summary.CompletionWarning)
	}
}

func TestCompletionContractSummaryDoesNotCountNoopWriteFileResultAsMutation(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("update the daemon file and run tests"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "write-1", Name: "write_file", Input: json.RawMessage(`{"file_path":"internal/daemon/daemon.go","content":"same"}`)},
			}},
			llm.NewToolResultMessage("write-1", "write_file: content already matches internal/daemon/daemon.go", false),
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
		t.Fatal("EditObserved = true, want no-op write_file result not counted as mutation")
	}
	if summary.CompletionWarning != "edit_intent_without_mutation" {
		t.Fatalf("CompletionWarning = %q, want edit_intent_without_mutation", summary.CompletionWarning)
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

func TestCompletionContractSummaryRecordsSkillExecutionReceipt(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("run the review skill"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "skill-run-1", Name: "skill", Input: json.RawMessage(`{"skill":"review-pr"}`)},
			}},
			llm.NewToolResultMessage("skill-run-1", `{"skill":"review-pr","status":"completed","source":"codex-plugin-skill","trust_level":"plugin_cache","external":true,"output":"done","receipt":{"skill":"review-pr","provider":"openai-responses","model":"gpt-5.5","reasoning_effort":"high","reasoning_effort_mode":"manual","permission_mode":"bypass","max_iterations":8,"required_tools":["read_file"],"available_tools":["read_file","grep"],"tool_filter_applied":true,"base_dir":"/tmp/skills/review-pr","source":"codex-plugin-skill","trust_level":"plugin_cache","external":true,"user_invocable":true}}`, false),
			llm.NewAssistantMessage("Done."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if len(summary.SkillExecutionReceipts) != 1 {
		t.Fatalf("SkillExecutionReceipts = %#v, want one receipt", summary.SkillExecutionReceipts)
	}
	receipt := summary.SkillExecutionReceipts[0]
	if receipt.Tool != "skill" || receipt.Action != "execute" || receipt.Skill != "review-pr" || receipt.Status != "completed" {
		t.Fatalf("receipt identity = %+v", receipt)
	}
	if receipt.Provider != "openai-responses" || receipt.Model != "gpt-5.5" || receipt.ReasoningEffort != "high" || receipt.ReasoningEffortMode != "manual" {
		t.Fatalf("receipt model context = %+v", receipt)
	}
	if receipt.PermissionMode != "bypass" || receipt.MaxIterations != 8 || !receipt.ToolFilterApplied {
		t.Fatalf("receipt execution bounds = %+v", receipt)
	}
	if len(receipt.RequiredTools) != 1 || receipt.RequiredTools[0] != "read_file" {
		t.Fatalf("required tools = %+v", receipt.RequiredTools)
	}
	if len(receipt.AvailableTools) != 2 || receipt.AvailableTools[0] != "read_file" || receipt.AvailableTools[1] != "grep" {
		t.Fatalf("available tools = %+v", receipt.AvailableTools)
	}
	if receipt.BaseDir != "/tmp/skills/review-pr" || receipt.Source != "codex-plugin-skill" || receipt.TrustLevel != "plugin_cache" || !receipt.External || !receipt.UserInvocable {
		t.Fatalf("receipt trust/source metadata = %+v", receipt)
	}
}

func TestCompletionContractSummaryRecordsCommandCatalogReceipt(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("find command metadata"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "command-1", Name: "command_catalog", Input: json.RawMessage(`{"action":"recommend","query":"reasoning effort"}`)},
			}},
			llm.NewToolResultMessage("command-1", `{"action":"recommend","query":"reasoning effort","commands":[],"receipt":{"tool":"command_catalog","action":"recommend","read_only":true,"registry_available":true,"execution_available":false,"execution_policy":"metadata_only","total_commands":12,"returned_commands":0,"executable_commands":11,"model_callable_commands":1,"include_hidden":false,"max_results":5,"query":"reasoning effort","followup_tool":"skill"}}`, false),
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
	if receipt.TotalCommands != 12 || receipt.ReturnedCommands != 0 || receipt.ExecutableCommands != 11 || receipt.ModelCallableCommands != 1 || receipt.MaxResults != 5 || receipt.Query != "reasoning effort" || receipt.FollowupTool != "skill" {
		t.Fatalf("receipt counts/bounds = %+v", receipt)
	}
}

func TestCompletionContractSummaryRecordsToolSearchReceipt(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("find a deferred tool"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "search-1", Name: "tool_search", Input: json.RawMessage(`{"query":"task","max_results":3}`)},
			}},
			llm.NewToolResultMessage("search-1", `{"query":"task","total_tools":12,"matches":[],"receipt":{"tool":"tool_search","action":"search","read_only":true,"registry_available":true,"execution_available":false,"execution_policy":"metadata_only","total_tools":12,"returned_matches":0,"deferred_matches":0,"max_results":3,"allow_names_count":0,"query":"task"}}`, false),
			llm.NewAssistantMessage("Done."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if len(summary.ToolSearchReceipts) != 1 {
		t.Fatalf("ToolSearchReceipts = %#v, want one receipt", summary.ToolSearchReceipts)
	}
	receipt := summary.ToolSearchReceipts[0]
	if receipt.Tool != "tool_search" || receipt.Action != "search" || !receipt.ReadOnly || !receipt.RegistryAvailable {
		t.Fatalf("receipt identity = %+v", receipt)
	}
	if receipt.ExecutionAvailable || receipt.ExecutionPolicy != "metadata_only" {
		t.Fatalf("receipt execution boundary = %+v", receipt)
	}
	if receipt.TotalTools != 12 || receipt.ReturnedMatches != 0 || receipt.MaxResults != 3 || receipt.Query != "task" {
		t.Fatalf("receipt counts/bounds = %+v", receipt)
	}
}

func TestCompletionContractSummaryRecordsControlToolReceipts(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("enqueue task and run in worktree"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "task-1", Name: "task_create", Input: json.RawMessage(`{"prompt":"do work"}`)},
			}},
			llm.NewToolResultMessage("task-1", `{"task_id":7,"status":"pending","receipt":{"tool":"task_create","action":"create","read_only":false,"persistent":true,"queue_backed":true,"execution_policy":"daemon_queue_enqueue","task_id":7,"status":"pending","followup_tool":"task_monitor"}}`, false),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "worktree-1", Name: "worktree_run", Input: json.RawMessage(`{"name":"feature/run","command":"go test ./..."}`)},
			}},
			llm.NewToolResultMessage("worktree-1", `{"name":"feature/run","runner":"direct","is_error":false,"receipt":{"tool":"worktree_run","action":"run","read_only":false,"persistent":true,"registry_backed":true,"execution_available":true,"execution_policy":"managed_worktree_command","name":"feature/run","branch":"elnath-worktree-feature+run","runner":"direct"}}`, false),
			llm.NewAssistantMessage("Done."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if len(summary.ControlToolReceipts) != 2 {
		t.Fatalf("ControlToolReceipts = %#v, want two receipts", summary.ControlToolReceipts)
	}
	taskReceipt := summary.ControlToolReceipts[0]
	if taskReceipt.Tool != "task_create" || taskReceipt.Action != "create" || taskReceipt.ReadOnly || !taskReceipt.Persistent || !taskReceipt.QueueBacked || taskReceipt.TaskID != 7 || taskReceipt.Status != "pending" || taskReceipt.FollowupTool != "task_monitor" {
		t.Fatalf("task receipt = %+v", taskReceipt)
	}
	worktreeReceipt := summary.ControlToolReceipts[1]
	if worktreeReceipt.Tool != "worktree_run" || worktreeReceipt.Action != "run" || worktreeReceipt.ReadOnly || !worktreeReceipt.Persistent || !worktreeReceipt.RegistryBacked || !worktreeReceipt.ExecutionAvailable || worktreeReceipt.Runner != "direct" {
		t.Fatalf("worktree receipt = %+v", worktreeReceipt)
	}
}

func TestCompletionContractSummaryRecordsRuntimeCommandReceipt(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("show runtime status"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "runtime-command-1", Name: "runtime_command", Input: json.RawMessage(`{"command":"/status","args":["--json"]}`)},
			}},
			llm.NewToolResultMessage("runtime-command-1", `{"command":"/status","args":["--json"],"output":"{}","receipt":{"tool":"runtime_command","action":"execute","read_only":true,"execution_available":true,"execution_policy":"local_runtime_control_readonly","command":"/status","args":["--json"],"state_mutation":false}}`, false),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if len(summary.ControlToolReceipts) != 1 {
		t.Fatalf("ControlToolReceipts = %#v, want one runtime command receipt", summary.ControlToolReceipts)
	}
	receipt := summary.ControlToolReceipts[0]
	if receipt.Tool != "runtime_command" || receipt.Action != "execute" || !receipt.ReadOnly || !receipt.ExecutionAvailable || receipt.ExecutionPolicy != "local_runtime_control_readonly" {
		t.Fatalf("receipt identity = %+v", receipt)
	}
	if receipt.Command != "/status" || len(receipt.Args) != 1 || receipt.Args[0] != "--json" || receipt.StateMutation {
		t.Fatalf("receipt command bounds = %+v", receipt)
	}
}

func TestCompletionContractSummaryRecordsAskUserQuestionReceipt(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("need clarification"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "ask-1", Name: "ask_user_question", Input: json.RawMessage(`{"question":"Which branch?","options":["main","new"],"allow_free_text":false,"timeout_seconds":120}`)},
			}},
			llm.NewToolResultMessage("ask-1", `{"type":"user_input_required","question":"Which branch?","options":["main","new"],"allow_free_text":false,"timeout_seconds":120,"request_id":"req-123","session_id":"sess-123","receipt":{"tool":"ask_user_question","action":"request","read_only":true,"execution_policy":"user_input_request","question":"Which branch?","question_chars":13,"option_count":2,"allow_free_text":false,"timeout_seconds":120,"request_id":"req-123","session_id":"sess-123"}}`, false),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if len(summary.ControlToolReceipts) != 1 {
		t.Fatalf("ControlToolReceipts = %#v, want one ask_user_question receipt", summary.ControlToolReceipts)
	}
	receipt := summary.ControlToolReceipts[0]
	if receipt.Tool != "ask_user_question" || receipt.Action != "request" || !receipt.ReadOnly || receipt.ExecutionPolicy != "user_input_request" {
		t.Fatalf("receipt identity = %+v", receipt)
	}
	if receipt.Question != "Which branch?" || receipt.QuestionChars != 13 || receipt.OptionCount != 2 || receipt.AllowFreeText || receipt.TimeoutSeconds != 120 || receipt.RequestID != "req-123" || receipt.SessionID != "sess-123" {
		t.Fatalf("receipt bounds = %+v", receipt)
	}
	if !summary.UserInputRequired {
		t.Fatal("UserInputRequired = false, want true")
	}
}

func TestCompletionContractSummaryRecordsUserQuestionAnswerReceipt(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("resume with answer"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "answer-1", Name: "user_question_answer", Input: json.RawMessage(`{"session_id":"sess-123","answer":"main"}`)},
			}},
			llm.NewToolResultMessage("answer-1", `{"type":"user_input_answer_enqueued","task_id":8,"status":"pending","request_id":"req-123","session_id":"sess-123","answer_chars":4,"receipt":{"tool":"user_question_answer","action":"answer","read_only":false,"persistent":true,"queue_backed":true,"execution_policy":"daemon_queue_user_answer_resume","task_id":8,"request_id":"req-123","session_id":"sess-123","status":"pending","followup_tool":"task_monitor","question_chars":13}}`, false),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if len(summary.ControlToolReceipts) != 1 {
		t.Fatalf("ControlToolReceipts = %#v, want one user_question_answer receipt", summary.ControlToolReceipts)
	}
	receipt := summary.ControlToolReceipts[0]
	if receipt.Tool != "user_question_answer" || receipt.Action != "answer" || receipt.ReadOnly || !receipt.Persistent || !receipt.QueueBacked || receipt.ExecutionPolicy != "daemon_queue_user_answer_resume" {
		t.Fatalf("receipt identity = %+v", receipt)
	}
	if receipt.TaskID != 8 || receipt.RequestID != "req-123" || receipt.SessionID != "sess-123" || receipt.Status != "pending" || receipt.FollowupTool != "task_monitor" || receipt.QuestionChars != 13 {
		t.Fatalf("receipt routing = %+v", receipt)
	}
}

func TestCompletionContractSummaryRecordsUserQuestionListReceipt(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("list pending questions"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "questions-1", Name: "user_question_list", Input: json.RawMessage(`{"session_id":"sess-123"}`)},
			}},
			llm.NewToolResultMessage("questions-1", `{"count":1,"pending":[],"receipt":{"tool":"user_question_list","action":"list","read_only":true,"session_id":"sess-123","limit":20,"total_returned":1}}`, false),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if len(summary.ControlToolReceipts) != 1 {
		t.Fatalf("ControlToolReceipts = %#v, want one user_question_list receipt", summary.ControlToolReceipts)
	}
	receipt := summary.ControlToolReceipts[0]
	if receipt.Tool != "user_question_list" || receipt.Action != "list" || !receipt.ReadOnly || receipt.SessionID != "sess-123" || receipt.Limit != 20 || receipt.TotalReturned != 1 {
		t.Fatalf("receipt = %+v, want pending-question list receipt", receipt)
	}
}

func TestCompletionContractSummaryDoesNotRetryWhenUserInputRequired(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("Need a branch decision before editing."),
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentBlock{
					llm.ToolUseBlock{ID: "ask-1", Name: "ask_user_question", Input: json.RawMessage(`{"question":"Which branch?","options":["main","new"],"allow_free_text":false,"timeout_seconds":120}`)},
				},
			},
			llm.NewToolResultMessage("ask-1", `{"type":"user_input_required","question":"Which branch?","options":["main","new"],"allow_free_text":false,"timeout_seconds":120,"receipt":{"tool":"ask_user_question","action":"request","read_only":true,"execution_policy":"user_input_request","question_chars":13,"option_count":2,"allow_free_text":false,"timeout_seconds":120}}`, false),
			llm.NewAssistantMessage("I still need your answer before I can finish."),
		},
		FinishReason: "stop",
	}

	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if !summary.UserInputRequired {
		t.Fatal("UserInputRequired = false, want true")
	}
	if summary.CompletionWarning != "final_response_reports_incomplete" {
		t.Fatalf("CompletionWarning = %q, want final_response_reports_incomplete", summary.CompletionWarning)
	}
	if summary.RetryDecision != "" || summary.RetryReason != "" {
		t.Fatalf("retry plan = %q/%q, want empty while user input is required", summary.RetryDecision, summary.RetryReason)
	}
}

func TestCompletionContractSummaryRecordsProcessToolReceipts(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("start background process and monitor it"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "process-1", Name: "process_start", Input: json.RawMessage(`{"command":"sleep 1"}`)},
			}},
			llm.NewToolResultMessage("process-1", `{"process_id":4,"status":"running","receipt":{"tool":"process_start","action":"start","read_only":false,"persistent":true,"execution_policy":"session_process_start","process_id":4,"status":"running","timeout_ms":600000,"cwd":"/tmp/work","followup_tool":"process_monitor"}}`, false),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "process-2", Name: "process_monitor", Input: json.RawMessage(`{"process_id":4}`)},
			}},
			llm.NewToolResultMessage("process-2", `{"process_id":4,"status":"completed","receipt":{"tool":"process_monitor","action":"monitor","read_only":true,"persistent":false,"execution_policy":"session_process_observation","process_id":4,"status":"completed","terminal":true,"exit_code":0,"found":true,"tail_bytes":4000,"stdout_raw_bytes":5,"stderr_raw_bytes":4,"stdout_truncated":false,"stderr_truncated":true,"cwd":"/tmp/work"}}`, false),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "process-3", Name: "process_stop", Input: json.RawMessage(`{"process_id":4}`)},
			}},
			llm.NewToolResultMessage("process-3", `{"process_id":4,"status":"stopped","receipt":{"tool":"process_stop","action":"stop","read_only":false,"persistent":true,"execution_policy":"session_process_stop","process_id":4,"status":"stopped","terminal":true,"found":true,"stop_signal":"SIGTERM","cwd":"/tmp/work","followup_tool":"process_monitor"}}`, false),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if len(summary.ControlToolReceipts) != 3 {
		t.Fatalf("ControlToolReceipts = %#v, want three process receipts", summary.ControlToolReceipts)
	}
	start := summary.ControlToolReceipts[0]
	if start.Tool != "process_start" || start.Action != "start" || start.ProcessID != 4 || start.TimeoutMS != 600000 || start.CWD != "/tmp/work" || start.FollowupTool != "process_monitor" {
		t.Fatalf("start receipt = %+v", start)
	}
	monitor := summary.ControlToolReceipts[1]
	if monitor.Tool != "process_monitor" || monitor.Action != "monitor" || monitor.ProcessID != 4 || !monitor.ReadOnly || monitor.Persistent || !monitor.Terminal || !monitor.Found || monitor.TailBytes != 4000 {
		t.Fatalf("monitor receipt = %+v", monitor)
	}
	if monitor.ExitCode == nil || *monitor.ExitCode != 0 || monitor.StdoutRawBytes != 5 || monitor.StderrRawBytes != 4 || !monitor.StderrTruncated {
		t.Fatalf("monitor receipt output metadata = %+v", monitor)
	}
	stop := summary.ControlToolReceipts[2]
	if stop.Tool != "process_stop" || stop.Action != "stop" || stop.ProcessID != 4 || stop.StopSignal != "SIGTERM" || !stop.Terminal || !stop.Found || stop.FollowupTool != "process_monitor" {
		t.Fatalf("stop receipt = %+v", stop)
	}
}

func TestCompletionContractSummaryRecordsSleepToolReceipt(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("wait briefly before checking again"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "sleep-1", Name: "sleep", Input: json.RawMessage(`{"duration_ms":1000}`)},
			}},
			llm.NewToolResultMessage("sleep-1", `{"requested_ms":1000,"slept_ms":1000,"receipt":{"tool":"sleep","action":"wait","read_only":true,"persistent":false,"execution_available":true,"execution_policy":"timer_wait","status":"completed","timeout_ms":1000}}`, false),
			llm.NewAssistantMessage("Done."),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if len(summary.ControlToolReceipts) != 1 {
		t.Fatalf("ControlToolReceipts = %#v, want one sleep receipt", summary.ControlToolReceipts)
	}
	receipt := summary.ControlToolReceipts[0]
	if receipt.Tool != "sleep" || receipt.Action != "wait" || !receipt.ReadOnly || receipt.Persistent || !receipt.ExecutionAvailable {
		t.Fatalf("sleep receipt = %+v", receipt)
	}
	if receipt.ExecutionPolicy != "timer_wait" || receipt.Status != "completed" || receipt.TimeoutMS != 1000 {
		t.Fatalf("sleep receipt execution = %+v", receipt)
	}
}

func TestCompletionContractSummaryRecordsDelegationToolReceipts(t *testing.T) {
	result := &orchestrator.WorkflowResult{
		Messages: []llm.Message{
			llm.NewUserMessage("spawn delegated child and send actor message"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "delegate-1", Name: "agentic_delegate_enqueue", Input: json.RawMessage(`{"child_task_id":9}`)},
			}},
			llm.NewToolResultMessage("delegate-1", `{"child_task_id":9,"parent_task_id":3,"queue_task_id":44,"receipt":{"tool":"agentic_delegate_enqueue","action":"enqueue","read_only":false,"persistent":true,"execution_policy":"agentic_delegation_enqueue","parent_task_id":3,"child_task_id":9,"queue_task_id":44,"decision_id":7,"decision_status":"enqueued","enqueued":true}}`, false),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "status-1", Name: "agentic_delegate_status", Input: json.RawMessage(`{"parent_task_id":3}`)},
			}},
			llm.NewToolResultMessage("status-1", `{"parent_task_id":3,"total":1,"receipt":{"tool":"agentic_delegate_status","action":"status","read_only":true,"persistent":false,"execution_policy":"agentic_delegation_observation","parent_task_id":3,"total":1}}`, false),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				llm.ToolUseBlock{ID: "message-1", Name: "agentic_message_send", Input: json.RawMessage(`{"task_id":3,"from_actor_id":1,"to_actor_id":2,"message":"go"}`)},
			}},
			llm.NewToolResultMessage("message-1", `{"task_id":3,"from_actor_id":1,"to_actor_id":2,"handoff_id":5,"receipt":{"tool":"agentic_message_send","action":"send","read_only":false,"persistent":true,"execution_policy":"agentic_actor_message_send","task_id":3,"from_actor_id":1,"to_actor_id":2,"handoff_id":5,"delivered":true}}`, false),
		},
		FinishReason: "stop",
	}
	summary := summarizeCompletionContract(nil, orchestrator.WorkflowConfig{}, result)

	if len(summary.ControlToolReceipts) != 3 {
		t.Fatalf("ControlToolReceipts = %#v, want delegation/status/message receipts", summary.ControlToolReceipts)
	}
	delegateReceipt := summary.ControlToolReceipts[0]
	if delegateReceipt.Tool != "agentic_delegate_enqueue" || delegateReceipt.ParentTaskID != 3 || delegateReceipt.ChildTaskID != 9 || delegateReceipt.QueueTaskID != 44 || delegateReceipt.DecisionID != 7 || delegateReceipt.DecisionStatus != "enqueued" || !delegateReceipt.Enqueued {
		t.Fatalf("delegate receipt = %+v", delegateReceipt)
	}
	statusReceipt := summary.ControlToolReceipts[1]
	if statusReceipt.Tool != "agentic_delegate_status" || statusReceipt.Action != "status" || !statusReceipt.ReadOnly || statusReceipt.ParentTaskID != 3 || statusReceipt.Total != 1 {
		t.Fatalf("status receipt = %+v", statusReceipt)
	}
	messageReceipt := summary.ControlToolReceipts[2]
	if messageReceipt.Tool != "agentic_message_send" || messageReceipt.TaskID != 3 || messageReceipt.FromActorID != 1 || messageReceipt.ToActorID != 2 || messageReceipt.HandoffID != 5 || !messageReceipt.Delivered {
		t.Fatalf("message receipt = %+v", messageReceipt)
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

func TestProcessControlReceiptsConvertToLearningAndAgentic(t *testing.T) {
	src := []completionControlToolReceipt{{
		Tool:            "process_start",
		Action:          "start",
		Persistent:      true,
		ExecutionPolicy: "session_process_start",
		ProcessID:       4,
		Status:          "running",
		TimeoutMS:       600000,
		CWD:             "/tmp/work",
		FollowupTool:    "process_monitor",
	}, {
		Tool:            "process_monitor",
		Action:          "monitor",
		ReadOnly:        true,
		ExecutionPolicy: "session_process_observation",
		ProcessID:       4,
		Status:          "completed",
		Terminal:        true,
		Found:           true,
		TailBytes:       4000,
		StdoutRawBytes:  5,
		StderrRawBytes:  4,
		StderrTruncated: true,
		CWD:             "/tmp/work",
	}, {
		Tool:            "process_stop",
		Action:          "stop",
		Persistent:      true,
		ExecutionPolicy: "session_process_stop",
		ProcessID:       4,
		Status:          "stopped",
		Terminal:        true,
		Found:           true,
		StopSignal:      "SIGTERM",
		CWD:             "/tmp/work",
		FollowupTool:    "process_monitor",
	}}

	learningReceipts := completionControlToolReceiptsToLearning(src)
	if len(learningReceipts) != 3 || learningReceipts[0].ProcessID != 4 || learningReceipts[0].TimeoutMS != 600000 || learningReceipts[0].FollowupTool != "process_monitor" || learningReceipts[1].TailBytes != 4000 || learningReceipts[1].StdoutRawBytes != 5 || !learningReceipts[1].StderrTruncated || learningReceipts[2].StopSignal != "SIGTERM" || learningReceipts[2].FollowupTool != "process_monitor" {
		t.Fatalf("learning receipts = %+v", learningReceipts)
	}
	agenticReceipts := completionControlToolReceiptsToAgentic(src)
	if len(agenticReceipts) != 3 || agenticReceipts[0].ProcessID != 4 || agenticReceipts[0].CWD != "/tmp/work" || agenticReceipts[0].FollowupTool != "process_monitor" || agenticReceipts[1].TailBytes != 4000 || agenticReceipts[1].StderrRawBytes != 4 || agenticReceipts[2].StopSignal != "SIGTERM" || agenticReceipts[2].FollowupTool != "process_monitor" {
		t.Fatalf("agentic receipts = %+v", agenticReceipts)
	}
}

func TestDelegationControlReceiptsConvertToLearningAndAgentic(t *testing.T) {
	src := []completionControlToolReceipt{{
		Tool:            "agentic_delegate_enqueue",
		Action:          "enqueue",
		Persistent:      true,
		ExecutionPolicy: "agentic_delegation_enqueue",
		ParentTaskID:    3,
		ChildTaskID:     9,
		QueueTaskID:     44,
		FollowupTool:    "agentic_delegate_status",
		DecisionID:      7,
		DecisionStatus:  "enqueued",
		Enqueued:        true,
		RequestID:       "req-123",
		SessionID:       "sess-123",
		Question:        "Which branch?",
	}, {
		Tool:            "agentic_message_send",
		Action:          "send",
		Persistent:      true,
		ExecutionPolicy: "agentic_actor_message_send",
		TaskID:          3,
		FromActorID:     1,
		ToActorID:       2,
		HandoffID:       5,
		Delivered:       true,
	}}

	learningReceipts := completionControlToolReceiptsToLearning(src)
	if len(learningReceipts) != 2 || learningReceipts[0].ParentTaskID != 3 || learningReceipts[0].ChildTaskID != 9 || learningReceipts[0].QueueTaskID != 44 || learningReceipts[0].FollowupTool != "agentic_delegate_status" || learningReceipts[0].DecisionID != 7 || !learningReceipts[0].Enqueued || learningReceipts[0].RequestID != "req-123" || learningReceipts[0].SessionID != "sess-123" || learningReceipts[0].Question != "Which branch?" || learningReceipts[1].HandoffID != 5 || !learningReceipts[1].Delivered {
		t.Fatalf("learning receipts = %+v", learningReceipts)
	}
	agenticReceipts := completionControlToolReceiptsToAgentic(src)
	if len(agenticReceipts) != 2 || agenticReceipts[0].ParentTaskID != 3 || agenticReceipts[0].ChildTaskID != 9 || agenticReceipts[0].QueueTaskID != 44 || agenticReceipts[0].FollowupTool != "agentic_delegate_status" || agenticReceipts[0].DecisionStatus != "enqueued" || agenticReceipts[0].RequestID != "req-123" || agenticReceipts[0].SessionID != "sess-123" || agenticReceipts[0].Question != "Which branch?" || agenticReceipts[1].FromActorID != 1 || agenticReceipts[1].ToActorID != 2 || !agenticReceipts[1].Delivered {
		t.Fatalf("agentic receipts = %+v", agenticReceipts)
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
			SkillExecutionReceipts: []completionSkillExecutionReceipt{{
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
			CommandCatalogReceipts: []completionCommandCatalogReceipt{{
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
			ToolSearchReceipts: []completionToolSearchReceipt{{
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
			ControlToolReceipts: []completionControlToolReceipt{{
				Tool:            "task_create",
				Action:          "create",
				Persistent:      true,
				QueueBacked:     true,
				ExecutionPolicy: "daemon_queue_enqueue",
				TaskID:          7,
				Status:          "pending",
			}},
			CorrectionAttempted:     true,
			CorrectionAttempts:      1,
			CorrectionMaxAttempts:   1,
			CorrectionDecision:      completionRetryDecisionRetrySmallerScope,
			CorrectionReason:        "final_response_reports_incomplete",
			CorrectionStatus:        "failed",
			CorrectionFailureFamily: "workflow_error",
			CorrectionAttemptDetails: []completionCorrectionAttemptReceipt{{
				Attempt:           1,
				Decision:          completionRetryDecisionRetrySmallerScope,
				Reason:            "final_response_reports_incomplete",
				Status:            "failed",
				FailureFamily:     "workflow_error",
				CompletionWarning: "final_response_reports_incomplete",
			}},
			RetryDecision: completionRetryDecisionRetrySmallerScope,
			RetryReason:   "final_response_reports_incomplete",
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
		SkillExecutionReceipts: []completionSkillExecutionReceipt{{
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
		CommandCatalogReceipts: []completionCommandCatalogReceipt{{
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
		ToolSearchReceipts: []completionToolSearchReceipt{{
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
		ControlToolReceipts: []completionControlToolReceipt{{
			Tool:            "worktree_run",
			Action:          "run",
			Persistent:      true,
			RegistryBacked:  true,
			ExecutionPolicy: "managed_worktree_command",
			Name:            "feature/run",
			Runner:          "direct",
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
		CorrectionAttemptDetails: []completionCorrectionAttemptReceipt{{
			Attempt:           1,
			Decision:          completionRetryDecisionRetrySmallerScope,
			Reason:            "final_response_reports_incomplete",
			Status:            "failed",
			FailureFamily:     "workflow_error",
			CompletionWarning: "final_response_reports_incomplete",
		}},
		RetryDecision: completionRetryDecisionRetrySmallerScope,
		RetryReason:   "final_response_reports_incomplete",
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
	if len(summary.SkillExecutionReceipts) != 1 || summary.SkillExecutionReceipts[0].Skill != "review-pr" || summary.SkillExecutionReceipts[0].Model != "gpt-5.5" {
		t.Fatalf("SkillExecutionReceipts = %+v", summary.SkillExecutionReceipts)
	}
	if len(summary.CommandCatalogReceipts) != 1 || summary.CommandCatalogReceipts[0].ExecutionPolicy != "metadata_only" || summary.CommandCatalogReceipts[0].ExecutableCommands != 11 || summary.CommandCatalogReceipts[0].ModelCallableCommands != 1 || summary.CommandCatalogReceipts[0].FollowupTool != "skill" {
		t.Fatalf("CommandCatalogReceipts = %+v", summary.CommandCatalogReceipts)
	}
	if len(summary.ToolSearchReceipts) != 1 || summary.ToolSearchReceipts[0].ExecutionPolicy != "metadata_only" {
		t.Fatalf("ToolSearchReceipts = %+v", summary.ToolSearchReceipts)
	}
	if len(summary.ControlToolReceipts) != 1 || summary.ControlToolReceipts[0].Tool != "worktree_run" {
		t.Fatalf("ControlToolReceipts = %+v", summary.ControlToolReceipts)
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
	if len(summary.CorrectionAttemptDetails) != 1 || summary.CorrectionAttemptDetails[0].FailureFamily != "workflow_error" {
		t.Fatalf("correction attempt details = %+v", summary.CorrectionAttemptDetails)
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
		SkillExecutionReceipts: []completionSkillExecutionReceipt{{
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
		CommandCatalogReceipts: []completionCommandCatalogReceipt{{
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
		ToolSearchReceipts: []completionToolSearchReceipt{{
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
		ControlToolReceipts: []completionControlToolReceipt{{
			Tool:            "task_stop",
			Action:          "stop",
			Persistent:      true,
			QueueBacked:     true,
			ExecutionPolicy: "daemon_queue_mutation",
			TaskID:          7,
			Status:          "failed",
		}},
		CorrectionAttempted:     true,
		CorrectionAttempts:      1,
		CorrectionMaxAttempts:   1,
		CorrectionDecision:      completionRetryDecisionRetrySmallerScope,
		CorrectionReason:        "final_response_reports_incomplete",
		CorrectionStatus:        "failed",
		CorrectionFailureFamily: "workflow_error",
		CorrectionAttemptDetails: []completionCorrectionAttemptReceipt{{
			Attempt:           1,
			Decision:          completionRetryDecisionRetrySmallerScope,
			Reason:            "final_response_reports_incomplete",
			Status:            "failed",
			FailureFamily:     "workflow_error",
			CompletionWarning: "final_response_reports_incomplete",
		}},
		RetryDecision: completionRetryDecisionRetrySmallerScope,
		RetryReason:   "final_response_reports_incomplete",
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
	skillExecutionReceipts, ok := summary["skill_execution_receipts"].([]any)
	if !ok || len(skillExecutionReceipts) != 1 {
		t.Fatalf("skill execution receipts missing from gate summary: %v", summary)
	}
	skillExecutionReceipt, ok := skillExecutionReceipts[0].(map[string]any)
	if !ok || skillExecutionReceipt["skill"] != "review-pr" || skillExecutionReceipt["model"] != "gpt-5.5" || skillExecutionReceipt["tool_filter_applied"] != true {
		t.Fatalf("skill execution receipt missing fields: receipt=%v summary=%v", skillExecutionReceipts[0], summary)
	}
	commandCatalogReceipts, ok := summary["command_catalog_receipts"].([]any)
	if !ok || len(commandCatalogReceipts) != 1 {
		t.Fatalf("command catalog receipts missing from gate summary: %v", summary)
	}
	commandCatalogReceipt, ok := commandCatalogReceipts[0].(map[string]any)
	if !ok || commandCatalogReceipt["executable_commands"] != float64(11) || commandCatalogReceipt["model_callable_commands"] != float64(1) {
		t.Fatalf("command catalog receipt execution counts missing: receipt=%v summary=%v", commandCatalogReceipts[0], summary)
	}
	toolSearchReceipts, ok := summary["tool_search_receipts"].([]any)
	if !ok || len(toolSearchReceipts) != 1 {
		t.Fatalf("tool search receipts missing from gate summary: %v", summary)
	}
	controlToolReceipts, ok := summary["control_tool_receipts"].([]any)
	if !ok || len(controlToolReceipts) != 1 {
		t.Fatalf("control tool receipts missing from gate summary: %v", summary)
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
	attemptDetails, ok := summary["correction_attempt_details"].([]any)
	if !ok || len(attemptDetails) != 1 {
		t.Fatalf("correction attempt details missing from gate summary: %v", summary)
	}
	attemptDetail, ok := attemptDetails[0].(map[string]any)
	if !ok || attemptDetail["attempt"] != float64(1) || attemptDetail["failure_family"] != "workflow_error" {
		t.Fatalf("correction attempt detail missing fields: detail=%v summary=%v", attemptDetails[0], summary)
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
	if len(rec.SkillExecutionReceipts) != 1 || rec.SkillExecutionReceipts[0].Skill != "review-pr" || rec.SkillExecutionReceipts[0].Model != "gpt-5.5" || !rec.SkillExecutionReceipts[0].ToolFilterApplied {
		t.Fatalf("SkillExecutionReceipts = %+v", rec.SkillExecutionReceipts)
	}
	if len(rec.CommandCatalogReceipts) != 1 || rec.CommandCatalogReceipts[0].ExecutionPolicy != "metadata_only" || rec.CommandCatalogReceipts[0].ExecutableCommands != 11 || rec.CommandCatalogReceipts[0].ModelCallableCommands != 1 || rec.CommandCatalogReceipts[0].FollowupTool != "skill" {
		t.Fatalf("CommandCatalogReceipts = %+v", rec.CommandCatalogReceipts)
	}
	if len(rec.ToolSearchReceipts) != 1 || rec.ToolSearchReceipts[0].ExecutionPolicy != "metadata_only" {
		t.Fatalf("ToolSearchReceipts = %+v", rec.ToolSearchReceipts)
	}
	if len(rec.ControlToolReceipts) != 1 || rec.ControlToolReceipts[0].Tool != "task_create" {
		t.Fatalf("ControlToolReceipts = %+v", rec.ControlToolReceipts)
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
	if len(rec.CorrectionAttemptDetails) != 1 || rec.CorrectionAttemptDetails[0].FailureFamily != "workflow_error" {
		t.Fatalf("correction attempt details = %+v", rec.CorrectionAttemptDetails)
	}
	if rec.RetryDecision != completionRetryDecisionRetrySmallerScope || rec.RetryReason != "final_response_reports_incomplete" {
		t.Fatalf("retry = decision %q reason %q", rec.RetryDecision, rec.RetryReason)
	}
}
