package eval

// Track is the benchmark scenario category.
type Track string

const (
	TrackBrownfieldFeature Track = "brownfield_feature"
	TrackBugfix            Track = "bugfix"
	TrackGreenfield        Track = "greenfield"
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
}

// Corpus is a versioned task list.
type Corpus struct {
	Version string `json:"version"`
	Tasks   []Task `json:"tasks"`
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
	Total                int
	Successes            int
	SuccessRate          float64
	Interventions        int
	InterventionRate     float64
	VerificationPasses   int
	VerificationPassRate float64
	RecoveryAttempts     int
	RecoverySuccesses    int
	RecoverySuccessRate  float64
	FailureFamilies      map[string]int
	RegressionsTriggered int
	RegressionRate       float64
}

// Summary is the aggregate result for a whole scorecard.
type Summary struct {
	Total                int
	Successes            int
	SuccessRate          float64
	Interventions        int
	InterventionRate     float64
	VerificationPasses   int
	VerificationPassRate float64
	RecoveryAttempts     int
	RecoverySuccesses    int
	RecoverySuccessRate  float64
	FailureFamilies      map[string]int
	ByTrack              map[Track]TrackSummary
	RegressionsTriggered int
	RegressionRate       float64
}

// DiffSummary compares two scorecards with the same task universe shape.
type DiffSummary struct {
	Current               Summary
	Baseline              Summary
	SuccessRateDelta      float64
	VerificationPassDelta float64
	RecoverySuccessDelta  float64
	ByTrack               map[Track]TrackDelta
	RegressionRateDelta   float64
}

// TrackDelta compares one track between scorecards.
type TrackDelta struct {
	Current               TrackSummary
	Baseline              TrackSummary
	SuccessRateDelta      float64
	VerificationPassDelta float64
	RecoverySuccessDelta  float64
	RegressionRateDelta   float64
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
