package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agent/errorclass"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

type captureHook struct {
	mu    sync.Mutex
	calls []ReflectionInput
}

func (c *captureHook) enqueue() ReflectionEnqueuer {
	return func(in ReflectionInput) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.calls = append(c.calls, in)
	}
}

func (c *captureHook) snapshot() []ReflectionInput {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ReflectionInput, len(c.calls))
	copy(out, c.calls)
	return out
}

func TestAgentReflectionHook_TriggeredOnError(t *testing.T) {
	cap := &captureHook{}
	// Use a non-retryable error (ModelNotFound) so the test finishes quickly
	// without exercising the exponential-backoff retry loop. The hook logic
	// is orthogonal to retry behaviour.
	p := &mockProvider{
		streamFn: func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			return errors.New("model does not exist: foo")
		},
	}
	a := New(p, tools.NewRegistry(),
		WithReflection(cap.enqueue()),
		WithMaxIterations(1),
	)

	_, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("hi")}, nil)
	if err == nil {
		t.Fatal("expected provider error")
	}
	calls := cap.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 hook call, got %d", len(calls))
	}
	got := calls[0]
	if got.FinishReason != FinishReasonError {
		t.Fatalf("finish reason = %q, want %q", got.FinishReason, FinishReasonError)
	}
	if got.ErrCategory != errorclass.ModelNotFound {
		t.Fatalf("category = %q, want %q", got.ErrCategory, errorclass.ModelNotFound)
	}
	if got.ErrorSummary == "" {
		t.Fatal("error summary should be populated on error path")
	}
	if len(got.Messages) == 0 {
		t.Fatal("messages must include the initial user turn")
	}
}

func TestAgentReflectionHook_NotTriggeredOnStop(t *testing.T) {
	cap := &captureHook{}
	p := &mockProvider{streamFn: textOnlyStreamFn("done")}

	a := New(p, tools.NewRegistry(), WithReflection(cap.enqueue()))
	_, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("hi")}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls := cap.snapshot(); len(calls) != 0 {
		t.Fatalf("hook must not fire on clean Stop, got %d calls", len(calls))
	}
}

func TestAgentReflectionHook_SkipsAuthCategory(t *testing.T) {
	// Auth is one of the skip-categories (spec §3.1) AND non-retryable, so
	// the test exits quickly and asserts the gate correctly swallows the
	// credential-rotation class of failures.
	cap := &captureHook{}
	p := &mockProvider{
		streamFn: func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			return errors.New("unauthorized: invalid api key")
		},
	}
	a := New(p, tools.NewRegistry(),
		WithReflection(cap.enqueue()),
		WithMaxIterations(1),
	)

	_, _ = a.Run(context.Background(), []llm.Message{llm.NewUserMessage("hi")}, nil)
	if calls := cap.snapshot(); len(calls) != 0 {
		t.Fatalf("auth category must skip hook, got %d calls: %+v", len(calls), calls)
	}
}

func TestAgentReflectionHook_DisabledFlag(t *testing.T) {
	// No WithReflection option → nil enqueuer → no hook invocation regardless
	// of outcome.
	p := &mockProvider{
		streamFn: func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			return errors.New("model does not exist")
		},
	}
	a := New(p, tools.NewRegistry(), WithMaxIterations(1))

	_, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("hi")}, nil)
	if err == nil {
		t.Fatal("expected error to surface even without reflection")
	}
	// no capture available — success is the absence of a panic / extra behaviour.
}

func TestAgentReflectionHook_SkippedOnContextCancel(t *testing.T) {
	cap := &captureHook{}
	p := &mockProvider{
		streamFn: func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	a := New(p, tools.NewRegistry(),
		WithReflection(cap.enqueue()),
		WithMaxIterations(1),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, _ = a.Run(ctx, []llm.Message{llm.NewUserMessage("hi")}, nil)
	if calls := cap.snapshot(); len(calls) != 0 {
		t.Fatalf("context cancel must skip hook, got %+v", calls)
	}
}

func TestAgentReflectionHook_NonBlockingReturn(t *testing.T) {
	// The enqueuer contract requires non-blocking semantics. Here we simulate
	// a real async consumer (goroutine handoff) and assert Run returns in <<
	// the consumer's processing time, proving the agent does not synchronously
	// wait on reflection work.
	start := make(chan struct{})
	done := make(chan struct{})
	enq := func(in ReflectionInput) {
		close(start)
		go func() {
			time.Sleep(300 * time.Millisecond)
			close(done)
		}()
	}

	p := &mockProvider{
		streamFn: func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			return errors.New("model does not exist")
		},
	}
	a := New(p, tools.NewRegistry(),
		WithReflection(enq),
		WithMaxIterations(1),
	)

	t0 := time.Now()
	_, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("hi")}, nil)
	runDur := time.Since(t0)

	if err == nil {
		t.Fatal("expected error")
	}
	select {
	case <-start:
	default:
		t.Fatal("enqueuer was not invoked")
	}
	if runDur > 100*time.Millisecond {
		t.Fatalf("Run blocked longer than enqueue latency: %v", runDur)
	}
	// reflection work finishes later
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("background reflection did not finish in 1s")
	}
}

