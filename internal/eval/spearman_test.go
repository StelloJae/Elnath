package eval

import (
	"math"
	"testing"
)

func TestSpearmanRank(t *testing.T) {
	cases := []struct {
		name           string
		values         []float64
		wantCoeff      float64
		wantIsConstant bool
		// epsilon for floating-point tolerance.
		epsilon float64
	}{
		{
			name:      "perfect monotonic increase",
			values:    []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
			wantCoeff: 1.0,
			epsilon:   1e-9,
		},
		{
			name:      "perfect inverse",
			values:    []float64{1.0, 0.9, 0.8, 0.7, 0.6, 0.5, 0.4, 0.3, 0.2, 0.1},
			wantCoeff: -1.0,
			epsilon:   1e-9,
		},
		{
			name:           "constant input flagged",
			values:         []float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5},
			wantCoeff:      0.0,
			wantIsConstant: true,
		},
		{
			name:      "n=2 below minimum",
			values:    []float64{0.1, 0.9},
			wantCoeff: 0.0,
			// not constant, just too few samples; isConstant=false
		},
		{
			name:      "empty input",
			values:    []float64{},
			wantCoeff: 0.0,
		},
		{
			// Hand-calculated: ranks are (1, 2, 3, 4, 5) for values vs
			// indices (1, 2, 3, 4, 5). Both are identical sequences, so
			// Spearman == 1.0.
			name:      "n=5 monotonic small sample",
			values:    []float64{2.0, 3.5, 4.1, 5.0, 7.2},
			wantCoeff: 1.0,
			epsilon:   1e-9,
		},
		{
			// Tie handling: values = (1.0, 2.0, 2.0, 3.0) → average ranks
			// (1.0, 2.5, 2.5, 4.0). Index ranks = (1,2,3,4). Spearman on
			// non-identical rank sequences: classic formula applies.
			name:      "ties use average rank",
			values:    []float64{1.0, 2.0, 2.0, 3.0},
			wantCoeff: 0.9486832980505138,
			epsilon:   1e-9,
		},
		{
			// Known positive correlation with one inversion at index 3/4.
			// Value ranks at positions 1..6 = (1,2,4,3,5,6); idx ranks
			// = (1..6); Σd² = 2, n=6 → ρ = 1 - 6·2 / (6·35) = 0.94286.
			name:      "one inversion",
			values:    []float64{0.1, 0.2, 0.5, 0.3, 0.6, 0.8},
			wantCoeff: 0.9428571428571428,
			epsilon:   1e-9,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			coeff, isConstant := SpearmanRank(tc.values)
			if isConstant != tc.wantIsConstant {
				t.Errorf("isConstant = %v, want %v", isConstant, tc.wantIsConstant)
			}
			if tc.epsilon > 0 {
				if math.Abs(coeff-tc.wantCoeff) > tc.epsilon {
					t.Errorf("coeff = %v, want %v (±%g)", coeff, tc.wantCoeff, tc.epsilon)
				}
			} else if coeff != tc.wantCoeff {
				t.Errorf("coeff = %v, want %v", coeff, tc.wantCoeff)
			}
		})
	}
}
