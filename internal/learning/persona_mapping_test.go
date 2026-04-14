package learning

import "testing"

func TestPersonaDeltaFromHint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		direction string
		magnitude string
		want      float64
	}{
		{name: "increase small", direction: "increase", magnitude: "small", want: 0.01},
		{name: "increase medium", direction: "increase", magnitude: "medium", want: 0.03},
		{name: "increase large", direction: "increase", magnitude: "large", want: 0.06},
		{name: "decrease small", direction: "decrease", magnitude: "small", want: -0.01},
		{name: "decrease medium", direction: "decrease", magnitude: "medium", want: -0.03},
		{name: "decrease large", direction: "decrease", magnitude: "large", want: -0.06},
		{name: "neutral small", direction: "neutral", magnitude: "small", want: 0},
		{name: "neutral medium", direction: "neutral", magnitude: "medium", want: 0},
		{name: "neutral large", direction: "neutral", magnitude: "large", want: 0},
		{name: "unknown direction", direction: "sideways", magnitude: "medium", want: 0},
		{name: "unknown magnitude", direction: "increase", magnitude: "huge", want: 0},
		{name: "empty values", direction: "", magnitude: "", want: 0},
		{name: "case insensitive", direction: "DECREASE", magnitude: "Large", want: -0.06},
		{name: "trim spaces", direction: " increase ", magnitude: " medium ", want: 0.03},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PersonaDeltaFromHint(tt.direction, tt.magnitude); got != tt.want {
				t.Fatalf("PersonaDeltaFromHint(%q, %q) = %v, want %v", tt.direction, tt.magnitude, got, tt.want)
			}
		})
	}
}
