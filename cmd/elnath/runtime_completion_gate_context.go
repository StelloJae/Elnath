package main

import (
	"context"

	agenticcompletion "github.com/stello/elnath/internal/agentic/completion"
	"github.com/stello/elnath/internal/daemon"
)

func (rt *executionRuntime) rememberAgenticCompletionContext(agenticTaskID int64, summary completionContractSummary) {
	if rt == nil || agenticTaskID == 0 {
		return
	}
	rt.completionCtxMu.Lock()
	defer rt.completionCtxMu.Unlock()
	if rt.completionCtxs == nil {
		rt.completionCtxs = make(map[int64]completionContractSummary)
	}
	rt.completionCtxs[agenticTaskID] = summary
}

func (rt *executionRuntime) CompletionContext(_ context.Context, _ daemon.Task, agenticTaskID int64) (agenticcompletion.CompletionContext, error) {
	if rt == nil || agenticTaskID == 0 {
		return agenticcompletion.CompletionContext{}, nil
	}
	rt.completionCtxMu.Lock()
	summary, ok := rt.completionCtxs[agenticTaskID]
	if ok {
		delete(rt.completionCtxs, agenticTaskID)
	}
	rt.completionCtxMu.Unlock()
	if !ok {
		return agenticcompletion.CompletionContext{}, nil
	}
	return agenticcompletion.CompletionContext{
		VerificationHint:         summary.VerificationHint,
		VerificationObserved:     summary.VerificationObserved,
		VerificationCommand:      summary.VerificationCommand,
		CompletionWarning:        summary.CompletionWarning,
		EditIntent:               summary.EditIntent,
		EditObserved:             summary.EditObserved,
		ReasoningEffort:          summary.ReasoningEffort,
		ReasoningEffortMode:      summary.ReasoningEffortMode,
		ReasoningEffortReason:    summary.ReasoningEffortReason,
		ProviderName:             summary.ProviderName,
		ProviderEffort:           summary.ProviderEffort,
		ProviderEffortNote:       summary.ProviderEffortNote,
		LoadedDeferredTools:      append([]string(nil), summary.LoadedDeferredTools...),
		SkillCatalogReceipts:     completionSkillCatalogReceiptsToAgentic(summary.SkillCatalogReceipts),
		SkillExecutionReceipts:   completionSkillExecutionReceiptsToAgentic(summary.SkillExecutionReceipts),
		CommandCatalogReceipts:   completionCommandCatalogReceiptsToAgentic(summary.CommandCatalogReceipts),
		ToolSearchReceipts:       completionToolSearchReceiptsToAgentic(summary.ToolSearchReceipts),
		ControlToolReceipts:      completionControlToolReceiptsToAgentic(summary.ControlToolReceipts),
		ConditionalSkillMatches:  completionSkillMatchesToAgentic(summary.ConditionalSkillMatches),
		CorrectionAttempted:      summary.CorrectionAttempted,
		CorrectionAttempts:       summary.CorrectionAttempts,
		CorrectionMaxAttempts:    summary.CorrectionMaxAttempts,
		CorrectionDecision:       summary.CorrectionDecision,
		CorrectionReason:         summary.CorrectionReason,
		CorrectionStatus:         summary.CorrectionStatus,
		CorrectionFailureFamily:  summary.CorrectionFailureFamily,
		CorrectionAttemptDetails: completionCorrectionAttemptDetailsToAgentic(summary.CorrectionAttemptDetails),
		RetryDecision:            summary.RetryDecision,
		RetryReason:              summary.RetryReason,
	}, nil
}

func completionCorrectionAttemptDetailsToAgentic(src []completionCorrectionAttemptReceipt) []agenticcompletion.CorrectionAttemptReceipt {
	if len(src) == 0 {
		return nil
	}
	out := make([]agenticcompletion.CorrectionAttemptReceipt, 0, len(src))
	for _, detail := range src {
		out = append(out, agenticcompletion.CorrectionAttemptReceipt{
			Attempt:             detail.Attempt,
			Decision:            detail.Decision,
			Reason:              detail.Reason,
			Status:              detail.Status,
			FailureFamily:       detail.FailureFamily,
			VerificationCommand: detail.VerificationCommand,
			CompletionWarning:   detail.CompletionWarning,
		})
	}
	return out
}

