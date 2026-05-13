package learning

import (
	"sort"
	"strings"
	"time"
)

type PendingUserQuestion struct {
	RequestID      string    `json:"request_id"`
	SessionID      string    `json:"session_id,omitempty"`
	Question       string    `json:"question,omitempty"`
	QuestionChars  int       `json:"question_chars,omitempty"`
	OptionCount    int       `json:"option_count,omitempty"`
	TimeoutSeconds int       `json:"timeout_seconds,omitempty"`
	AskedAt        time.Time `json:"asked_at"`
}

func PendingUserQuestions(records []OutcomeRecord, sessionID string, limit int) []PendingUserQuestion {
	sessionID = strings.TrimSpace(sessionID)
	ordered := append([]OutcomeRecord(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Timestamp.Before(ordered[j].Timestamp)
	})

	pending := make(map[string]PendingUserQuestion)
	var order []string
	for _, record := range ordered {
		for _, receipt := range record.ControlToolReceipts {
			requestID := strings.TrimSpace(receipt.RequestID)
			if requestID == "" {
				continue
			}
			receiptSessionID := strings.TrimSpace(receipt.SessionID)
			if sessionID != "" && receiptSessionID != sessionID {
				continue
			}
			switch {
			case receipt.Tool == "ask_user_question" && receipt.Action == "request":
				if _, exists := pending[requestID]; !exists {
					order = append(order, requestID)
				}
				pending[requestID] = PendingUserQuestion{
					RequestID:      requestID,
					SessionID:      receiptSessionID,
					Question:       strings.TrimSpace(receipt.Question),
					QuestionChars:  receipt.QuestionChars,
					OptionCount:    receipt.OptionCount,
					TimeoutSeconds: receipt.TimeoutSeconds,
					AskedAt:        record.Timestamp,
				}
			case receipt.Tool == "user_question_answer" && receipt.Action == "answer":
				delete(pending, requestID)
			}
		}
	}

	out := make([]PendingUserQuestion, 0, len(pending))
	for i := len(order) - 1; i >= 0; i-- {
		requestID := order[i]
		question, ok := pending[requestID]
		if !ok {
			continue
		}
		out = append(out, question)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}
