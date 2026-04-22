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

// secondaryModelByProvider maps a Provider.Name() value to the small/fast
// model id that should handle secondary extracts. Ollama intentionally has
// no entry — local setups lack a reliable "fast" tier, so the noop caller
// (which passes the markdown through unchanged) stays the safer default.
// Model ids mirror the ones used elsewhere in Elnath: Anthropic uses the
// date-less alias introduced by F-5 (2026-04-14 `d58e8bc`), and the OpenAI
// family — including Codex OAuth — uses gpt-5.4-mini for fast extraction.
var secondaryModelByProvider = map[string]string{
	"anthropic":        "claude-haiku-4-5",
	"codex":            "gpt-5.4-mini",
	"openai":           "gpt-5.4-mini",
	"openai-responses": "gpt-5.4-mini",
}

const secondaryModelMaxTokens = 2048

// NewSecondaryModelCaller selects a SecondaryModelCaller for the supplied
// Provider. The Phase A.5 closure (this function, FU-SecondaryWireUp)
// inspects Provider.Name() and returns a realCaller that forwards
// (markdown, prompt, isPreapproved) through makeSecondaryModelPrompt and
// calls Chat on the small/fast model associated with that provider. Nil
// providers, Ollama, and any unrecognised provider still receive the noop
// caller so the WebFetch call path stays byte-for-byte compatible with
// the Phase A.4 build on those setups.
func NewSecondaryModelCaller(p Provider) SecondaryModelCaller {
	if p == nil {
		return noopCaller{}
	}
	model, ok := secondaryModelByProvider[p.Name()]
	if !ok {
		return noopCaller{}
	}
	return &realCaller{provider: p, model: model, maxTokens: secondaryModelMaxTokens}
}

// realCaller is the concrete SecondaryModelCaller returned for providers
// with a known small/fast model. It formats the markdown + user prompt via
// makeSecondaryModelPrompt (Claude Code prompt.ts:23-46 parity), sends the
// resulting user-role message to Provider.Chat, and returns the text body
// of the response. An empty response falls back to the Claude Code
// "No response from model" string (utils.ts:529 parity) so callers always
// see a non-empty extract or an explicit error.
type realCaller struct {
	provider  Provider
	model     string
	maxTokens int
}

func (c *realCaller) Extract(ctx context.Context, markdown, prompt string, isPreapproved bool) (string, error) {
	userPrompt := makeSecondaryModelPrompt(markdown, prompt, isPreapproved)
	req := ChatRequest{
		Model:     c.model,
		Messages:  []Message{NewUserMessage(userPrompt)},
		MaxTokens: c.maxTokens,
	}
	resp, err := c.provider.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("secondary chat: %w", err)
	}
	if resp == nil || resp.Content == "" {
		return "No response from model", nil
	}
	return resp.Content, nil
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