func completionSkillCatalogReceiptsToAgentic(src []completionSkillCatalogReceipt) []agenticcompletion.SkillCatalogReceipt {
	if len(src) == 0 {
		return nil
	}
	out := make([]agenticcompletion.SkillCatalogReceipt, 0, len(src))
	for _, receipt := range src {
		out = append(out, agenticcompletion.SkillCatalogReceipt{
			Tool:               receipt.Tool,
			Action:             receipt.Action,
			ReadOnly:           receipt.ReadOnly,
			RegistryAvailable:  receipt.RegistryAvailable,
			TotalSkills:        receipt.TotalSkills,
			ReturnedSkills:     receipt.ReturnedSkills,
			ReturnedMatches:    receipt.ReturnedMatches,
			TrustFilterApplied: receipt.TrustFilterApplied,
			AllowTrustLevels:   append([]string(nil), receipt.AllowTrustLevels...),
			MaxResults:         receipt.MaxResults,
			Query:              receipt.Query,
			Skill:              receipt.Skill,
			PathCount:          receipt.PathCount,
			CWDSet:             receipt.CWDSet,
			IncludePrompt:      receipt.IncludePrompt,
		})
	}
	return out
}

func completionSkillExecutionReceiptsToAgentic(src []completionSkillExecutionReceipt) []agenticcompletion.SkillExecutionReceipt {
	if len(src) == 0 {
		return nil
	}
	out := make([]agenticcompletion.SkillExecutionReceipt, 0, len(src))
	for _, receipt := range src {
		out = append(out, agenticcompletion.SkillExecutionReceipt{
			Tool:                receipt.Tool,
			Action:              receipt.Action,
			Skill:               receipt.Skill,
			Status:              receipt.Status,
			Provider:            receipt.Provider,
			Model:               receipt.Model,
			ReasoningEffort:     receipt.ReasoningEffort,
			ReasoningEffortMode: receipt.ReasoningEffortMode,
			PermissionMode:      receipt.PermissionMode,
			MaxIterations:       receipt.MaxIterations,
			RequiredTools:       append([]string(nil), receipt.RequiredTools...),
			AvailableTools:      append([]string(nil), receipt.AvailableTools...),
			ToolFilterApplied:   receipt.ToolFilterApplied,
			BaseDir:             receipt.BaseDir,
			Source:              receipt.Source,
			TrustLevel:          receipt.TrustLevel,
			External:            receipt.External,
			UserInvocable:       receipt.UserInvocable,
		})
	}
	return out
}

func completionCommandCatalogReceiptsToAgentic(src []completionCommandCatalogReceipt) []agenticcompletion.CommandCatalogReceipt {
	if len(src) == 0 {
		return nil
	}
	out := make([]agenticcompletion.CommandCatalogReceipt, 0, len(src))
	for _, receipt := range src {
		out = append(out, agenticcompletion.CommandCatalogReceipt{
			Tool:                  receipt.Tool,
			Action:                receipt.Action,
			ReadOnly:              receipt.ReadOnly,
			RegistryAvailable:     receipt.RegistryAvailable,
			ExecutionAvailable:    receipt.ExecutionAvailable,
			ExecutionPolicy:       receipt.ExecutionPolicy,
			TotalCommands:         receipt.TotalCommands,
			ReturnedCommands:      receipt.ReturnedCommands,
			ExecutableCommands:    receipt.ExecutableCommands,
			ModelCallableCommands: receipt.ModelCallableCommands,
			IncludeHidden:         receipt.IncludeHidden,
			MaxResults:            receipt.MaxResults,
			Query:                 receipt.Query,
			Command:               receipt.Command,
			FollowupTool:          receipt.FollowupTool,
		})
	}
	return out
}

func completionToolSearchReceiptsToAgentic(src []completionToolSearchReceipt) []agenticcompletion.ToolSearchReceipt {
	if len(src) == 0 {
		return nil
	}
	out := make([]agenticcompletion.ToolSearchReceipt, 0, len(src))
	for _, receipt := range src {
		out = append(out, agenticcompletion.ToolSearchReceipt{
			Tool:               receipt.Tool,
			Action:             receipt.Action,
			ReadOnly:           receipt.ReadOnly,
			RegistryAvailable:  receipt.RegistryAvailable,
			ExecutionAvailable: receipt.ExecutionAvailable,
			ExecutionPolicy:    receipt.ExecutionPolicy,
			TotalTools:         receipt.TotalTools,
			ReturnedMatches:    receipt.ReturnedMatches,
			DeferredMatches:    receipt.DeferredMatches,
			MaxResults:         receipt.MaxResults,
			AllowNamesCount:    receipt.AllowNamesCount,
			Query:              receipt.Query,
		})
	}
	return out
}

