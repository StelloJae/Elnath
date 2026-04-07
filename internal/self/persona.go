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

func clamp(v float64) float64 {
	return math.Max(0.0, math.Min(1.0, v))
}
