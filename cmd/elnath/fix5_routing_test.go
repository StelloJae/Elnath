package main

import "testing"

// TestBuildRoutingContext_BacktickedAuthor locks in the Phase 8.2 Fix 5
// behavior on the P19 prompt. The pre-Fix5 classifier missed the
// newWorkPhrase "author .github" because the backtick prevented the
// substring match and left VerificationHint=true — that combined with
// "repo" (ExistingCode) forced routeComplexTask to pick "ralph".
//
// After Fix5 the normalized lookup recovers the match, VerificationHint
// stays false, and the ralph gate (`ExistingCode && VerificationHint`) is
// no longer tripped. Ultimate route (team vs single) depends on
// estimateFiles and is out of Fix 5 scope — the Fix 5 contract is
// specifically "ralph avoidance via phrase-match restoration".
func TestBuildRoutingContext_BacktickedAuthor(t *testing.T) {
	prompt := "Author `.github/workflows/ci.yml` that on `push` to `main`:\n" +
		"1. Checks out the repo\n" +
		"2. Sets up Go 1.22\n" +
		"3. Runs `go test -race ./...`\n" +
		"4. Caches the Go module cache between runs"
	ctx := buildRoutingContext(prompt)
	if ctx.VerificationHint {
		t.Errorf("VerificationHint = true, want false (newWorkPhrase 'author .github' should suppress via backtick-aware normalize)")
	}
	if ctx.ExplicitWorkflow != "" {
		t.Errorf("ExplicitWorkflow = %q, want empty (no prefix)", ctx.ExplicitWorkflow)
	}
	// ExistingCode is allowed to stay true here — "repo" is a genuine cue
	// per Fix 1's semantic-truth preservation. The ralph gate still opens
	// only when both flags are true, so VerHint=false alone is enough.
}

// TestBuildRoutingContext_NonBacktickedP07Regression guards the P07 prompt
// ("Write a reusable async rate limiter...") against regression. Fix5 must
// not disturb non-backticked prompts that Fix1 already routed to single.
func TestBuildRoutingContext_NonBacktickedP07Regression(t *testing.T) {
	prompt := "Write a reusable async rate limiter (token bucket) that wraps " +
		"aiohttp.ClientSession. Include a unit test."
	ctx := buildRoutingContext(prompt)
	if ctx.VerificationHint {
		t.Errorf("VerificationHint = true, want false (newWorkPhrase 'write a reusable'/'include a unit test')")
	}
	if ctx.EstimatedFiles >= 4 {
		t.Errorf("EstimatedFiles = %d, want < 4", ctx.EstimatedFiles)
	}
}

// TestBuildRoutingContext_NonBacktickedP16Regression guards the P16 prompt
// ("Write tests for Python function parsing JSON...") against regression.
func TestBuildRoutingContext_NonBacktickedP16Regression(t *testing.T) {
	prompt := "Write tests for a Python function that parses JSON input. " +
		"Cover malformed JSON and empty strings."
	ctx := buildRoutingContext(prompt)
	if ctx.VerificationHint {
		t.Errorf("VerificationHint = true, want false (newWorkPhrase 'write tests')")
	}
}

// TestBuildRoutingContext_ExplicitRalphOverride_Backticked confirms the
// power-user override semantics survive the backtick-aware matcher: a
// leading "[ralph]" prefix should still pin ExplicitWorkflow regardless of
// whether the rest of the prompt would otherwise normalize to an
// inline-friendly phrase.
func TestBuildRoutingContext_ExplicitRalphOverride_Backticked(t *testing.T) {
	prompt := "[ralph] Author `.github/workflows/ci.yml` that on `push` to `main`."
	ctx := buildRoutingContext(prompt)
	if ctx.ExplicitWorkflow != "ralph" {
		t.Errorf("ExplicitWorkflow = %q, want \"ralph\"", ctx.ExplicitWorkflow)
	}
}
