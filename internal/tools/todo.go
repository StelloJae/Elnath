package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const TodoWriteName = "todo_write"

type TodoWriteTool struct{}

func NewTodoWriteTool() *TodoWriteTool {
	return &TodoWriteTool{}
}

func (t *TodoWriteTool) Name() string { return TodoWriteName }

func (t *TodoWriteTool) Description() string {
	return "Replace the current session todo checklist with structured task statuses"
}

func (t *TodoWriteTool) Schema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"todos": map[string]any{
				"type":        "array",
				"description": "Complete replacement todo list for the current session.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"content": map[string]any{
							"type":        "string",
							"description": "Short task description.",
						},
						"status": map[string]any{
							"type":        "string",
							"description": "Current task status.",
							"enum":        []string{"pending", "in_progress", "completed"},
						},
						"active_form": map[string]any{
							"type":        "string",
							"description": "Optional in-progress phrasing for UI or progress display.",
						},
						"activeForm": map[string]any{
							"type":        "string",
							"description": "Camel-case compatibility alias for active_form.",
						},
					},
					"required": []string{"content", "status"},
				},
			},
		},
		"required": []string{"todos"},
	}
	raw, _ := json.Marshal(schema)
	return raw
}

func (t *TodoWriteTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *TodoWriteTool) Reversible() bool { return true }

func (t *TodoWriteTool) Scope(json.RawMessage) ToolScope { return ToolScope{} }

func (t *TodoWriteTool) ShouldCancelSiblingsOnError() bool { return false }

type todoWriteInput struct {
	Todos []todoItemInput `json:"todos"`
}

type todoItemInput struct {
	Content         string `json:"content"`
	Status          string `json:"status"`
	ActiveForm      string `json:"active_form"`
	ActiveFormCamel string `json:"activeForm"`
}

type todoWriteOutput struct {
	Todos                    []todoItemOutput `json:"todos"`
	Total                    int              `json:"total"`
	Counts                   map[string]int   `json:"counts"`
	AllCompleted             bool             `json:"all_completed"`
	VerificationNudgeNeeded  bool             `json:"verification_nudge_needed,omitempty"`
	VerificationNudgeMessage string           `json:"verification_nudge_message,omitempty"`
	Receipt                  todoWriteReceipt `json:"receipt"`
}

type todoItemOutput struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"active_form,omitempty"`
}

type todoWriteReceipt struct {
	Tool                       string `json:"tool"`
	Action                     string `json:"action"`
	ReadOnly                   bool   `json:"read_only"`
	Persistent                 bool   `json:"persistent"`
	ExecutionPolicy            string `json:"execution_policy"`
	Total                      int    `json:"total"`
	Pending                    int    `json:"pending"`
	InProgress                 int    `json:"in_progress"`
	Completed                  int    `json:"completed"`
	AllCompleted               bool   `json:"all_completed"`
	VerificationNudgeNeeded    bool   `json:"verification_nudge_needed"`
	VerificationNudgeAvailable bool   `json:"verification_nudge_available"`
}

func (t *TodoWriteTool) Execute(_ context.Context, params json.RawMessage) (*Result, error) {
	if len(params) == 0 {
		return ErrorResult("todo_write: missing params"), nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(params, &raw); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	if _, ok := raw["todos"]; !ok {
		return ErrorResult("todo_write: missing todos"), nil
	}

	var input todoWriteInput
	if err := json.Unmarshal(params, &input); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}

	todos := make([]todoItemOutput, 0, len(input.Todos))
	counts := map[string]int{
		"pending":     0,
		"in_progress": 0,
		"completed":   0,
	}
	for i, item := range input.Todos {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			return ErrorResult(fmt.Sprintf("todo_write: todos[%d].content is required", i)), nil
		}

		status := strings.TrimSpace(strings.ToLower(item.Status))
		if _, ok := counts[status]; !ok {
			return ErrorResult(fmt.Sprintf("todo_write: todos[%d].status must be pending, in_progress, or completed", i)), nil
		}
		counts[status]++

		activeForm := strings.TrimSpace(item.ActiveForm)
		if activeForm == "" {
			activeForm = strings.TrimSpace(item.ActiveFormCamel)
		}
		todos = append(todos, todoItemOutput{
			Content:    content,
			Status:     status,
			ActiveForm: activeForm,
		})
	}
	if counts["in_progress"] > 1 {
		return ErrorResult("todo_write: at most one in_progress todo is allowed"), nil
	}

	allCompleted := len(todos) > 0 && counts["completed"] == len(todos)
	nudgeNeeded := allCompleted && len(todos) >= 3 && !hasVerificationTodo(todos)
	output := todoWriteOutput{
		Todos:                   todos,
		Total:                   len(todos),
		Counts:                  counts,
		AllCompleted:            allCompleted,
		VerificationNudgeNeeded: nudgeNeeded,
		Receipt: todoWriteReceipt{
			Tool:                       TodoWriteName,
			Action:                     "replace",
			ReadOnly:                   false,
			Persistent:                 false,
			ExecutionPolicy:            "session_todo_scratchpad",
			Total:                      len(todos),
			Pending:                    counts["pending"],
			InProgress:                 counts["in_progress"],
			Completed:                  counts["completed"],
			AllCompleted:               allCompleted,
			VerificationNudgeNeeded:    nudgeNeeded,
			VerificationNudgeAvailable: true,
		},
	}
	if nudgeNeeded {
		output.VerificationNudgeMessage = "All tasks are completed, but no verification-oriented todo was present. Run or record verification before making a final success claim."
	}

	rawOutput, err := json.Marshal(output)
	if err != nil {
		return ErrorResult(fmt.Sprintf("todo_write: marshal output: %v", err)), nil
	}
	return SuccessResult(string(rawOutput)), nil
}

func hasVerificationTodo(todos []todoItemOutput) bool {
	for _, todo := range todos {
		text := strings.ToLower(todo.Content + " " + todo.ActiveForm)
		for _, marker := range []string{"verify", "verification", "test", "검증", "테스트"} {
			if strings.Contains(text, marker) {
				return true
			}
		}
	}
	return false
}
