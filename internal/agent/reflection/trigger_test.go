package reflection

import (
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/agent/errorclass"
)

func TestShouldReflect_TriggerReasons(t *testing.T) {
	cases := []struct {
		name   string
		reason agent.FinishReason
		want   bool
	}{
		{"Error triggers", agent.FinishReasonError, true},
		{"BudgetExceeded triggers", agent.FinishReasonBudgetExceeded, true},
		{"AckLoop triggers", agent.FinishReasonAckLoop, true},
		{"Stop does not trigger", agent.FinishReasonStop, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ShouldReflect(tc.reason, "", false, false)
			if got != tc.want {
				t.Fatalf("ShouldReflect(%s)=%v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}

// TestShouldReflect_PartialSuccess_NOT_Triggered is a C1 regression test.
// learning/outcome.go:36 already treats partial_success as successful; observing
// it again as a self-heal attempt would double-count and bias RoutingAdvisor.
func TestShouldReflect_PartialSuccess_NOT_Triggered(t *testing.T) {
	got := ShouldReflect(agent.FinishReasonPartialSuccess, "", false, false)
	if got {
		t.Fatal("partial_success must NOT trigger reflection (C1 regression; see outcome.go:36)")
	}
}

func TestShouldReflect_CategorySkips(t *testing.T) {
	skipped := []errorclass.Category{
		errorclass.RateLimit,
		errorclass.Auth,
		errorclass.AuthPermanent,
		errorclass.Billing,
		errorclass.Overloaded,
	}
	for _, cat := range skipped {
		t.Run(string(cat), func(t *testing.T) {
			if ShouldReflect(agent.FinishReasonError, cat, false, false) {
				t.Fatalf("category %s must skip reflection", cat)
			}
		})
	}
}

func TestShouldReflect_NonSkippedCategory(t *testing.T) {
	// Categories outside the skip set still trigger on a reflect-worthy reason.
	cats := []errorclass.Category{
		errorclass.ServerError,
		errorclass.Timeout,
		errorclass.ContextOverflow,
		errorclass.Unknown,
		"", // empty (unclassified) should still reflect
	}
	for _, cat := range cats {
		t.Run(string(cat), func(t *testing.T) {
			if !ShouldReflect(agent.FinishReasonError, cat, false, false) {
				t.Fatalf("category %q should not block reflection", cat)
			}
		})
	}
}

func TestShouldReflect_UserCancelled_Skip(t *testing.T) {
	if ShouldReflect(agent.FinishReasonError, "", true, false) {
		t.Fatal("user-cancelled runs must skip reflection")
	}
}

func TestShouldReflect_DestructiveApproved_Skip(t *testing.T) {
	if ShouldReflect(agent.FinishReasonError, "", false, true) {
		t.Fatal("destructive tool + user_approved must skip reflection")
	}
}