func completionControlToolReceiptsToAgentic(src []completionControlToolReceipt) []agenticcompletion.ControlToolReceipt {
	if len(src) == 0 {
		return nil
	}
	out := make([]agenticcompletion.ControlToolReceipt, 0, len(src))
	for _, receipt := range src {
		out = append(out, agenticcompletion.ControlToolReceipt{
			Tool:                    receipt.Tool,
			Action:                  receipt.Action,
			ReadOnly:                receipt.ReadOnly,
			Persistent:              receipt.Persistent,
			QueueBacked:             receipt.QueueBacked,
			RegistryBacked:          receipt.RegistryBacked,
			ExecutionAvailable:      receipt.ExecutionAvailable,
			ExecutionPolicy:         receipt.ExecutionPolicy,
			FollowupTool:            receipt.FollowupTool,
			TaskID:                  receipt.TaskID,
			ParentTaskID:            receipt.ParentTaskID,
			ChildTaskID:             receipt.ChildTaskID,
			QueueTaskID:             receipt.QueueTaskID,
			ProcessID:               receipt.ProcessID,
			DecisionID:              receipt.DecisionID,
			DecisionStatus:          receipt.DecisionStatus,
			Status:                  receipt.Status,
			PreviousStatus:          receipt.PreviousStatus,
			Terminal:                receipt.Terminal,
			ExitCode:                receipt.ExitCode,
			Found:                   receipt.Found,
			TimeoutMS:               receipt.TimeoutMS,
			CWD:                     receipt.CWD,
			TailBytes:               receipt.TailBytes,
			StdoutRawBytes:          receipt.StdoutRawBytes,
			StderrRawBytes:          receipt.StderrRawBytes,
			StdoutTruncated:         receipt.StdoutTruncated,
			StderrTruncated:         receipt.StderrTruncated,
			StopSignal:              receipt.StopSignal,
			EdgeType:                receipt.EdgeType,
			Enqueued:                receipt.Enqueued,
			Deduplicated:            receipt.Deduplicated,
			TotalReturned:           receipt.TotalReturned,
			Limit:                   receipt.Limit,
			Field:                   receipt.Field,
			RetrievalStatus:         receipt.RetrievalStatus,
			Name:                    receipt.Name,
			Path:                    receipt.Path,
			Branch:                  receipt.Branch,
			RegistryPath:            receipt.RegistryPath,
			Runner:                  receipt.Runner,
			IsError:                 receipt.IsError,
			Removed:                 receipt.Removed,
			DryRun:                  receipt.DryRun,
			Total:                   receipt.Total,
			TaskName:                receipt.TaskName,
			TaskCountBefore:         receipt.TaskCountBefore,
			TaskCountAfter:          receipt.TaskCountAfter,
			PreviousMode:            receipt.PreviousMode,
			CurrentMode:             receipt.CurrentMode,
			Restored:                receipt.Restored,
			ReadOnlyAfterTransition: receipt.ReadOnlyAfterTransition,
			FromActorID:             receipt.FromActorID,
			ToActorID:               receipt.ToActorID,
			ActorID:                 receipt.ActorID,
			HandoffID:               receipt.HandoffID,
			Box:                     receipt.Box,
			Delivered:               receipt.Delivered,
			Command:                 receipt.Command,
			Args:                    append([]string(nil), receipt.Args...),
			StateMutation:           receipt.StateMutation,
			QuestionChars:           receipt.QuestionChars,
			OptionCount:             receipt.OptionCount,
			AllowFreeText:           receipt.AllowFreeText,
			TimeoutSeconds:          receipt.TimeoutSeconds,
		})
	}
	return out
}

func completionSkillMatchesToAgentic(src []completionConditionalSkillMatch) []agenticcompletion.ConditionalSkillMatch {
	if len(src) == 0 {
		return nil
	}
	out := make([]agenticcompletion.ConditionalSkillMatch, 0, len(src))
	for _, match := range src {
		out = append(out, agenticcompletion.ConditionalSkillMatch{
			SkillName:  match.SkillName,
			Pattern:    match.Pattern,
			Path:       match.Path,
			Source:     match.Source,
			TrustLevel: match.TrustLevel,
			External:   match.External,
		})
	}
	return out
}
