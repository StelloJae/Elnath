package daemon

import (
	"context"
	"log/slog"
)

// LogSink is the built-in default sink that logs every task completion at Info
// level, ensuring completions are never silently swallowed.
type LogSink struct {
	logger *slog.Logger
}

// NewLogSink returns a LogSink backed by the given logger.
func NewLogSink(logger *slog.Logger) *LogSink {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogSink{logger: logger}
}

// NotifyCompletion logs the task completion and always returns nil.
func (s *LogSink) NotifyCompletion(_ context.Context, completion TaskCompletion) error {
	s.logger.Info("task completed",
		"task_id", completion.TaskID,
		"session_id", completion.SessionID,
		"status", string(completion.Status),
		"summary", completion.Summary,
		"duration_ms", completion.CompletedAt.Sub(completion.StartedAt).Milliseconds(),
	)
	return nil
}
