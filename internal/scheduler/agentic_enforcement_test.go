package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stello/elnath/internal/daemon"
)

func TestSchedulerAndAmbient_DefaultsPassThrough(t *testing.T) {
	enq := &mockEnq{}
	s := New(nil, enq, discardLogger())

	s.enqueueOnce(context.Background(), ScheduledTask{
		Name:     "legacy-scheduled",
		Prompt:   "run scheduled task",
		Interval: time.Minute,
	})

	_, payloads := enq.snapshot()
	if len(payloads) != 1 {
		t.Fatalf("payloads = %d, want 1", len(payloads))
	}
	payload := daemon.ParseTaskPayload(payloads[0])
	if payload.AgenticEnforcement != "" {
		t.Fatalf("AgenticEnforcement = %q, want empty default pass-through", payload.AgenticEnforcement)
	}
}
