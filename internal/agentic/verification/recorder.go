package verification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/secret"
)

var (
	ErrMissingTaskID  = errors.New("verification: missing task_id")
	ErrInvalidVerdict = errors.New("verification: invalid verdict")
)

const MaxReasonBytes = 2048

type Recorder struct {
	store *agentic.Store
}

type RunRequest struct {
	TaskID           int64
	VerifierActorID  int64
	CriteriaJSON     string
	EvidenceRefsJSON string
	Verdict          string
	Reason           string
	CreatedAt        time.Time
}

func NewRecorder(store *agentic.Store) *Recorder {
	return &Recorder{store: store}
}

func (r *Recorder) Record(ctx context.Context, req RunRequest) (*agentic.VerificationRun, error) {
	return r.RecordVerificationRun(ctx, agentic.VerificationRun{
		TaskID:           req.TaskID,
		VerifierActorID:  req.VerifierActorID,
		CriteriaJSON:     req.CriteriaJSON,
		EvidenceRefsJSON: req.EvidenceRefsJSON,
		Verdict:          req.Verdict,
		Reason:           req.Reason,
		CreatedAt:        req.CreatedAt,
	})
}

func (r *Recorder) RecordVerificationRun(ctx context.Context, run agentic.VerificationRun) (*agentic.VerificationRun, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("verification: nil recorder store")
	}
	if run.TaskID == 0 {
		return nil, ErrMissingTaskID
	}
	if !validVerdict(run.Verdict) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidVerdict, run.Verdict)
	}
	if !json.Valid([]byte(run.CriteriaJSON)) {
		return nil, errors.New("verification: criteria_json must be valid JSON")
	}
	if !json.Valid([]byte(run.EvidenceRefsJSON)) {
		return nil, errors.New("verification: evidence_refs_json must be valid JSON")
	}
	run.Reason = sanitizeReason(run.Reason)
	return r.store.CreateVerificationRun(ctx, run)
}

func validVerdict(verdict string) bool {
	switch verdict {
	case agentic.VerificationVerdictPassed, agentic.VerificationVerdictFailed, agentic.VerificationVerdictInconclusive:
		return true
	default:
		return false
	}
}

func sanitizeReason(reason string) string {
	reason = strings.TrimSpace(secret.NewDetector().RedactString(reason))
	if len(reason) <= MaxReasonBytes {
		return reason
	}
	return reason[:MaxReasonBytes]
}
