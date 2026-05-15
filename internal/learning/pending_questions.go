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
	Options        []string  `json:"options,omitempty"`
	OptionCount    int       `json:"option_count,omitempty"`
	AllowFreeText  bool      `json:"allow_free_text,omitempty"`
	TimeoutSeconds int       `json:"timeout_seconds,omitempty"`
	AskedAt        time.Time `json:"asked_at"`
	Answerable     bool      `json:"answerable"`
	AnswerCommand  string    `json:"answer_command,omitempty"`
	PendingCommand string    `json:"pending_command,omitempty"`
}

func PendingUserQuestions(records []OutcomeRecord, sessionID string, limit int) []PendingUserQuestion {
	return PendingUserQuestionsAt(records, sessionID, limit, time.Now().UTC())
}

func PendingUserQuestionsAt(records []OutcomeRecord, sessionID string, limit int, now time.Time) []PendingUserQuestion {
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
					Options:        cleanPendingQuestionOptions(receipt.Options),
					OptionCount:    receipt.OptionCount,
					AllowFreeText:  receipt.AllowFreeText,
					TimeoutSeconds: receipt.TimeoutSeconds,
					AskedAt:        record.Timestamp,
				}
				pending[requestID] = withUserQuestionHandoff(pending[requestID])
			case receipt.Tool == "user_question_answer" && receipt.Action == "answer":
				delete(pending, requestID)
			case receipt.Tool == UserQuestionCancelToolName && receipt.Action == "cancel":
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
		if userQuestionTimedOut(question.AskedAt, question.TimeoutSeconds, now) {
			continue
		}
		out = append(out, question)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func userQuestionTimedOut(askedAt time.Time, timeoutSeconds int, now time.Time) bool {
	if timeoutSeconds <= 0 || askedAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return !now.Before(askedAt.Add(time.Duration(timeoutSeconds) * time.Second))
}

func cleanPendingQuestionOptions(options []string) []string {
	out := make([]string, 0, len(options))
	seen := make(map[string]struct{}, len(options))
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		if _, ok := seen[option]; ok {
			continue
		}
		seen[option] = struct{}{}
		out = append(out, option)
	}
	return out
}

func FindPendingUserQuestion(records []OutcomeRecord, sessionID, requestID string) (PendingUserQuestion, bool) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return PendingUserQuestion{}, false
	}
	for _, question := range PendingUserQuestions(records, sessionID, 0) {
		if question.RequestID == requestID {
			return question, true
		}
	}
	return PendingUserQuestion{}, false
}

func withUserQuestionHandoff(question PendingUserQuestion) PendingUserQuestion {
	if strings.TrimSpace(question.SessionID) == "" || strings.TrimSpace(question.RequestID) == "" {
		return question
	}
	question.Answerable = true
	question.AnswerCommand = UserQuestionAnswerCommand(question.SessionID, question.RequestID)
	question.PendingCommand = PendingUserQuestionsCommand(question.SessionID)
	return question
}

func UserQuestionAnswerCommand(sessionID, requestID string) string {
	sessionID = strings.TrimSpace(sessionID)
	requestID = strings.TrimSpace(requestID)
	if sessionID == "" || requestID == "" {
		return ""
	}
	return "elnath task answer --session " + shellQuoteUserQuestionArg(sessionID) + " --request " + shellQuoteUserQuestionArg(requestID) + " --answer 'ANSWER_TEXT'"
}

func PendingUserQuestionsCommand(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	return "elnath explain pending-questions --session " + shellQuoteUserQuestionArg(sessionID)
}

func shellQuoteUserQuestionArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
