package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/learning"
)

type LearningWriter interface {
	AppendNew(learning.Lesson) (bool, error)
}

type LearningWriteRequest struct {
	TaskID            int64
	ReceiptID         int64
	VerificationRunID int64
	Source            string
	PayloadHash       string
	Redact            func(string) string
}

func AppendLegacyLearningLesson(writer LearningWriter, lesson learning.Lesson) (bool, error) {
	if writer == nil {
		return false, nil
	}
	return writer.AppendNew(lesson)
}

func (g *Gate) AppendLearningLesson(ctx context.Context, req LearningWriteRequest, writer LearningWriter, lesson learning.Lesson) (bool, *agentic.MemoryUpdate, error) {
	if writer == nil {
		return false, nil, errors.New("memory gate: nil learning writer")
	}
	payloadHash := req.PayloadHash
	if payloadHash == "" {
		hash, err := hashLesson(lesson, req.Redact)
		if err != nil {
			return false, nil, err
		}
		payloadHash = hash
	}
	decision, err := g.Decide(ctx, Request{
		TaskID:            req.TaskID,
		ReceiptID:         req.ReceiptID,
		VerificationRunID: req.VerificationRunID,
		Target:            TargetLearningLesson,
		Operation:         OperationAppend,
		PayloadHash:       payloadHash,
		Source:            req.Source,
	})
	if err != nil {
		return false, nil, err
	}
	if !decision.Allowed {
		return false, decision.Update, nil
	}
	if decision.Update.Status == agentic.MemoryUpdateStatusApplied {
		return false, decision.Update, nil
	}

	added, err := writer.AppendNew(lesson)
	if err != nil {
		failed, markErr := g.MarkFailed(ctx, decision.Update.ID, err.Error())
		if markErr != nil {
			return false, decision.Update, fmt.Errorf("%w; memory gate: mark failed: %v", err, markErr)
		}
		return false, failed, err
	}
	applied, err := g.MarkApplied(ctx, decision.Update.ID, decision.Update.Reason)
	if err != nil {
		return added, decision.Update, err
	}
	return added, applied, nil
}

func hashLesson(lesson learning.Lesson, redactor func(string) string) (string, error) {
	if redactor != nil {
		lesson.Text = redactor(lesson.Text)
		lesson.Topic = redactor(lesson.Topic)
		lesson.Source = redactor(lesson.Source)
		lesson.Rationale = redactor(lesson.Rationale)
		for i := range lesson.Evidence {
			lesson.Evidence[i] = redactor(lesson.Evidence[i])
		}
	}
	lesson.Created = time.Time{}
	b, err := json.Marshal(lesson)
	if err != nil {
		return "", fmt.Errorf("memory gate: hash lesson: %w", err)
	}
	return hashBytes(b), nil
}
