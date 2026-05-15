package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/stello/elnath/internal/tools"
)

const (
	UserQuestionListToolName   = "user_question_list"
	UserQuestionWaitToolName   = "user_question_wait"
	UserQuestionCancelToolName = "user_question_cancel"

	userQuestionWaitDefaultMS = 30000
	userQuestionWaitMaxMS     = 300000
	userQuestionWaitPollMS    = 25
)

type UserQuestionListTool struct {
	store *OutcomeStore
}

func NewUserQuestionListTool(store *OutcomeStore) *UserQuestionListTool {
	return &UserQuestionListTool{store: store}
}

func (t *UserQuestionListTool) Name() string { return UserQuestionListToolName }

func (t *UserQuestionListTool) Description() string {
	return "List unanswered user-input requests derived from completion outcome receipts"
}

func (t *UserQuestionListTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"session_id": tools.String("Optional session id filter."),
		"limit":      tools.Int("Maximum pending questions to return. Defaults to 20."),
	}, nil)
}

func (t *UserQuestionListTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *UserQuestionListTool) Reversible() bool { return true }

func (t *UserQuestionListTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *UserQuestionListTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *UserQuestionListTool) DeferInitialToolSchema() bool { return true }

type userQuestionListToolInput struct {
	SessionID string `json:"session_id"`
	Limit     int    `json:"limit"`
}

type userQuestionListToolOutput struct {
	Count   int                         `json:"count"`
	Pending []PendingUserQuestion       `json:"pending"`
	Receipt userQuestionListToolReceipt `json:"receipt"`
}

type userQuestionListToolReceipt struct {
	Tool          string `json:"tool"`
	Action        string `json:"action"`
	ReadOnly      bool   `json:"read_only"`
	SessionID     string `json:"session_id,omitempty"`
	Limit         int    `json:"limit"`
	TotalReturned int    `json:"total_returned"`
}

func (t *UserQuestionListTool) Execute(_ context.Context, params json.RawMessage) (*tools.Result, error) {
	if t == nil || t.store == nil {
		return tools.ErrorResult("user_question_list: outcome store unavailable"), nil
	}
	var input userQuestionListToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	limit := normalizeUserQuestionListLimit(input.Limit)
	sessionID := strings.TrimSpace(input.SessionID)
	records, err := t.store.Recent(0)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("user_question_list: %v", err)), nil
	}
	pending := PendingUserQuestions(records, sessionID, limit)
	output := userQuestionListToolOutput{
		Count:   len(pending),
		Pending: pending,
		Receipt: userQuestionListToolReceipt{
			Tool:          UserQuestionListToolName,
			Action:        "list",
			ReadOnly:      true,
			SessionID:     sessionID,
			Limit:         limit,
			TotalReturned: len(pending),
		},
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("user_question_list: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

func normalizeUserQuestionListLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

type UserQuestionWaitTool struct {
	store *OutcomeStore
}

func NewUserQuestionWaitTool(store *OutcomeStore) *UserQuestionWaitTool {
	return &UserQuestionWaitTool{store: store}
}

func (t *UserQuestionWaitTool) Name() string { return UserQuestionWaitToolName }

func (t *UserQuestionWaitTool) Description() string {
	return "Wait briefly for a specific pending user-input request to receive an answer receipt"
}

func (t *UserQuestionWaitTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"session_id": tools.String("Session id from the ask_user_question receipt."),
		"request_id": tools.String("Request id from the ask_user_question receipt."),
		"wait_ms":    tools.Int("Maximum time to wait in milliseconds. Defaults to 30000 and caps at 300000."),
	}, []string{"session_id", "request_id"})
}

func (t *UserQuestionWaitTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *UserQuestionWaitTool) Reversible() bool { return true }

func (t *UserQuestionWaitTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *UserQuestionWaitTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *UserQuestionWaitTool) DeferInitialToolSchema() bool { return true }

type userQuestionWaitToolInput struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
	WaitMS    int    `json:"wait_ms"`
}

