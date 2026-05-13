package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/tools"
)

const UserQuestionListToolName = "user_question_list"

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
