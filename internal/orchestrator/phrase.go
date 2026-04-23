package orchestrator

import "strings"

// NormalizeForPhraseMatch canonicalizes prompt text for substring phrase
// matching. Lowercases, replaces markdown inline-code backticks with
// spaces, and collapses whitespace runs to single spaces.
//
// Phase 8.2 Fix 5: the backtick replacement is load-bearing — prompts
// like "Author `.github/workflows/ci.yml`" previously missed phrase
// rules keyed on "author .github" because strings.Contains treats the
// backtick and space as distinct characters. Both the router classifier
// (buildRoutingContext) and the ralph inline-eligibility guard
// (isInlineEligibleTask) depend on the same normalization so routing
// and inline-acceptance decisions stay in sync.
func NormalizeForPhraseMatch(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "`", " ")
	s = strings.Join(strings.Fields(s), " ")
	return s
}