type userQuestionWaitToolOutput struct {
	Status        string                      `json:"status"`
	RequestID     string                      `json:"request_id"`
	SessionID     string                      `json:"session_id"`
	QuestionChars int                         `json:"question_chars,omitempty"`
	AnswerChars   int                         `json:"answer_chars,omitempty"`
	TaskID        int64                       `json:"task_id,omitempty"`
	WaitMS        int                         `json:"wait_ms"`
	WaitElapsedMS int                         `json:"wait_elapsed_ms"`
	WaitTimedOut  bool                        `json:"wait_timed_out"`
	Receipt       userQuestionWaitToolReceipt `json:"receipt"`
}

type userQuestionWaitToolReceipt struct {
	Tool            string `json:"tool"`
	Action          string `json:"action"`
	ReadOnly        bool   `json:"read_only"`
	ExecutionPolicy string `json:"execution_policy"`
	RequestID       string `json:"request_id"`
	SessionID       string `json:"session_id"`
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
	TaskID          int64  `json:"task_id,omitempty"`
	QuestionChars   int    `json:"question_chars,omitempty"`
	AnswerChars     int    `json:"answer_chars,omitempty"`
	WaitMS          int    `json:"wait_ms"`
	WaitElapsedMS   int    `json:"wait_elapsed_ms"`
	WaitTimedOut    bool   `json:"wait_timed_out"`
	FollowupTool    string `json:"followup_tool,omitempty"`
}

type userQuestionWaitState struct {
	Found          bool
	Status         string
	Reason         string
	RequestID      string
	SessionID      string
	QuestionChars  int
	AnswerChars    int
	TimeoutSeconds int
	AskedAt        time.Time
	TaskID         int64
	FollowupTool   string
}

func (t *UserQuestionWaitTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	if t == nil || t.store == nil {
		return tools.ErrorResult("user_question_wait: outcome store unavailable"), nil
	}
	var input userQuestionWaitToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return tools.ErrorResult("user_question_wait: session_id is required"), nil
	}
	requestID := strings.TrimSpace(input.RequestID)
	if requestID == "" {
		return tools.ErrorResult("user_question_wait: request_id is required"), nil
	}
	waitMS := normalizeUserQuestionWaitMS(input.WaitMS)
	started := time.Now()
	for {
		state, err := t.userQuestionWaitState(sessionID, requestID)
		if err != nil {
			return tools.ErrorResult(fmt.Sprintf("user_question_wait: %v", err)), nil
		}
		elapsedMS := int(time.Since(started) / time.Millisecond)
		switch {
		case !state.Found:
			return t.userQuestionWaitResult(state, requestID, sessionID, waitMS, elapsedMS, false)
		case state.Status == "answered":
			return t.userQuestionWaitResult(state, requestID, sessionID, waitMS, elapsedMS, false)
		case state.Status == "cancelled":
			return t.userQuestionWaitResult(state, requestID, sessionID, waitMS, elapsedMS, false)
		case state.Status == "timed_out":
			return t.userQuestionWaitResult(state, requestID, sessionID, waitMS, elapsedMS, false)
		case elapsedMS >= waitMS:
			return t.userQuestionWaitResult(state, requestID, sessionID, waitMS, elapsedMS, true)
		}
		poll := time.Duration(userQuestionWaitPollMS) * time.Millisecond
		remaining := time.Duration(waitMS-elapsedMS) * time.Millisecond
		if remaining < poll {
			poll = remaining
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return tools.ErrorResult(fmt.Sprintf("user_question_wait: %v", ctx.Err())), nil
		case <-timer.C:
		}
	}
}

func (t *UserQuestionWaitTool) userQuestionWaitState(sessionID, requestID string) (userQuestionWaitState, error) {
	records, err := t.store.Recent(0)
	if err != nil {
		return userQuestionWaitState{}, err
	}
	return findUserQuestionWaitStateAt(records, sessionID, requestID, time.Now().UTC()), nil
}

