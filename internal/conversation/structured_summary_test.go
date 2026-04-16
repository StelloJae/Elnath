package conversation

import (
	"strings"
	"testing"

	"github.com/stello/elnath/internal/llm"
)

func testStructuredSummary(nextAction string) string {
	return `# Session Summary

## 1. User goal
Ship Hermes parity without losing compression fidelity.

## 2. Completed steps
- Read the W2 spec
- Audited the current compression flow

## 3. Current focus
Wire structured compression into the Stage 2 summarizer.

## 4. Files touched
- internal/conversation/context.go - read/edit
- internal/conversation/structured_summary.go - write

## 5. Outstanding TODOs
- Add parser tests
- Add iterative-update prompt routing

## 6. Blockers / unresolved
(none)

## 7. Key decisions
- Keep legacy fallback available to tolerate malformed LLM output

## 8. Open questions
(none)

## 9. Next action
` + nextAction + `
`
}

func TestParseStructuredSummary_Valid(t *testing.T) {
	body, ok := parseStructuredSummary(testStructuredSummary("Run the targeted conversation tests."))
	if !ok {
		t.Fatal("expected valid structured summary to parse")
	}
	if !strings.HasPrefix(body, "# Session Summary") {
		t.Fatalf("body = %q, want Session Summary header", body)
	}
}

func TestParseStructuredSummary_MissingSection(t *testing.T) {
	content := strings.Replace(
		testStructuredSummary("Run the targeted conversation tests."),
		"\n## 8. Open questions\n(none)\n",
		"\n",
		1,
	)
	if _, ok := parseStructuredSummary(content); ok {
		t.Fatal("expected missing section to be rejected")
	}
}

func TestParseStructuredSummary_DuplicateSection(t *testing.T) {
	content := testStructuredSummary("Run the targeted conversation tests.") + "\n## 9. Next action\nShip the duplicate section.\n"
	if _, ok := parseStructuredSummary(content); ok {
		t.Fatal("expected duplicate section to be rejected")
	}
}

func TestParseStructuredSummary_PreambleRejected(t *testing.T) {
	content := "Here is the summary you asked for:\n\n" + testStructuredSummary("Run the targeted conversation tests.")
	if _, ok := parseStructuredSummary(content); ok {
		t.Fatal("expected preamble to be rejected")
	}
}

func TestParseStructuredSummary_ExtraHeadingRejected(t *testing.T) {
	content := testStructuredSummary("Run the targeted conversation tests.") + "\n## Appendix\nExtra notes.\n"
	if _, ok := parseStructuredSummary(content); ok {
		t.Fatal("expected extra heading to be rejected")
	}
}

func TestParseStructuredSummary_WrongHeader(t *testing.T) {
	content := strings.Replace(
		testStructuredSummary("Run the targeted conversation tests."),
		"# Session Summary",
		"# Not A Session Summary",
		1,
	)
	if _, ok := parseStructuredSummary(content); ok {
		t.Fatal("expected wrong header to be rejected")
	}
}

func TestIsStructuredSummaryMessage_Assistant(t *testing.T) {
	if !isStructuredSummaryMessage(llm.NewAssistantMessage(testStructuredSummary("Ship the change."))) {
		t.Fatal("expected assistant structured summary to be detected")
	}
}

func TestIsStructuredSummaryMessage_User(t *testing.T) {
	if isStructuredSummaryMessage(llm.NewUserMessage(testStructuredSummary("Ship the change."))) {
		t.Fatal("expected user message never to be treated as structured summary")
	}
}
