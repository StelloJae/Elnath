package eval

import (
	"reflect"
	"testing"
)

func TestRegressionDetectorNilBaseline(t *testing.T) {
	if got := NewRegressionDetector(nil).Detect(&TestResults{}); got != nil {
		t.Fatalf("Detect() = %#v, want nil", got)
	}
}

func TestRegressionDetectorEmptyBaseline(t *testing.T) {
	detector := NewRegressionDetector(&TestBaseline{PassedTests: []string{}})
	if got := detector.Detect(&TestResults{}); got != nil {
		t.Fatalf("Detect() = %#v, want nil", got)
	}
}

func TestRegressionDetectorNoRegression(t *testing.T) {
	detector := NewRegressionDetector(&TestBaseline{PassedTests: []string{"a", "b"}})
	got := detector.Detect(&TestResults{PassedTests: []string{"a", "b"}})
	if len(got) != 0 {
		t.Fatalf("Detect() = %#v, want empty slice", got)
	}
}

func TestRegressionDetectorAllRegressed(t *testing.T) {
	detector := NewRegressionDetector(&TestBaseline{PassedTests: []string{"a", "b"}})
	got := detector.Detect(&TestResults{FailedTests: []string{"a", "b"}})
	want := []Regression{
		{TestID: "a", Baseline: "pass", After: "fail"},
		{TestID: "b", Baseline: "pass", After: "fail"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Detect() = %#v, want %#v", got, want)
	}
}

func TestRegressionDetectorMissingAfter(t *testing.T) {
	detector := NewRegressionDetector(&TestBaseline{PassedTests: []string{"a"}})
	got := detector.Detect(&TestResults{PassedTests: []string{"b"}, FailedTests: []string{"c"}})
	want := []Regression{{TestID: "a", Baseline: "pass", After: "missing"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Detect() = %#v, want %#v", got, want)
	}
}

func TestRegressionDetectorPartialRegression(t *testing.T) {
	detector := NewRegressionDetector(&TestBaseline{PassedTests: []string{"a", "b", "c"}})
	got := detector.Detect(&TestResults{PassedTests: []string{"a", "c"}, FailedTests: []string{"b"}})
	want := []Regression{{TestID: "b", Baseline: "pass", After: "fail"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Detect() = %#v, want %#v", got, want)
	}
}

func TestRegressionDetectorNilAfter(t *testing.T) {
	detector := NewRegressionDetector(&TestBaseline{PassedTests: []string{"a"}})
	if got := detector.Detect(nil); got != nil {
		t.Fatalf("Detect() = %#v, want nil", got)
	}
}
