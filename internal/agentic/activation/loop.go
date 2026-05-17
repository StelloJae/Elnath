package activation

import (
	"context"
	"log/slog"
	"time"
)

type Runner interface {
	RunOnce(context.Context, int) (Result, error)
}

type LoopOptions struct {
	Interval   time.Duration
	Limit      int
	RunOnStart bool
	Logger     *slog.Logger
}

func RunLoop(ctx context.Context, runner Runner, opts LoopOptions) {
	if runner == nil {
		return
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 25
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	run := func() {
		result, err := runner.RunOnce(ctx, limit)
		if err != nil {
			logger.Warn("agentic activation tick failed", "run_id", result.RunID, "error", err)
			return
		}
		logger.Info("agentic activation tick completed",
			"run_id", result.RunID,
			"followups_processed", result.Followups.Processed,
			"signals_processed", result.Signals.Processed,
			"enqueue_performed", result.EnqueuePerformed,
		)
	}
	if opts.RunOnStart {
		run()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
