package daemon

import "context"

// TaskRunnerResult carries the output of a single parsed-payload task execution.
type TaskRunnerResult struct {
	Summary   string
	Result    string
	SessionID string
}

// TaskRunner executes a parsed task payload.
type TaskRunner interface {
	Run(ctx context.Context, payload TaskPayload, onText func(string)) (TaskRunnerResult, error)
}
