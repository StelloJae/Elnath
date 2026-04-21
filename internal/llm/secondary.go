package llm

import (
	"context"
	"fmt"
)

// SecondaryModelCaller abstracts the "apply prompt to fetched markdown"
// step that Claude Code delegates to Haiku in
// /Users/stello/claude-code-src/src/tools/WebFetchTool/utils.ts:484-530.
// WebFetch invokes it after HTML→markdown conversion when the caller
// supplied a prompt, swapping the bulk markdown for a focused extract.
//
// Implementations decide which provider / model to route to; the default
// returned by NewSecondaryModelCaller is a no-op that passes the markdown
// through unchanged, keeping backward compatibility with every call site
// that does not yet wire a real secondary. A concrete implementation is
// expected to land at the daemon/chat wire-site once dogfood evidence
// motivates paying the extra API round-trip per web_fetch.
type SecondaryModelCaller interface {
	Extract(ctx context.Context, markdown, prompt string, isPreapproved bool) (string, error)
}

// noopCaller is the fallback returned when no real secondary is wired.
// It returns the markdown verbatim so a web_fetch call with a prompt still
// produces a useful (if verbose) result.
type noopCaller struct{}

func (noopCaller) Extract(_ context.Context, markdown, _ string, _ bool) (string, error) {
	return markdown, nil
}

// NewNoopSecondaryModelCaller returns the pass-through implementation.
// Exported so callers and tests can obtain a noop without fabricating a
// Provider — the name documents intent at the callsite.
func NewNoopSecondaryModelCaller() SecondaryModelCaller {
	return noopCaller{}
}

// NewSecondaryModelCaller selects a SecondaryModelCaller for the supplied
// Provider. Phase A.5 ships only the interface + noop default — real
// per-provider dispatch (Anthropic Haiku, OpenAI/Responses mini, Codex
// OAuth mini) is added at the wire-site once dogfood evidence motivates
// the extra API round-trip. Nil or unrecognised providers return the
// no-op so the call path stays byte-for-byte compatible with the Phase
// A.4 build.
func NewSecondaryModelCaller(_ Provider) SecondaryModelCaller {
	return noopCaller{}
}

// makeSecondaryModelPrompt mirrors Claude Code's prompt.ts:23-46 verbatim:
// preapproved hosts (documentation, code references) get a short
// "include relevant details, code examples, and documentation excerpts
// as needed" directive, while everything else carries the 125-char quote
// cap and the lyrics/legality/quotation guardrails. The surrounding
// frame ("Web page content:\n---\n…\n---\n\n<prompt>\n\n<guidelines>\n")
// is byte-for-byte identical to the upstream template literal.
func makeSecondaryModelPrompt(markdown, prompt string, isPreapproved bool) string {
	const preapprovedGuidelines = `Provide a concise response based on the content above. Include relevant details, code examples, and documentation excerpts as needed.`

	const defaultGuidelines = `Provide a concise response based only on the content above. In your response:
 - Enforce a strict 125-character maximum for quotes from any source document. Open Source Software is ok as long as we respect the license.
 - Use quotation marks for exact language from articles; any language outside of the quotation should never be word-for-word the same.
 - You are not a lawyer and never comment on the legality of your own prompts and responses.
 - Never produce or reproduce exact song lyrics.`

	guidelines := defaultGuidelines
	if isPreapproved {
		guidelines = preapprovedGuidelines
	}
	return fmt.Sprintf("\nWeb page content:\n---\n%s\n---\n\n%s\n\n%s\n", markdown, prompt, guidelines)
}

// MakeSecondaryModelPrompt is the exported alias real SecondaryModelCaller
// implementations will use when assembling their API request. Keeping the
// internal helper lowercase lets the tests pin the exact format without
// broadening the package's public surface more than necessary.
func MakeSecondaryModelPrompt(markdown, prompt string, isPreapproved bool) string {
	return makeSecondaryModelPrompt(markdown, prompt, isPreapproved)
}
