package learning

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

type OutcomeRecord struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	Intent         string    `json:"intent"`
	Workflow       string    `json:"workflow"`
	FinishReason   string    `json:"finish_reason"`
	Success        bool      `json:"success"`
	Duration       float64   `json:"duration_s"`
	Cost           float64   `json:"cost"`
	Iterations     int       `json:"iterations"`
	InputSnippet   string    `json:"input_snippet,omitempty"`
	EstimatedFiles int       `json:"estimated_files,omitempty"`
	ExistingCode   bool      `json:"existing_code,omitempty"`
	PreferenceUsed bool      `json:"preference_used,omitempty"`
	Timestamp      time.Time `json:"timestamp"`

	// FU-LearningObservability schema extension (2026-04-20). All fields are
	// omitempty so older scorecard code and legacy records continue to read
	// cleanly; the daemon self-analysis lens relies on them being populated
	// going forward.
	MaxIterations int             `json:"max_iterations,omitempty"`
	InputTokens   int             `json:"input_tokens,omitempty"`
	OutputTokens  int             `json:"output_tokens,omitempty"`
	ToolStats     []AgentToolStat `json:"tool_stats,omitempty"`
	SessionID     string          `json:"session_id,omitempty"`

	// Completion observability is intentionally advisory. These fields let the
	// runtime record verification/completion gaps before any blocking retry
	// policy is introduced.
	VerificationHint     bool   `json:"verification_hint,omitempty"`
	VerificationObserved *bool  `json:"verification_observed,omitempty"`
	CompletionWarning    string `json:"completion_warning,omitempty"`
	ReasoningEffort      string `json:"reasoning_effort,omitempty"`
	ReasoningEffortMode  string `json:"reasoning_effort_mode,omitempty"`
	ProviderName         string `json:"provider_name,omitempty"`
	ProviderEffort       string `json:"provider_effort,omitempty"`
	ProviderEffortNote   string `json:"provider_effort_note,omitempty"`
	RetryDecision        string `json:"retry_decision,omitempty"`
	RetryReason          string `json:"retry_reason,omitempty"`
}

// IsSuccessful returns true for workflow outcomes that count as completion in
// the learning store. Ralph's "unverified_inline" is included per Phase 8.1a
// Fix 2 + partner M3 decision: inline-artifact answers (guard-gated) are
// honest non-verification completions, not failures. Recording them as
// failures would train the router to avoid ralph for future inline tasks.
func IsSuccessful(finishReason string) bool {
	switch finishReason {
	case "stop", "partial_success", "unverified_inline":
		return true
	default:
		return false
	}
}

func ShouldRecord(finishReason string) bool {
	return finishReason != ""
}

func deriveOutcomeID(projectID, intent, workflow string, ts time.Time) string {
	sum := sha256.Sum256([]byte(projectID + intent + workflow + ts.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])[:8]
}
