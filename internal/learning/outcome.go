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
}

func IsSuccessful(finishReason string) bool {
	return finishReason == "stop"
}

func ShouldRecord(finishReason string) bool {
	return finishReason != ""
}

func deriveOutcomeID(projectID, intent, workflow string, ts time.Time) string {
	sum := sha256.Sum256([]byte(projectID + intent + workflow + ts.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])[:8]
}