func (t *UserQuestionWaitTool) userQuestionWaitResult(state userQuestionWaitState, requestID, sessionID string, waitMS, elapsedMS int, waitTimedOut bool) (*tools.Result, error) {
	status := state.Status
	if status == "" {
		status = "not_found"
	}
	followup := state.FollowupTool
	if status == "pending" && waitTimedOut {
		followup = UserQuestionWaitToolName
	}
	if (status == "cancelled" || status == "timed_out") && followup == "" {
		followup = UserQuestionListToolName
	}
	output := userQuestionWaitToolOutput{
		Status:        status,
		RequestID:     requestID,
		SessionID:     sessionID,
		QuestionChars: state.QuestionChars,
		AnswerChars:   state.AnswerChars,
		TaskID:        state.TaskID,
		WaitMS:        waitMS,
		WaitElapsedMS: elapsedMS,
		WaitTimedOut:  waitTimedOut,
		Receipt: userQuestionWaitToolReceipt{
			Tool:            UserQuestionWaitToolName,
			Action:          "wait",
			ReadOnly:        true,
			ExecutionPolicy: "user_input_wait",
			RequestID:       requestID,
			SessionID:       sessionID,
			Status:          status,
			Reason:          state.Reason,
			TaskID:          state.TaskID,
			QuestionChars:   state.QuestionChars,
			AnswerChars:     state.AnswerChars,
			WaitMS:          waitMS,
			WaitElapsedMS:   elapsedMS,
			WaitTimedOut:    waitTimedOut,
			FollowupTool:    followup,
		},
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("user_question_wait: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

func findUserQuestionWaitState(records []OutcomeRecord, sessionID, requestID string) userQuestionWaitState {
	return findUserQuestionWaitStateAt(records, sessionID, requestID, time.Now().UTC())
}

func findUserQuestionWaitStateAt(records []OutcomeRecord, sessionID, requestID string, now time.Time) userQuestionWaitState {
	sessionID = strings.TrimSpace(sessionID)
	requestID = strings.TrimSpace(requestID)
	ordered := append([]OutcomeRecord(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Timestamp.Before(ordered[j].Timestamp)
	})
	var state userQuestionWaitState
	for _, record := range ordered {
		for _, receipt := range record.ControlToolReceipts {
			if strings.TrimSpace(receipt.RequestID) != requestID || strings.TrimSpace(receipt.SessionID) != sessionID {
				continue
			}
			switch {
			case receipt.Tool == "ask_user_question" && receipt.Action == "request":
				state = userQuestionWaitState{
					Found:          true,
					Status:         "pending",
					RequestID:      requestID,
					SessionID:      sessionID,
					QuestionChars:  receipt.QuestionChars,
					TimeoutSeconds: receipt.TimeoutSeconds,
					AskedAt:        record.Timestamp,
				}
				if userQuestionTimedOut(record.Timestamp, receipt.TimeoutSeconds, now) {
					state.Status = "timed_out"
					state.Reason = "question timeout expired"
					state.FollowupTool = UserQuestionListToolName
				}
			case receipt.Tool == "user_question_answer" && receipt.Action == "answer":
				followup := strings.TrimSpace(receipt.FollowupTool)
				if followup == "" {
					followup = "task_monitor"
				}
				state = userQuestionWaitState{
					Found:         true,
					Status:        "answered",
					RequestID:     requestID,
					SessionID:     sessionID,
					QuestionChars: receipt.QuestionChars,
					AnswerChars:   receipt.AnswerChars,
					TaskID:        receipt.TaskID,
					FollowupTool:  followup,
				}
			case receipt.Tool == UserQuestionCancelToolName && receipt.Action == "cancel":
				questionChars := receipt.QuestionChars
				if questionChars == 0 {
					questionChars = state.QuestionChars
				}
				followup := strings.TrimSpace(receipt.FollowupTool)
				if followup == "" {
					followup = UserQuestionListToolName
				}
				state = userQuestionWaitState{
					Found:         true,
					Status:        "cancelled",
					Reason:        strings.TrimSpace(receipt.Reason),
					RequestID:     requestID,
					SessionID:     sessionID,
					QuestionChars: questionChars,
					FollowupTool:  followup,
				}
			}
		}
	}
	return state
}

func normalizeUserQuestionWaitMS(waitMS int) int {
	if waitMS <= 0 {
		return userQuestionWaitDefaultMS
	}
	if waitMS > userQuestionWaitMaxMS {
		return userQuestionWaitMaxMS
	}
	return waitMS
}

type UserQuestionCancelTool struct {
	store *OutcomeStore
}

func NewUserQuestionCancelTool(store *OutcomeStore) *UserQuestionCancelTool {
	return &UserQuestionCancelTool{store: store}
}

func (t *UserQuestionCancelTool) Name() string { return UserQuestionCancelToolName }

func (t *UserQuestionCancelTool) Description() string {
	return "Cancel a pending user-input request and record a durable cancellation receipt"
}

func (t *UserQuestionCancelTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"session_id": tools.String("Session id from the ask_user_question receipt."),
		"request_id": tools.String("Request id from the ask_user_question receipt."),
		"reason":     tools.String("Optional privacy-safe cancellation reason."),
	}, []string{"session_id", "request_id"})
}

func (t *UserQuestionCancelTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *UserQuestionCancelTool) Reversible() bool { return false }

func (t *UserQuestionCancelTool) Scope(json.RawMessage) tools.ToolScope {
	return tools.ToolScope{Persistent: true}
}

func (t *UserQuestionCancelTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *UserQuestionCancelTool) DeferInitialToolSchema() bool { return true }

type userQuestionCancelToolInput struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
	Reason    string `json:"reason"`
}

