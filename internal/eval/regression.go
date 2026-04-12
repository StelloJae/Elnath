package eval

import "time"

// TestBaseline is the set of tests passing before a benchmark task ran.
type TestBaseline struct {
	PassedTests []string  `json:"passed_tests"`
	Timestamp   time.Time `json:"timestamp,omitempty"`
}

// TestResults is the set of test outcomes after a benchmark task ran.
type TestResults struct {
	PassedTests []string  `json:"passed_tests"`
	FailedTests []string  `json:"failed_tests"`
	Timestamp   time.Time `json:"timestamp,omitempty"`
}

// Regression identifies a test that stopped passing after a task ran.
type Regression struct {
	TestID   string `json:"test_id"`
	Baseline string `json:"baseline_status"`
	After    string `json:"after_status"`
}

// RegressionDetector diffs an after snapshot against a baseline.
type RegressionDetector struct {
	baseline *TestBaseline
}

// NewRegressionDetector returns a pure detector over a baseline snapshot.
func NewRegressionDetector(baseline *TestBaseline) *RegressionDetector {
	return &RegressionDetector{baseline: baseline}
}

// Detect returns tests that passed in baseline but no longer pass after.
func (r *RegressionDetector) Detect(after *TestResults) []Regression {
	if r == nil || r.baseline == nil || len(r.baseline.PassedTests) == 0 {
		return nil
	}
	if after == nil {
		return nil
	}

	afterPass := make(map[string]struct{}, len(after.PassedTests))
	for _, testID := range after.PassedTests {
		afterPass[testID] = struct{}{}
	}
	afterFail := make(map[string]struct{}, len(after.FailedTests))
	for _, testID := range after.FailedTests {
		afterFail[testID] = struct{}{}
	}

	var out []Regression
	for _, testID := range r.baseline.PassedTests {
		if _, stillPass := afterPass[testID]; stillPass {
			continue
		}
		afterStatus := "missing"
		if _, failed := afterFail[testID]; failed {
			afterStatus = "fail"
		}
		out = append(out, Regression{TestID: testID, Baseline: "pass", After: afterStatus})
	}
	return out
}
