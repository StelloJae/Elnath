package orchestrator

import "testing"

func TestNormalizeForPhraseMatch(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lowercases", "AUTHOR", "author"},
		{"backtick to space", "Author `.github/...`", "author .github/..."},
		{"collapses whitespace", "a  b\tc\nd", "a b c d"},
		{"mixed", "Author\t`.github/workflows/ci.yml`", "author .github/workflows/ci.yml"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeForPhraseMatch(tc.in); got != tc.want {
				t.Errorf("NormalizeForPhraseMatch(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsInlineEligibleTask_BacktickedAuthor(t *testing.T) {
	// Phase 8.2 Fix 5 regression: P19 prompt wraps the target path in
	// markdown backticks, so the pre-Fix5 substring match missed
	// "author .github". NormalizeForPhraseMatch must restore the match.
	prompt := "Author `.github/workflows/ci.yml` that on `push` to `main`."
	if !isInlineEligibleTask(prompt) {
		t.Fatalf("isInlineEligibleTask(%q) = false, want true", prompt)
	}
}

func TestIsInlineEligibleTask_BacktickedUnitTest(t *testing.T) {
	// "write a unit test" must continue to register even when the callee
	// is wrapped in backticks, which is common for inline code references.
	prompt := "Write a unit test for `foo.Bar` in `internal/bar/bar.go`."
	if !isInlineEligibleTask(prompt) {
		t.Fatalf("isInlineEligibleTask(%q) = false, want true", prompt)
	}
}

func TestIsInlineEligibleTask_FileModBlock(t *testing.T) {
	// File-modification phrases must keep blocking inline eligibility even
	// after normalization — the block list is semantic, not syntactic.
	prompt := "Please update cmd/foo/bar.go to handle the new flag."
	if isInlineEligibleTask(prompt) {
		t.Fatalf("isInlineEligibleTask(%q) = true, want false (file-mod blocked)", prompt)
	}
}
