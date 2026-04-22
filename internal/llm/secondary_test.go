package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestNewSecondaryModelCaller_UnknownProviderReturnsNoop pins Phase A.5's
// default dispatch: callers that pass nil (or any provider the dispatcher
// does not yet know about) get a no-op caller so the WebFetch path stays
// byte-for-byte compatible with the Phase A.4 build.
func TestNewSecondaryModelCaller_UnknownProviderReturnsNoop(t *testing.T) {
	c := NewSecondaryModelCaller(nil)
	got, err := c.Extract(context.Background(), "original markdown", "extract price", true)
	if err != nil {
		t.Fatalf("noop Extract error: %v", err)
	}
	if got != "original markdown" {
		t.Errorf("noop Extract should return markdown verbatim; got %q", got)
	}
}

// TestNoopCaller_IgnoresPromptAndFlag guards against a noop that leaks
// the user prompt into the output. The whole point of the default path is
// that prompt + isPreapproved are dropped.
func TestNoopCaller_IgnoresPromptAndFlag(t *testing.T) {
	c := NewNoopSecondaryModelCaller()
	got, err := c.Extract(context.Background(), "## H1", "this should NOT appear", false)
	if err != nil {
		t.Fatalf("noop Extract error: %v", err)
	}
	if got != "## H1" {
		t.Errorf("noop Extract unexpected output: %q", got)
	}
	if strings.Contains(got, "should NOT appear") {
		t.Errorf("noop leaked prompt into output: %q", got)
	}
}

// TestMakeSecondaryModelPrompt_PreapprovedGuideline pins the prompt.ts
// branch that fires for trusted documentation hosts — no quote cap,
// explicit permission to include code examples / documentation excerpts.
func TestMakeSecondaryModelPrompt_PreapprovedGuideline(t *testing.T) {
	out := MakeSecondaryModelPrompt("## Hello", "summarize", true)

	mustContain := []string{"## Hello", "summarize", "code examples", "documentation excerpts"}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("preapproved prompt missing %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "125-character") {
		t.Errorf("preapproved should NOT carry the 125-char quote cap:\n%s", out)
	}
	if strings.Contains(out, "song lyrics") {
		t.Errorf("preapproved should NOT carry the lyrics ban:\n%s", out)
	}
}

