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

func TestBuildSystemPromptWithPersona(t *testing.T) {
	state := New(t.TempDir())

	prompt := BuildSystemPromptWithPersona(state, "", "Focus on research: generate hypotheses.")

	if !strings.Contains(prompt, "Focus on research") {
		t.Error("persona extra text not included in prompt")
	}
	// Persona text should come before the tools section.
	personaIdx := strings.Index(prompt, "Focus on research")
	toolsIdx := strings.Index(prompt, "You have access to tools")
	if personaIdx > toolsIdx {
		t.Error("persona text should appear before tools description")
	}
}

func TestBuildSystemPromptWithPersona_Empty(t *testing.T) {
	state := New(t.TempDir())

	withExtra := BuildSystemPromptWithPersona(state, "", "")
	withoutExtra := BuildSystemPrompt(state, "")

	if withExtra != withoutExtra {
		t.Error("empty personaExtra should produce same output as BuildSystemPrompt")
	}
}

func TestPreset(t *testing.T) {
	presets := ValidPresets()
	if len(presets) != 4 {
		t.Fatalf("expected 4 presets, got %d", len(presets))
	}

	for _, name := range presets {
		t.Run(string(name), func(t *testing.T) {
			persona, extra := Preset(name)
			if extra == "" {
				t.Errorf("preset %q should have non-empty extra text", name)
			}
			// All persona values should be in [0, 1].
			for _, v := range []float64{persona.Curiosity, persona.Verbosity, persona.Caution, persona.Creativity, persona.Persistence} {
				if v < 0 || v > 1 {
					t.Errorf("preset %q has out-of-range value %.2f", name, v)
				}
			}
		})
	}
}

func TestPreset_Default(t *testing.T) {
	persona, extra := Preset(PresetDefault)
	if extra != "" {
		t.Error("default preset should have empty extra text")
	}
	def := DefaultPersona()
	if persona != def {
		t.Errorf("default preset persona = %+v, want %+v", persona, def)
	}
}

func TestPreset_Unknown(t *testing.T) {
	persona, extra := Preset(PresetName("nonexistent"))
	if extra != "" {
		t.Error("unknown preset should return empty extra")
	}
	def := DefaultPersona()
	if persona != def {
		t.Errorf("unknown preset should return default persona")
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
