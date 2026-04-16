package learning

import (
	"context"
	"log/slog"
	"time"

	"github.com/stello/elnath/internal/ambient"
)

// RunDailyConsolidationLoop blocks until ctx is cancelled, firing
// Consolidator.Run once per day at dailyAt. It logs outcomes through logger
// and tolerates individual run failures — a failure simply waits for the next
// daily slot, so transient provider problems do not break the loop.
//
// The gate attached to the consolidator still governs whether the run
// actually consolidates; this loop only decides when Run is invoked.
func RunDailyConsolidationLoop(ctx context.Context, c *Consolidator, dailyAt ambient.TimeOfDay, logger *slog.Logger) {
	if c == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "consolidation-scheduler")

	for {
		delay := ambient.NextDailyRun(time.Now(), dailyAt)
		logger.Info("consolidation scheduler waiting", "delay", delay, "target_hour", dailyAt.Hour, "target_minute", dailyAt.Minute)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			logger.Info("consolidation scheduler stopped")
			return
		case <-timer.C:
			result, err := c.Run(ctx)
			switch {
			case err != nil:
				logger.Error("consolidation run errored", "err", err)
			case result.Skipped:
				logger.Info("consolidation skipped", "reason", result.SkipReason)
			case result.Error != nil:
				logger.Warn("consolidation failed, will retry next slot", "err", result.Error)
			default:
				logger.Info("consolidation completed",
					"syntheses", result.SynthesisCount,
					"superseded", result.SupersededCount,
				)
			}
		}
	}
}
