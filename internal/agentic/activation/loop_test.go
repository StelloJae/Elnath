package activation

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

type fakeActivationRunner struct {
	calls  int
	cancel context.CancelFunc
}

func (r *fakeActivationRunner) RunOnce(context.Context, int) (Result, error) {
	r.calls++
	if r.cancel != nil {
		r.cancel()
	}
	return Result{RunID: int64(r.calls), Status: "succeeded"}, nil
}

func TestRunLoop_RunOnStartExecutesOnceAndStopsOnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &fakeActivationRunner{cancel: cancel}

	RunLoop(ctx, runner, LoopOptions{
		Interval:   time.Hour,
		Limit:      3,
		RunOnStart: true,
		Logger:     slog.New(slog.NewTextHandler(testDiscard{}, nil)),
	})

	if runner.calls != 1 {
		t.Fatalf("calls = %d, want one startup activation", runner.calls)
	}
}

type testDiscard struct{}

func (testDiscard) Write(p []byte) (int, error) { return len(p), nil }
