package self

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNew(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load from nonexistent dir: %v", err)
	}
	id := DefaultIdentity()
	if s.Identity.Name != id.Name {
		t.Errorf("identity name: got %q, want %q", s.Identity.Name, id.Name)
	}
	p := DefaultPersona()
	if s.Persona.Curiosity != p.Curiosity {
		t.Errorf("persona curiosity: got %v, want %v", s.Persona.Curiosity, p.Curiosity)
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	s.Identity.Name = "Rigel"
	s.Persona.Verbosity = 0.9

	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s2.Identity.Name != "Rigel" {
		t.Errorf("name: got %q, want %q", s2.Identity.Name, "Rigel")
	}
	if s2.Persona.Verbosity != 0.9 {
		t.Errorf("verbosity: got %v, want 0.9", s2.Persona.Verbosity)
	}
}

func TestApplyLessons(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	lessons := []Lesson{
		{Param: "curiosity", Delta: 0.3},
		{Param: "caution", Delta: -0.1},
	}
	p := s.ApplyLessons(lessons)

	if p.Curiosity != 0.8 {
		t.Errorf("curiosity: got %v, want 0.8", p.Curiosity)
	}
	if p.Caution != 0.4 {
		t.Errorf("caution: got %v, want 0.4", p.Caution)
	}
	// Other fields unchanged
	if p.Verbosity != 0.5 {
		t.Errorf("verbosity: got %v, want 0.5", p.Verbosity)
	}
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	// State file must exist
	if _, err := os.Stat(filepath.Join(dir, stateFileName)); err != nil {
		t.Errorf("state file missing: %v", err)
	}
}

func TestPersonaAdjust(t *testing.T) {
	cases := []struct {
		name    string
		start   float64
		delta   float64
		param   string
		want    float64
	}{
		{"clamp high curiosity", 0.9, 0.5, "curiosity", 1.0},
		{"clamp low verbosity", 0.1, -0.5, "verbosity", 0.0},
		{"normal persistence", 0.5, 0.2, "persistence", 0.7},
		{"unknown param no-op", 0.5, 0.5, "unknown", 0.5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := DefaultPersona()
			// Override the field under test to the starting value
			switch tc.param {
			case "curiosity":
				p.Curiosity = tc.start
			case "verbosity":
				p.Verbosity = tc.start
			case "persistence":
				p.Persistence = tc.start
			}

			lessons := []Lesson{{Param: tc.param, Delta: tc.delta}}
			next := p.Adjust(lessons)

			// Verify immutability: original must be unchanged for named fields
			switch tc.param {
			case "curiosity":
				if p.Curiosity != tc.start {
					t.Errorf("original mutated: got %v, want %v", p.Curiosity, tc.start)
				}
				if next.Curiosity != tc.want {
					t.Errorf("result: got %v, want %v", next.Curiosity, tc.want)
				}
			case "verbosity":
				if p.Verbosity != tc.start {
					t.Errorf("original mutated: got %v, want %v", p.Verbosity, tc.start)
				}
				if next.Verbosity != tc.want {
					t.Errorf("result: got %v, want %v", next.Verbosity, tc.want)
				}
			case "persistence":
				if p.Persistence != tc.start {
					t.Errorf("original mutated: got %v, want %v", p.Persistence, tc.start)
				}
				if next.Persistence != tc.want {
					t.Errorf("result: got %v, want %v", next.Persistence, tc.want)
				}
			case "unknown":
				// All fields should remain at default 0.5
				if next.Curiosity != 0.5 || next.Verbosity != 0.5 || next.Caution != 0.5 ||
					next.Creativity != 0.5 || next.Persistence != 0.5 {
					t.Errorf("unknown param changed fields: %+v", next)
				}
			}
		})
	}
}
