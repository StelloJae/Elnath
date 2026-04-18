package eval

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
