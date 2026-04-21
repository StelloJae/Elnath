package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stello/elnath/internal/routing"
)

// RealRouter decides which workflow name to use for a given intent when
// the v2 benchmark runs against the real decision path. Production
// adapters wrap the orchestrator's Router; tests inject fakes. Workflow
// is returned as a bare string so this interface does not drag the full
// orchestrator.Workflow tree into the eval package.
//
// Consumed by runV2SingleRun when V2RunOptions.Router is non-nil.
type RealRouter interface {
	DecideWorkflow(intent string, pref *routing.WorkflowPreference) string
}

// WriteV2TimeSeries persists the series as pretty-printed JSON, creating
// parent directories as needed. Intended for the `elnath eval run-v2`
// CLI so external tooling (CI, trend dashboards) can consume the cycle
// result without re-parsing the Markdown report.
func WriteV2TimeSeries(path string, series *V2TimeSeries) error {
	if series == nil {
		return fmt.Errorf("write v2 timeseries: series is nil")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write v2 timeseries: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(series, "", "  ")
	if err != nil {
		return fmt.Errorf("write v2 timeseries: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write v2 timeseries: %w", err)
	}
	return nil
}

// V2BenchmarkProjectID is the fixed synthetic project ID used by every v2
// benchmark run. Every Advise() call and every scratch OutcomeStore write in
// the v2 harness uses this constant so the scratch advisor's ForProject
// filter isolates benchmark data from any production store.
const V2BenchmarkProjectID = "__v2_benchmark__"

// V2 verdicts for the self-improvement trajectory check.
const (
	V2VerdictPass       = "PASS"
	V2VerdictStrongPass = "STRONG_PASS"
	V2VerdictFail       = "FAIL"
)

// V2RunResult captures one run of a v2 benchmark cycle. A cycle contains
// N runs (10 by default); each run executes the training set then measures
// hit rate on the held-out set.
type V2RunResult struct {
	RunIndex       int     `json:"run_index"`
	Timestamp      string  `json:"timestamp"`
	HeldOutHitRate float64 `json:"held_out_hit_rate"`
	OutcomesCount  int     `json:"outcomes_count"`
}

// V2TimeSeries is the aggregated result of all runs in a cycle plus the
// statistical verdict. SpearmanCoeff is the primary metric; First3Avg/
// Last3Avg drive the supporting narrative. Verdict is one of
// V2VerdictPass, V2VerdictStrongPass, V2VerdictFail.
type V2TimeSeries struct {
	Runs          []V2RunResult `json:"runs"`
	SpearmanCoeff float64       `json:"spearman_coeff"`
	IsConstant    bool          `json:"is_constant"`
	First3Avg     float64       `json:"first3_avg"`
	Last3Avg      float64       `json:"last3_avg"`
	Verdict       string        `json:"verdict"`
}