type userQuestionCancelToolOutput struct {
	Type          string             `json:"type"`
	Status        string             `json:"status"`
	RequestID     string             `json:"request_id"`
	SessionID     string             `json:"session_id"`
	Reason        string             `json:"reason,omitempty"`
	QuestionChars int                `json:"question_chars,omitempty"`
	Receipt       ControlToolReceipt `json:"receipt"`
}

func (t *UserQuestionCancelTool) Execute(_ context.Context, params json.RawMessage) (*tools.Result, error) {
	if t == nil || t.store == nil {
		return tools.ErrorResult("user_question_cancel: outcome store unavailable"), nil
	}
	var input userQuestionCancelToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return tools.ErrorResult("user_question_cancel: session_id is required"), nil
	}
	requestID := strings.TrimSpace(input.RequestID)
	if requestID == "" {
		return tools.ErrorResult("user_question_cancel: request_id is required"), nil
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "cancelled by operator"
	}
	records, err := t.store.Recent(0)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("user_question_cancel: %v", err)), nil
	}
	question, ok := FindPendingUserQuestion(records, sessionID, requestID)
	if !ok {
		return tools.ErrorResult("user_question_cancel: request_id is not pending for session_id"), nil
	}
	receipt := ControlToolReceipt{
		Tool:            UserQuestionCancelToolName,
		Action:          "cancel",
		ReadOnly:        false,
		Persistent:      true,
		ExecutionPolicy: "user_input_cancel",
		FollowupTool:    UserQuestionListToolName,
		RequestID:       requestID,
		SessionID:       sessionID,
		Status:          "cancelled",
		Reason:          reason,
		Terminal:        true,
		Found:           true,
		QuestionChars:   question.QuestionChars,
	}
	if err := t.store.Append(OutcomeRecord{
		ProjectID:           "elnath",
		Intent:              "user_input_cancel",
		Workflow:            "user_question_cancel",
		FinishReason:        "stop",
		Success:             true,
		SessionID:           sessionID,
		ControlToolReceipts: []ControlToolReceipt{receipt},
	}); err != nil {
		return tools.ErrorResult(fmt.Sprintf("user_question_cancel: %v", err)), nil
	}
	output := userQuestionCancelToolOutput{
		Type:          "user_input_request_cancelled",
		Status:        "cancelled",
		RequestID:     requestID,
		SessionID:     sessionID,
		Reason:        reason,
		QuestionChars: question.QuestionChars,
		Receipt:       receipt,
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("user_question_cancel: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}
