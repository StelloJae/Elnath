package reflection

import (
	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/agent/errorclass"
)

// skipCategories enumerates error classes where reflection would duplicate
// existing agent-level recovery signals (retry, credential rotate, fallback).
// Reflecting on them would either waste LLM calls or risk rate-limit cascades.
var skipCategories = map[errorclass.Category]struct{}{
	errorclass.RateLimit:     {},
	errorclass.Auth:          {},
	errorclass.AuthPermanent: {},
	errorclass.Billing:       {},
	errorclass.Overloaded:    {},
}

// ShouldReflect gates the agent-side reflection hook. It returns true only
// when the finish reason is a reflect-worthy failure AND no skip rule fires.
//
// Phase 0 skip rules (spec §3.1):
//   - FinishReasonPartialSuccess is NOT triggered (C1 regression).
//   - FinishReasonStop is normal completion.
//   - errorclass {RateLimit, Auth, AuthPermanent, Billing, Overloaded} are
//     already handled by provider-level recovery.
//   - user cancellation and destructive-approved operations are out of scope.
func ShouldReflect(
	finishReason agent.FinishReason,
	errCategory errorclass.Category,
	userCancelled bool,
	destructiveUserApproved bool,
) bool {
	if userCancelled || destructiveUserApproved {
		return false
	}
	if _, skip := skipCategories[errCategory]; skip {
		return false
	}
	switch finishReason {
	case agent.FinishReasonError,
		agent.FinishReasonBudgetExceeded,
		agent.FinishReasonAckLoop:
		return true
	default:
		return false
	}
}
