package eval

import (
	"math"
	"sort"
)

// SpearmanRank computes the Spearman rank correlation between the supplied
// values and their own position index (i.e. "does the sequence trend up as
// the index grows?"). This is the shape of the question Phase 7.3 asks:
// given held-out hit rate over N benchmark runs, does it monotonically
// improve?
//
// Return contract:
//   - (0.0, true)  — all values are identical ("constant input"). The
//     classic formula is undefined here (division by zero); callers must
//     distinguish "no trend because flat" from "no trend because noisy".
//   - (0.0, false) — n < 3. Any correlation is statistically meaningless;
//     caller should require a minimum run count before trusting the verdict.
//   - otherwise    — the coefficient in [-1.0, 1.0]. Ties are handled via
//     average-rank assignment.
func SpearmanRank(values []float64) (float64, bool) {
	n := len(values)
	if n < 3 {
		return 0.0, false
	}
	if allEqual(values) {
		return 0.0, true
	}

	ranks := averageRanks(values)
	// Position index ranks are just 1..n (strictly monotonic, no ties).
	// Compute mean of both sequences to plug into the Pearson-on-ranks
	// definition (which accommodates ties where tied-rank formulas fail).
	var sumRanks float64
	for _, r := range ranks {
		sumRanks += r
	}
	meanRanks := sumRanks / float64(n)
	meanIdx := float64(n+1) / 2.0

	var numerator, denomValues, denomIdx float64
	for i, r := range ranks {
		idx := float64(i + 1)
		dv := r - meanRanks
		di := idx - meanIdx
		numerator += dv * di
		denomValues += dv * dv
		denomIdx += di * di
	}
	denom := sqrtNonNegative(denomValues) * sqrtNonNegative(denomIdx)
	if denom == 0 {
		// denomValues is 0 iff all ranks collapse — should not hit because
		// allEqual is checked above, but keep the guard for safety.
		return 0.0, true
	}
	return numerator / denom, false
}

func allEqual(values []float64) bool {
	first := values[0]
	for _, v := range values[1:] {
		if v != first {
			return false
		}
	}
	return true
}

// averageRanks returns the Spearman-style average ranks for the values.
// Ties share the average of the positions they occupy (e.g. values
// [1, 2, 2, 3] → ranks [1.0, 2.5, 2.5, 4.0]).
func averageRanks(values []float64) []float64 {
	n := len(values)
	indexed := make([]struct {
		value float64
		index int
	}, n)
	for i, v := range values {
		indexed[i].value = v
		indexed[i].index = i
	}
	sort.SliceStable(indexed, func(i, j int) bool {
		return indexed[i].value < indexed[j].value
	})
	ranks := make([]float64, n)
	i := 0
	for i < n {
		j := i + 1
		for j < n && indexed[j].value == indexed[i].value {
			j++
		}
		// positions [i+1 .. j] (1-indexed), average = (i+1 + j) / 2.
		avg := float64(i+1+j) / 2.0
		for k := i; k < j; k++ {
			ranks[indexed[k].index] = avg
		}
		i = j
	}
	return ranks
}

func sqrtNonNegative(x float64) float64 {
	if x <= 0 {
		return 0
	}
	return math.Sqrt(x)
}
