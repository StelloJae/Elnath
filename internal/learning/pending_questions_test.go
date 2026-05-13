package learning

import (
	"testing"
	"time"
)

func TestPendingUserQuestionsListsUnansweredLatestFirst(t *testing.T) {
	first := time.Date(2026, 5, 13, 7, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	third := second.Add(time.Minute)
	records := []OutcomeRecord{
		{
			Timestamp: first,
			ControlToolReceipts: []ControlToolReceipt{{
				Tool:           "ask_user_question",
				Action:         "request",
				RequestID:      "req-1",
				SessionID:      "sess-1",
				Question:       "Which branch?",
				QuestionChars:  13,
				OptionCount:    2,
				TimeoutSeconds: 120,
			}},
		},
		{
			Timestamp: second,
			ControlToolReceipts: []ControlToolReceipt{{
				Tool:      "ask_user_question",
				Action:    "request",
				RequestID: "req-2",
				SessionID: "sess-1",
				Question:  "Run tests?",
			}},
		},
		{
			Timestamp: third,
			ControlToolReceipts: []ControlToolReceipt{{
				Tool:      "user_question_answer",
				Action:    "answer",
				RequestID: "req-1",
				SessionID: "sess-1",
			}},
		},
	}

	pending := PendingUserQuestions(records, "sess-1", 10)
	if len(pending) != 1 {
		t.Fatalf("pending count = %d, want 1: %+v", len(pending), pending)
	}
	if got := pending[0]; got.RequestID != "req-2" || got.SessionID != "sess-1" || got.Question != "Run tests?" {
		t.Fatalf("pending[0] = %+v, want unanswered req-2", got)
	}
}

func TestPendingUserQuestionsFiltersSessionAndLimit(t *testing.T) {
	now := time.Date(2026, 5, 13, 7, 0, 0, 0, time.UTC)
	records := []OutcomeRecord{{
		Timestamp: now,
		ControlToolReceipts: []ControlToolReceipt{{
			Tool:      "ask_user_question",
			Action:    "request",
			RequestID: "req-1",
			SessionID: "sess-1",
			Question:  "First?",
		}, {
			Tool:      "ask_user_question",
			Action:    "request",
			RequestID: "req-2",
			SessionID: "sess-2",
			Question:  "Second?",
		}},
	}}

	pending := PendingUserQuestions(records, "sess-2", 1)
	if len(pending) != 1 || pending[0].RequestID != "req-2" {
		t.Fatalf("pending = %+v, want only sess-2 req-2", pending)
	}
}
