package tools

import (
	"context"
	"encoding/json"
)

// Tool is the interface all executable tools must implement.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(ctx context.Context, params json.RawMessage) (*Result, error)
}

// Result holds the output of a tool execution.
type Result struct {
	Output  string
	IsError bool
}

// ErrorResult returns a Result that signals a tool execution failure.
func ErrorResult(msg string) *Result {
	return &Result{Output: msg, IsError: true}
}

// SuccessResult returns a Result that signals successful tool execution.
func SuccessResult(output string) *Result {
	return &Result{Output: output, IsError: false}
}
