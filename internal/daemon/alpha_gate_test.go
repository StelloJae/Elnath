package daemon

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestClosedAlphaTelemetryAggregatesRequiredSignals(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	ctx := context.Background()

	firstID, err := q.Enqueue(ctx, "first alpha rehearsal")
	if err != nil {
		t.Fatalf("Enqueue(first): %v", err)
	}
	first, err := q.Next(ctx)
	if err != nil {
		t.Fatalf("Next(first): %v", err)
	}
	if first.ID != firstID {
		t.Fatalf("first task id = %d, want %d", first.ID, firstID)
	}
	if err := q.BindSession(ctx, first.ID, "sess-a"); err != nil {
		t.Fatalf("BindSession(first): %v", err)
	}
	if err := q.MarkDone(ctx, first.ID, "done", "background completion delivered"); err != nil {
		t.Fatalf("MarkDone(first): %v", err)
	}

	second, err := q.Enqueue(ctx, "second alpha rehearsal")
	if err != nil {
		t.Fatalf("Enqueue(second): %v", err)
	}
	claimedSecond, err := q.Next(ctx)
	if err != nil {
		t.Fatalf("Next(second): %v", err)
	}
	if claimedSecond.ID != second {
		t.Fatalf("second task id = %d, want %d", claimedSecond.ID, second)
	}
	if err := q.BindSession(ctx, claimedSecond.ID, "sess-a"); err != nil {
		t.Fatalf("BindSession(second): %v", err)
	}
	if err := q.MarkDone(ctx, claimedSecond.ID, "done again", "resume path confirmed"); err != nil {
		t.Fatalf("MarkDone(second): %v", err)
	}

	third, err := q.Enqueue(ctx, "timeout rehearsal")
	if err != nil {
		t.Fatalf("Enqueue(third): %v", err)
	}
	claimedThird, err := q.Next(ctx)
	if err != nil {
		t.Fatalf("Next(third): %v", err)
	}
	if claimedThird.ID != third {
		t.Fatalf("third task id = %d, want %d", claimedThird.ID, third)
	}
	if err := q.BindSession(ctx, claimedThird.ID, "sess-b"); err != nil {
		t.Fatalf("BindSession(third): %v", err)
	}

	startedAt := time.Now().Add(-12 * time.Minute).UnixMilli()
	recentActivity := time.Now().Add(-8 * time.Minute).UnixMilli()
	if _, err := db.Exec(`
		UPDATE task_queue
		SET started_at = ?, updated_at = ?, progress = ?
		WHERE id = ?`,
		startedAt, recentActivity, "still running", claimedThird.ID,
	); err != nil {
		t.Fatalf("force stale active window: %v", err)
	}
	if recovered, err := q.RecoverStale(ctx, 5*time.Minute, 0); err != nil {
		t.Fatalf("RecoverStale: %v", err)
	} else if recovered != 1 {
		t.Fatalf("RecoverStale recovered = %d, want 1", recovered)
	}

	telemetry, err := q.ClosedAlphaTelemetry(ctx)
	if err != nil {
		t.Fatalf("ClosedAlphaTelemetry: %v", err)
	}

	if telemetry.TotalTasks != 3 {
		t.Fatalf("TotalTasks = %d, want 3", telemetry.TotalTasks)
	}
	if telemetry.CompletionSignals != 2 {
		t.Fatalf("CompletionSignals = %d, want 2", telemetry.CompletionSignals)
	}
	if telemetry.ResumeSignals != 3 {
		t.Fatalf("ResumeSignals = %d, want 3", telemetry.ResumeSignals)
	}
	if telemetry.DistinctSessions != 2 {
		t.Fatalf("DistinctSessions = %d, want 2", telemetry.DistinctSessions)
	}
	if telemetry.RetentionSignals != 1 {
		t.Fatalf("RetentionSignals = %d, want 1", telemetry.RetentionSignals)
	}
	if telemetry.TimeoutSignals != 1 {
		t.Fatalf("TimeoutSignals = %d, want 1", telemetry.TimeoutSignals)
	}
	if telemetry.TimeoutMetrics.ActiveButKilledRecoveries != 1 {
		t.Fatalf("ActiveButKilledRecoveries = %d, want 1", telemetry.TimeoutMetrics.ActiveButKilledRecoveries)
	}

	gate := EvaluateClosedAlphaGate(telemetry)
	if !gate.Open {
		t.Fatalf("EvaluateClosedAlphaGate unexpectedly failed: %+v", gate)
	}
	if got := gate.Summary(); got != "Month 4 closed alpha gate: OPEN" {
		t.Fatalf("Summary() = %q, want open gate line", got)
	}
}

func TestEvaluateClosedAlphaGateFailsClosedWithoutSignalCoverage(t *testing.T) {
	gate := EvaluateClosedAlphaGate(ClosedAlphaTelemetry{
		CompletionSignals: 1,
	})
	if gate.Open {
		t.Fatalf("expected fail-closed gate, got %+v", gate)
	}

	for _, want := range []string{
		"resume signals are missing",
		"false-timeout signals are missing",
		"repeat-use retention signals are missing",
	} {
		if !strings.Contains(gate.Summary(), want) {
			t.Fatalf("Summary() = %q, want to contain %q", gate.Summary(), want)
		}
	}
}
