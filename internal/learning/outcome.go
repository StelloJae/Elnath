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
}

func IsSuccessful(finishReason string) bool {
	return finishReason == "stop" || finishReason == "partial_success"
}

func ShouldRecord(finishReason string) bool {
	return finishReason != ""
}

func deriveOutcomeID(projectID, intent, workflow string, ts time.Time) string {
	sum := sha256.Sum256([]byte(projectID + intent + workflow + ts.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])[:8]
}
