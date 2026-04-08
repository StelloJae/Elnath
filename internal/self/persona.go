package self

import "math"

// Persona holds tunable personality parameters, each in [0.0, 1.0].
type Persona struct {
	Curiosity   float64 `json:"curiosity"`
	Verbosity   float64 `json:"verbosity"`
	Caution     float64 `json:"caution"`
	Creativity  float64 `json:"creativity"`
	Persistence float64 `json:"persistence"`
}

// DefaultPersona returns balanced defaults (all 0.5).
func DefaultPersona() Persona {
	return Persona{
		Curiosity:   0.5,
		Verbosity:   0.5,
		Caution:     0.5,
		Creativity:  0.5,
		Persistence: 0.5,
	}
}

// Lesson is a single adjustment signal from experience.
type Lesson struct {
	Param string  // field name: "curiosity", "verbosity", etc.
	Delta float64 // positive = increase, negative = decrease
}

// Adjust applies a slice of lessons to the persona, clamping each field to [0.0, 1.0].
func (p Persona) Adjust(lessons []Lesson) Persona {
	next := p
	for _, l := range lessons {
		switch l.Param {
		case "curiosity":
			next.Curiosity = clamp(next.Curiosity + l.Delta)
		case "verbosity":
			next.Verbosity = clamp(next.Verbosity + l.Delta)
		case "caution":
			next.Caution = clamp(next.Caution + l.Delta)
		case "creativity":
			next.Creativity = clamp(next.Creativity + l.Delta)
		case "persistence":
			next.Persistence = clamp(next.Persistence + l.Delta)
		}
	}
	return next
}

// PresetName identifies a named persona preset.
type PresetName string

const (
	PresetDefault    PresetName = ""
	PresetResearcher PresetName = "researcher"
	PresetCoder      PresetName = "coder"
	PresetWriter     PresetName = "writer"
	PresetReviewer   PresetName = "reviewer"
)

// Preset returns the Persona values and supplementary system prompt for a named preset.
// The second return value is extra text appended to the system prompt.
func Preset(name PresetName) (Persona, string) {
	switch name {
	case PresetResearcher:
		return Persona{
			Curiosity:   0.9,
			Verbosity:   0.6,
			Caution:     0.7,
			Creativity:  0.7,
			Persistence: 0.8,
		}, "Focus on research: generate hypotheses, gather evidence, cite sources, and synthesize findings. Prefer depth over breadth."

	case PresetCoder:
		return Persona{
			Curiosity:   0.4,
			Verbosity:   0.3,
			Caution:     0.5,
			Creativity:  0.5,
			Persistence: 0.9,
		}, "Focus on implementation: write clean, tested code. Be terse. Prefer working solutions over discussion."

	case PresetWriter:
		return Persona{
			Curiosity:   0.6,
			Verbosity:   0.8,
			Caution:     0.3,
			Creativity:  0.9,
			Persistence: 0.6,
		}, "Focus on writing: produce clear, well-structured prose. Use vivid language and strong narrative flow."

	case PresetReviewer:
		return Persona{
			Curiosity:   0.7,
			Verbosity:   0.5,
			Caution:     0.9,
			Creativity:  0.3,
			Persistence: 0.7,
		}, "Focus on review: identify bugs, security issues, and design flaws. Be thorough and critical. Cite specific code locations."

	default:
		return DefaultPersona(), ""
	}
}

// ValidPresets returns the list of valid preset names.
func ValidPresets() []PresetName {
	return []PresetName{PresetResearcher, PresetCoder, PresetWriter, PresetReviewer}
}

func clamp(v float64) float64 {
	return math.Max(0.0, math.Min(1.0, v))
}
