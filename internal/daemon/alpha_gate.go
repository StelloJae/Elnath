package daemon

import (
	"context"
	"fmt"
	"strings"
)

// ClosedAlphaTelemetry summarizes the continuity/runtime signals the Month 4
// closed-alpha gate expects to observe from queued daemon work.
type ClosedAlphaTelemetry struct {
	TotalTasks        int
	CompletionSignals int
	ResumeSignals     int
	DistinctSessions  int
	RetentionSignals  int
	TimeoutSignals    int
	TimeoutMetrics    TimeoutMetrics
}

// ClosedAlphaGateResult reports whether the current telemetry is sufficient to
// treat the Month 4 closed alpha gate as open, or whether it should remain
// conservatively fail-closed.
type ClosedAlphaGateResult struct {
	Open    bool
	Reasons []string
}

// ClosedAlphaTelemetry aggregates completion, resume, timeout, and repeat-use
// evidence from the daemon queue.
func (q *Queue) ClosedAlphaTelemetry(ctx context.Context) (ClosedAlphaTelemetry, error) {
	tasks, err := q.List(ctx)
	if err != nil {
		return ClosedAlphaTelemetry{}, fmt.Errorf("queue: closed alpha telemetry: %w", err)
	}

	timeoutMetrics, err := q.TimeoutMetrics(ctx)
	if err != nil {
		return ClosedAlphaTelemetry{}, fmt.Errorf("queue: closed alpha telemetry: %w", err)
	}

	sessionCounts := map[string]int{}
	telemetry := ClosedAlphaTelemetry{
		TotalTasks:     len(tasks),
		TimeoutMetrics: timeoutMetrics,
		TimeoutSignals: timeoutMetrics.IdleRecoveries + timeoutMetrics.ActiveButKilledRecoveries,
	}

	for _, task := range tasks {
		if task.Completion != nil {
			telemetry.CompletionSignals++
		}
		if sessionID := strings.TrimSpace(task.SessionID); sessionID != "" {
			telemetry.ResumeSignals++
			sessionCounts[sessionID]++
		}
	}

	telemetry.DistinctSessions = len(sessionCounts)
	for _, count := range sessionCounts {
		if count > 1 {
			telemetry.RetentionSignals++
		}
	}

	return telemetry, nil
}

// EvaluateClosedAlphaGate keeps the Month 4 gate fail-closed until the shared
// runtime proves all required signal classes exist in queue-backed telemetry.
func EvaluateClosedAlphaGate(telemetry ClosedAlphaTelemetry) ClosedAlphaGateResult {
	result := ClosedAlphaGateResult{Open: true}

	if telemetry.CompletionSignals == 0 {
		result.Reasons = append(result.Reasons, "completion signals are missing")
	}
	if telemetry.ResumeSignals == 0 {
		result.Reasons = append(result.Reasons, "resume signals are missing")
	}
	if telemetry.TimeoutSignals == 0 {
		result.Reasons = append(result.Reasons, "false-timeout signals are missing")
	}
	if telemetry.RetentionSignals == 0 {
		result.Reasons = append(result.Reasons, "repeat-use retention signals are missing")
	}

	result.Open = len(result.Reasons) == 0
	return result
}

// Summary returns a single operator-facing gate line.
func (r ClosedAlphaGateResult) Summary() string {
	if r.Open {
		return "Month 4 closed alpha gate: OPEN"
	}
	if len(r.Reasons) == 0 {
		return "Month 4 closed alpha gate: FAIL-CLOSED"
	}
	return fmt.Sprintf("Month 4 closed alpha gate: FAIL-CLOSED (%s)", strings.Join(r.Reasons, "; "))
}
