package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/wiki"
)

type WikiWriter interface {
	Upsert(*wiki.Page) error
}

type WikiWriteRequest struct {
	TaskID            int64
	ReceiptID         int64
	VerificationRunID int64
	Source            string
	PayloadHash       string
	Redact            func(string) string
}

func (g *Gate) UpsertWikiPage(ctx context.Context, req WikiWriteRequest, writer WikiWriter, page *wiki.Page) (bool, *agentic.MemoryUpdate, error) {
	if writer == nil {
		return false, nil, errors.New("memory gate: nil wiki writer")
	}
	if page == nil {
		return false, nil, errors.New("memory gate: nil wiki page")
	}
	payloadHash := req.PayloadHash
	if payloadHash == "" {
		hash, err := hashWikiPage(page, req.Redact)
		if err != nil {
			return false, nil, err
		}
		payloadHash = hash
	}
	decision, err := g.Decide(ctx, Request{
		TaskID:            req.TaskID,
		ReceiptID:         req.ReceiptID,
		VerificationRunID: req.VerificationRunID,
		Target:            TargetWikiPage,
		Operation:         OperationUpsert,
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

	if err := writer.Upsert(page); err != nil {
		failed, markErr := g.MarkFailed(ctx, decision.Update.ID, err.Error())
		if markErr != nil {
			return false, decision.Update, fmt.Errorf("%w; memory gate: mark failed: %v", err, markErr)
		}
		return false, failed, err
	}
	applied, err := g.MarkApplied(ctx, decision.Update.ID, decision.Update.Reason)
	if err != nil {
		return true, decision.Update, err
	}
	return true, applied, nil
}

func hashWikiPage(page *wiki.Page, redactor func(string) string) (string, error) {
	if page == nil {
		return "", errors.New("memory gate: nil wiki page")
	}
	stable := *page
	stable.Created = time.Time{}
	stable.Updated = time.Time{}
	if redactor != nil {
		stable.Path = redactor(stable.Path)
		stable.Title = redactor(stable.Title)
		stable.Content = redactor(stable.Content)
		stable.TTL = redactor(stable.TTL)
		stable.Confidence = redactor(stable.Confidence)
		for i := range stable.Tags {
			stable.Tags[i] = redactor(stable.Tags[i])
		}
		if stable.Extra != nil {
			extra := make(map[string]any, len(stable.Extra))
			for k, v := range stable.Extra {
				ks := redactor(k)
				if s, ok := v.(string); ok {
					extra[ks] = redactor(s)
				} else {
					extra[ks] = v
				}
			}
			stable.Extra = extra
		}
	}
	b, err := json.Marshal(stable)
	if err != nil {
		return "", fmt.Errorf("memory gate: hash wiki page: %w", err)
	}
	return hashBytes(b), nil
}
