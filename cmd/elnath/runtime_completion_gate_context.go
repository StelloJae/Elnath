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
		VerificationHint:        summary.VerificationHint,
		VerificationObserved:    summary.VerificationObserved,
		VerificationCommand:     summary.VerificationCommand,
		CompletionWarning:       summary.CompletionWarning,
		EditIntent:              summary.EditIntent,
		EditObserved:            summary.EditObserved,
		ReasoningEffort:         summary.ReasoningEffort,
		ReasoningEffortMode:     summary.ReasoningEffortMode,
		ReasoningEffortReason:   summary.ReasoningEffortReason,
		ProviderName:            summary.ProviderName,
		ProviderEffort:          summary.ProviderEffort,
		ProviderEffortNote:      summary.ProviderEffortNote,
		LoadedDeferredTools:     append([]string(nil), summary.LoadedDeferredTools...),
		SkillCatalogReceipts:    completionSkillCatalogReceiptsToAgentic(summary.SkillCatalogReceipts),
		ConditionalSkillMatches: completionSkillMatchesToAgentic(summary.ConditionalSkillMatches),
		CorrectionAttempted:     summary.CorrectionAttempted,
		CorrectionAttempts:      summary.CorrectionAttempts,
		CorrectionMaxAttempts:   summary.CorrectionMaxAttempts,
		CorrectionDecision:      summary.CorrectionDecision,
		CorrectionReason:        summary.CorrectionReason,
		CorrectionStatus:        summary.CorrectionStatus,
		CorrectionFailureFamily: summary.CorrectionFailureFamily,
		RetryDecision:           summary.RetryDecision,
		RetryReason:             summary.RetryReason,
	}, nil
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