// TestMakeSecondaryModelPrompt_DefaultGuideline pins the fallback branch
// covering the 125-char quote cap, the legality disclaimer, and the
// lyrics ban verbatim from prompt.ts:30-34.
func TestMakeSecondaryModelPrompt_DefaultGuideline(t *testing.T) {
	out := MakeSecondaryModelPrompt("## Hello", "summarize", false)

	mustContain := []string{
		"## Hello",
		"summarize",
		"125-character",
		"Open Source Software is ok",
		"never comment on the legality",
		"song lyrics",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("default prompt missing %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "code examples") {
		t.Errorf("default branch must not carry the 'code examples' permission:\n%s", out)
	}
}

// TestMakeSecondaryModelPrompt_FrameStructure guards the template-literal
// frame so changes to the surrounding scaffold can't silently drift away
// from Claude Code's prompt.ts:36-45.
func TestMakeSecondaryModelPrompt_FrameStructure(t *testing.T) {
	out := MakeSecondaryModelPrompt("BODY", "ASK", true)

	if !strings.HasPrefix(out, "\nWeb page content:\n---\nBODY\n---\n\nASK\n\n") {
		t.Errorf("frame prefix drifted from prompt.ts template literal:\n%s", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("frame missing trailing newline (template literal ends with \\n):\n%s", out)
	}
}

// mockSecondaryProvider is the scripted Provider used by the
// FU-SecondaryWireUp tests. It captures the last ChatRequest so we can
// assert that NewSecondaryModelCaller's realCaller forwards the prompt
// via makeSecondaryModelPrompt, targets the right model, and propagates
// Provider errors faithfully.
type mockSecondaryProvider struct {
	name      string
	lastReq   ChatRequest
	callCount int
	respText  string
	respErr   error
}

func (m *mockSecondaryProvider) Name() string { return m.name }
func (m *mockSecondaryProvider) Chat(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	m.lastReq = req
	m.callCount++
	if m.respErr != nil {
		return nil, m.respErr
	}
	return &ChatResponse{Content: m.respText}, nil
}
func (m *mockSecondaryProvider) Stream(_ context.Context, _ ChatRequest, _ func(StreamEvent)) error {
	return nil
}
func (m *mockSecondaryProvider) Models() []ModelInfo { return nil }

// TestNewSecondaryModelCaller_AnthropicReturnsRealCaller pins FU-SecondaryWireUp:
// an Anthropic provider must dispatch to the Haiku fast model, not fall
// through to the noop.
func TestNewSecondaryModelCaller_AnthropicReturnsRealCaller(t *testing.T) {
	p := &mockSecondaryProvider{name: "anthropic", respText: "extracted"}
	c := NewSecondaryModelCaller(p)
	if _, ok := c.(noopCaller); ok {
		t.Fatalf("Anthropic provider should dispatch to realCaller, got noopCaller")
	}
	if _, err := c.Extract(context.Background(), "md", "prompt", true); err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if p.lastReq.Model != "claude-haiku-4-5" {
		t.Errorf("Anthropic secondary should target claude-haiku-4-5, got %q", p.lastReq.Model)
	}
}

// TestNewSecondaryModelCaller_CodexReturnsRealCaller pins the Codex OAuth
// dispatch to gpt-5.4-mini. Codex is the partner's default provider, so
// this is the most exercised path in dogfood.
func TestNewSecondaryModelCaller_CodexReturnsRealCaller(t *testing.T) {
	p := &mockSecondaryProvider{name: "codex", respText: "extracted"}
	c := NewSecondaryModelCaller(p)
	if _, ok := c.(noopCaller); ok {
		t.Fatalf("Codex provider should dispatch to realCaller, got noopCaller")
	}
	if _, err := c.Extract(context.Background(), "md", "prompt", true); err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if p.lastReq.Model != "gpt-5.4-mini" {
		t.Errorf("Codex secondary should target gpt-5.4-mini, got %q", p.lastReq.Model)
	}
}

// TestNewSecondaryModelCaller_OpenAIAndResponsesReturnRealCaller covers the
// direct OpenAI provider and the Responses API path — both should dispatch
// to the same gpt-5.4-mini target.
func TestNewSecondaryModelCaller_OpenAIAndResponsesReturnRealCaller(t *testing.T) {
	for _, name := range []string{"openai", "openai-responses"} {
		t.Run(name, func(t *testing.T) {
			p := &mockSecondaryProvider{name: name, respText: "extracted"}
			c := NewSecondaryModelCaller(p)
			if _, ok := c.(noopCaller); ok {
				t.Fatalf("%s provider should dispatch to realCaller, got noopCaller", name)
			}
			if _, err := c.Extract(context.Background(), "md", "prompt", true); err != nil {
				t.Fatalf("Extract error: %v", err)
			}
			if p.lastReq.Model != "gpt-5.4-mini" {
				t.Errorf("%s secondary should target gpt-5.4-mini, got %q", name, p.lastReq.Model)
			}
		})
	}
}

// TestNewSecondaryModelCaller_OllamaReturnsNoop guards the safer default
// for local providers — Ollama has no reliable "fast" tier, so the caller
// should stay the pass-through noop.
func TestNewSecondaryModelCaller_OllamaReturnsNoop(t *testing.T) {
	p := &mockSecondaryProvider{name: "ollama", respText: "extracted"}
	c := NewSecondaryModelCaller(p)
	if _, ok := c.(noopCaller); !ok {
		t.Fatalf("Ollama provider should fall through to noopCaller; got %T", c)
	}
	got, err := c.Extract(context.Background(), "original md", "prompt", true)
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if got != "original md" {
		t.Errorf("Ollama noop should return markdown verbatim; got %q", got)
	}
	if p.callCount != 0 {
		t.Errorf("Ollama provider Chat must not be invoked by noopCaller; calls=%d", p.callCount)
	}
}

// TestRealCaller_ExtractSendsFormattedPrompt pins the prompt.ts frame
// crossing the provider boundary — the user-role message delivered to
// Chat must be the output of makeSecondaryModelPrompt so the secondary
// model receives the exact preapproved/default guideline mix.
func TestRealCaller_ExtractSendsFormattedPrompt(t *testing.T) {
	p := &mockSecondaryProvider{name: "anthropic", respText: "summary"}
	c := NewSecondaryModelCaller(p)

	_, err := c.Extract(context.Background(), "BODY123", "ASK456", false)
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	if len(p.lastReq.Messages) != 1 {
		t.Fatalf("expected 1 user message in ChatRequest, got %d", len(p.lastReq.Messages))
	}
	msg := p.lastReq.Messages[0]
	if msg.Role != RoleUser {
		t.Errorf("secondary message role should be user, got %q", msg.Role)
	}
	text := msg.Text()
	if !strings.Contains(text, "BODY123") || !strings.Contains(text, "ASK456") {
		t.Errorf("ChatRequest user message missing markdown/prompt: %q", text)
	}
	if !strings.Contains(text, "125-character") {
		t.Errorf("non-preapproved guideline missing from user message: %q", text)
	}
	if p.lastReq.MaxTokens != secondaryModelMaxTokens {
		t.Errorf("MaxTokens drift: got %d, want %d", p.lastReq.MaxTokens, secondaryModelMaxTokens)
	}
}

// TestRealCaller_ExtractReturnsContent guards the happy path: the Chat
// response's Content body is surfaced verbatim to the caller.
func TestRealCaller_ExtractReturnsContent(t *testing.T) {
	p := &mockSecondaryProvider{name: "codex", respText: "AAPL: $180"}
	c := NewSecondaryModelCaller(p)
	got, err := c.Extract(context.Background(), "md", "extract price", true)
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if got != "AAPL: $180" {
		t.Errorf("Extract should return Chat Content verbatim; got %q", got)
	}
}

// TestRealCaller_ExtractEmptyResponseFallback pins Claude Code utils.ts:529
// parity: an empty Chat response becomes "No response from model" rather
// than an empty string that downstream callers might mis-handle.
func TestRealCaller_ExtractEmptyResponseFallback(t *testing.T) {
	p := &mockSecondaryProvider{name: "codex", respText: ""}
	c := NewSecondaryModelCaller(p)
	got, err := c.Extract(context.Background(), "md", "prompt", true)
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if got != "No response from model" {
		t.Errorf(`expected "No response from model" fallback, got %q`, got)
	}
}

// TestRealCaller_ExtractChatErrorPropagates guarantees that a Provider
// error wraps back up to the caller — WebFetch.Execute then surfaces it
// as IsError=true and keeps the URL out of the LRU, per Phase A.5 design.
func TestRealCaller_ExtractChatErrorPropagates(t *testing.T) {
	p := &mockSecondaryProvider{name: "codex", respErr: errors.New("rate limited")}
	c := NewSecondaryModelCaller(p)
	_, err := c.Extract(context.Background(), "md", "prompt", true)
	if err == nil {
		t.Fatal("expected Chat error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error should carry underlying message: %v", err)
	}
}
