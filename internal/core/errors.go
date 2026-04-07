package core

import "errors"

var (
	ErrNotFound       = errors.New("not found")
	ErrTimeout        = errors.New("operation timed out")
	ErrMaxIterations  = errors.New("max iterations exceeded")
	ErrPermissionDeny = errors.New("permission denied")
	ErrSessionCorrupt = errors.New("session data corrupted")
	ErrContextOverflow = errors.New("context window exceeded")
	ErrProviderError  = errors.New("llm provider error")
	ErrToolExecution  = errors.New("tool execution failed")
	ErrCostCapReached = errors.New("cost cap reached")
)
