package llm

import (
	"context"
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
