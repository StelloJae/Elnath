package self

import "testing"

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
