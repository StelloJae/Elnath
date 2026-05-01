package ambient

import (
	"context"
	"testing"

	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/event"
)

func TestAmbientScheduler_DefaultsPassThrough(t *testing.T) {
	var seenPayload string
	runner := func(_ context.Context, payload string, _ event.Sink) (daemon.TaskResult, error) {
		seenPayload = payload
		return daemon.TaskResult{Summary: "done"}, nil
	}
	cfg := Config{
		Tasks: []BootTask{
			{Title: "startup", Prompt: "ambient prompt", Schedule: Schedule{Type: ScheduleStartup}, Silent: true},
		},
		Runner:        runner,
		MaxConcurrent: 1,
	}

	s := NewScheduler(cfg)
	s.Start(context.Background())
	s.Stop()

	if seenPayload != "ambient prompt" {
		t.Fatalf("payload = %q, want raw ambient prompt", seenPayload)
	}
}
