package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/stello/elnath/internal/tools"
)

const AskUserQuestionToolName = "ask_user_question"

type AskUserQuestionTool struct{}

func NewAskUserQuestionTool() *AskUserQuestionTool {
	return &AskUserQuestionTool{}
}

func (t *AskUserQuestionTool) Name() string { return AskUserQuestionToolName }

func (t *AskUserQuestionTool) Description() string {
	return "Return a structured user-input request when safe progress requires clarification"
}

func (t *AskUserQuestionTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"question":        tools.String("The concise question to ask the user."),
		"options":         tools.Array("Optional suggested answers. Keep short and mutually exclusive.", "string"),
		"allow_free_text": tools.Bool("Whether the user may answer outside the suggested options. Defaults to true."),
		"timeout_seconds": tools.Int("Optional UI timeout hint in seconds. Zero means no blocking timeout is requested."),
	}, []string{"question"})
}

func (t *AskUserQuestionTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *AskUserQuestionTool) Reversible() bool { return true }

func (t *AskUserQuestionTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *AskUserQuestionTool) ShouldCancelSiblingsOnError() bool { return false }

type askUserQuestionToolInput struct {
	Question       string   `json:"question"`
	Options        []string `json:"options"`
	AllowFreeText  *bool    `json:"allow_free_text"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

type askUserQuestionToolOutput struct {
	Type           string                 `json:"type"`
	Question       string                 `json:"question"`
	Options        []string               `json:"options,omitempty"`
	AllowFreeText  bool                   `json:"allow_free_text"`
	TimeoutSeconds int                    `json:"timeout_seconds"`
	RequestID      string                 `json:"request_id"`
	SessionID      string                 `json:"session_id,omitempty"`
	Instruction    string                 `json:"instruction"`
	Receipt        askUserQuestionReceipt `json:"receipt"`
}

type askUserQuestionReceipt struct {
	Tool            string `json:"tool"`
	Action          string `json:"action"`
	ReadOnly        bool   `json:"read_only"`
	ExecutionPolicy string `json:"execution_policy"`
	QuestionChars   int    `json:"question_chars"`
	OptionCount     int    `json:"option_count"`
	AllowFreeText   bool   `json:"allow_free_text"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	RequestID       string `json:"request_id"`
	SessionID       string `json:"session_id,omitempty"`
}

func (t *AskUserQuestionTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	var input askUserQuestionToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	question := strings.TrimSpace(input.Question)
	if question == "" {
		return tools.ErrorResult("ask_user_question: question is required"), nil
	}

	output := askUserQuestionToolOutput{
		Type:           "user_input_required",
		Question:       question,
		Options:        cleanUserQuestionOptions(input.Options),
		AllowFreeText:  true,
		TimeoutSeconds: normalizeUserQuestionTimeout(input.TimeoutSeconds),
		RequestID:      uuid.NewString(),
		SessionID:      tools.SessionIDFrom(ctx),
		Instruction:    "Stop and ask the user this question; do not guess an answer or continue with assumptions.",
	}
	if input.AllowFreeText != nil {
		output.AllowFreeText = *input.AllowFreeText
	}
	output.Receipt = askUserQuestionReceipt{
		Tool:            AskUserQuestionToolName,
		Action:          "request",
		ReadOnly:        true,
		ExecutionPolicy: "user_input_request",
		QuestionChars:   len([]rune(question)),
		OptionCount:     len(output.Options),
		AllowFreeText:   output.AllowFreeText,
		TimeoutSeconds:  output.TimeoutSeconds,
		RequestID:       output.RequestID,
		SessionID:       output.SessionID,
	}

	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("ask_user_question: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

func cleanUserQuestionOptions(options []string) []string {
	out := make([]string, 0, len(options))
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		out = append(out, option)
	}
	return out
}

func normalizeUserQuestionTimeout(seconds int) int {
	if seconds < 0 {
		return 0
	}
	return seconds
}
