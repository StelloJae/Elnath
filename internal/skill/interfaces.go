package skill

import (
	"context"

	"github.com/stello/elnath/internal/llm"
)

type CreateParams struct {
	Name           string
	Description    string
	Trigger        string
	RequiredTools  []string
	Model          string
	Prompt         string
	Status         string
	Source         string
	SourceSessions []string
}

type Analyst interface {
	Analyze(ctx context.Context, sessions []SessionTrajectory) ([]SkillPatch, error)
}

type Consolidator interface {
	Run(ctx context.Context) (*ConsolidationResult, error)
}

type SessionTrajectory struct {
	SessionID string
	Messages  []llm.Message
	Success   bool
	Intent    string
}

type SkillPatch struct {
	Action         string
	Params         CreateParams
	Evidence       []string
	Confidence     float64
	PatchRationale string
}

type ConsolidationResult struct {
	Promoted []string
	Merged   []string
	Rejected []string
	Cleaned  []string
}

type NotifyFunc func(ctx context.Context, message string) error
