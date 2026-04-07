package self

import (
	"strings"
	"testing"
)

func TestBuildSystemPrompt_StaticSection(t *testing.T) {
	state := New(t.TempDir())

	prompt := BuildSystemPrompt(state, "")

	checks := []struct {
		name string
		want string
	}{
		{"contains name", "You are Elnath."},
		{"contains mission", "Mission: "},
		{"contains vibe", "Vibe: "},
		{"contains curiosity", "curiosity=0.50"},
		{"contains verbosity", "verbosity=0.50"},
		{"contains boundary", dynamicBoundary},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(prompt, tc.want) {
				t.Errorf("prompt missing %q", tc.want)
			}
		})
	}
}

func TestBuildSystemPrompt_WithWiki(t *testing.T) {
	state := New(t.TempDir())
	wiki := "Stella changed auth module on 2026-04-06."

	prompt := BuildSystemPrompt(state, wiki)

	if !strings.Contains(prompt, "Relevant knowledge from wiki:") {
		t.Error("missing wiki section header")
	}
	if !strings.Contains(prompt, wiki) {
		t.Error("missing wiki content")
	}
}

func TestBuildSystemPrompt_EmptyWiki(t *testing.T) {
	state := New(t.TempDir())

	prompt := BuildSystemPrompt(state, "")

	if strings.Contains(prompt, "Relevant knowledge from wiki:") {
		t.Error("should not include wiki section when summary is empty")
	}
}

func TestBuildSystemPrompt_CustomPersona(t *testing.T) {
	state := New(t.TempDir())
	state.ApplyLessons([]Lesson{
		{Param: "curiosity", Delta: 0.3},
		{Param: "verbosity", Delta: -0.4},
	})

	prompt := BuildSystemPrompt(state, "")

	if !strings.Contains(prompt, "curiosity=0.80") {
		t.Errorf("expected curiosity=0.80 in prompt, got: %s", prompt)
	}
	if !strings.Contains(prompt, "verbosity=0.10") {
		t.Errorf("expected verbosity=0.10 in prompt, got: %s", prompt)
	}
}

func TestBuildSystemPrompt_BoundaryPosition(t *testing.T) {
	state := New(t.TempDir())

	prompt := BuildSystemPrompt(state, "some wiki data")

	idx := strings.Index(prompt, dynamicBoundary)
	if idx == -1 {
		t.Fatal("boundary not found")
	}

	static := prompt[:idx]
	dynamic := prompt[idx+len(dynamicBoundary):]

	if !strings.Contains(static, "You are Elnath") {
		t.Error("static section should contain identity")
	}
	if !strings.Contains(dynamic, "some wiki data") {
		t.Error("dynamic section should contain wiki data")
	}
}
