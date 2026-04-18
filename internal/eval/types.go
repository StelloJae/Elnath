package eval

// Track is the benchmark scenario category.
type Track string

const (
	TrackBrownfieldFeature Track = "brownfield_feature"
	TrackBugfix            Track = "bugfix"
	TrackGreenfield        Track = "greenfield"
	TrackResearch          Track = "research"
)

// Language is the primary language for a benchmark task.
type Language string

const (
	LanguageGo         Language = "go"
	LanguageTypeScript Language = "typescript"
)

// Task is a single benchmark task in the public corpus.
type Task struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Track              Track    `json:"track"`
	Language           Language `json:"language"`
	RepoClass          string   `json:"repo_class,omitempty"`
	BenchmarkFamily    string   `json:"benchmark_family,omitempty"`
	Holdout            bool     `json:"holdout,omitempty"`
	Prompt             string   `json:"prompt"`
	Repo               string   `json:"repo,omitempty"`
	RepoRef            string   `json:"repo_ref,omitempty"`
	SourceURL          string   `json:"source_url,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	// Intent is the user-intent category for v2 self-improvement benchmarks
	// (e.g. "question", "complex_task", "bugfix"). Used by the v2 harness to
	// aggregate outcomes by intent for advisor preference learning.
	Intent string `json:"intent,omitempty"`
	// ExpectedWorkflow is the workflow that stub execution records as the
	// successful workflow for this task's outcome. v2-only.
	ExpectedWorkflow string `json:"expected_workflow,omitempty"`
}

// Corpus is a versioned task list.
type Corpus struct {
	Version string `json:"version"`
	Tasks   []Task `json:"tasks"`
	// IntentDistribution declares the target mix of intents in v2 corpora
	// (e.g. {"question": 0.45, "complex_task": 0.35, "bugfix": 0.20}).
	// The same distribution must apply to both TrainingSet and HeldOutSet so
	// held-out hit-rate measurement reflects in-distribution generalization.
	IntentDistribution map[string]float64 `json:"intent_distribution,omitempty"`
	// TrainingSet is the list of Task IDs the advisor learns from. v2-only.
	TrainingSet []string `json:"training_set,omitempty"`
	// HeldOutSet is the disjoint list of Task IDs used to measure advisor
	// hit rate without feeding outcomes back into the scratch store. v2-only.
	HeldOutSet []string `json:"held_out_set,omitempty"`
}

// RunResult is the outcome of one benchmark task execution.
type RunResult struct {
	Run                 int      `json:"run,omitempty"`
	TaskID              string   `json:"task_id"`
	Track               Track    `json:"track"`
	Language            Language `json:"language"`
	Success             bool     `json:"success"`
	InterventionCount   int      `json:"intervention_count"`
	InterventionNeeded  bool     `json:"intervention_needed"`
	InterventionClass   string   `json:"intervention_class,omitempty"`
	VerificationCommand string   `json:"verification_command,omitempty"`
	VerificationPassed  bool     `json:"verification_passed,omitempty"`
	FailureFamily       string   `json:"failure_family,omitempty"`
	RecoveryAttempted   bool     `json:"recovery_attempted,omitempty"`
	RecoverySucceeded   bool     `json:"recovery_succeeded,omitempty"`
	DurationSeconds     float64  `json:"duration_seconds"`
	Notes               string   `json:"notes,omitempty"`
	RegressionTriggered bool     `json:"regression_triggered,omitempty"`
}

// Scorecard is a versioned result bundle for one evaluated system/baseline.
type Scorecard struct {
	Version           string      `json:"version"`
	System            string      `json:"system"`
	Baseline          string      `json:"baseline,omitempty"`
	Context           string      `json:"context,omitempty"`
	RuntimePolicy     string      `json:"runtime_policy"`
	RepeatedRuns      int         `json:"repeated_runs,omitempty"`
	InterventionNotes bool        `json:"intervention_notes,omitempty"`
	Results           []RunResult `json:"results"`
}

// TrackSummary is the aggregate result for one track.
type TrackSummary struct {
	Total                  int
	Successes              int
	SuccessRate            float64
	SuccessAndVerifiedRate float64
	Interventions          int
	InterventionRate       float64
	InterventionMean       float64
	VerificationPasses     int
	VerificationPassRate   float64
	RecoveryAttempts       int
	RecoverySuccesses      int
	RecoverySuccessRate    float64
	FailureFamilies        map[string]int
	RegressionsTriggered   int
	RegressionRate         float64
	SuccessDurationMean    float64
}

// Summary is the aggregate result for a whole scorecard.
type Summary struct {
	Total                  int
	Successes              int
	SuccessRate            float64
	SuccessAndVerifiedRate float64
	Interventions          int
	InterventionRate       float64
	InterventionMean       float64
	VerificationPasses     int
	VerificationPassRate   float64
	RecoveryAttempts       int
	RecoverySuccesses      int
	RecoverySuccessRate    float64
	FailureFamilies        map[string]int
	ByTrack                map[Track]TrackSummary
	RegressionsTriggered   int
	RegressionRate         float64
	SuccessDurationMean    float64
}

// DiffSummary compares two scorecards with the same task universe shape.
// RegressionRateDelta sign convention: positive means current has MORE
// regressions than baseline (i.e. worse). Opposite polarity from SuccessRateDelta
// where positive means better.
type DiffSummary struct {
	Current                     Summary
	Baseline                    Summary
	SuccessRateDelta            float64
	SuccessAndVerifiedRateDelta float64
	VerificationPassDelta       float64
	RecoverySuccessDelta        float64
	InterventionMeanDelta       float64
	SuccessDurationMeanDelta    float64
	ByTrack                     map[Track]TrackDelta
	RegressionRateDelta         float64
}

// TrackDelta compares one track between scorecards.
type TrackDelta struct {
	Current                     TrackSummary
	Baseline                    TrackSummary
	SuccessRateDelta            float64
	SuccessAndVerifiedRateDelta float64
	VerificationPassDelta       float64
	RecoverySuccessDelta        float64
	InterventionMeanDelta       float64
	SuccessDurationMeanDelta    float64
	RegressionRateDelta         float64
}

// BaselineRunPlan is a starter scaffold for evaluating an external baseline.
type BaselineRunPlan struct {
	Version           string   `json:"version"`
	System            string   `json:"system,omitempty"`
	Baseline          string   `json:"baseline"`
	CorpusPath        string   `json:"corpus_path"`
	CommandTemplate   string   `json:"command_template"`
	OutputPath        string   `json:"output_path"`
	Context           string   `json:"context,omitempty"`
	RuntimePolicy     string   `json:"runtime_policy"`
	RepeatedRuns      int      `json:"repeated_runs,omitempty"`
	InterventionNotes bool     `json:"intervention_notes,omitempty"`
	RequiredEnv       []string `json:"required_env,omitempty"`
	Notes             []string `json:"notes,omitempty"`
}

// RuleViolation represents one anti-vanity rule failure.
type RuleViolation struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}
