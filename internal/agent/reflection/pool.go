package reflection

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Pool runs reflection jobs asynchronously so agent.Run never blocks on
// engine/store latency. Bounded queue + bounded concurrency + 30s shutdown
// grace (spec §3.3 safeguards).
type Pool struct {
	engine  Engine
	store   Store
	logger  *slog.Logger
	queue   chan job
	sem     chan struct{}
	wg      sync.WaitGroup
	stopped atomic.Bool
	stopCh  chan struct{}
	stopOnce sync.Once

	droppedQueueFull atomic.Int64
	enqueued         atomic.Int64
	processed        atomic.Int64
}

type job struct {
	input Input
	meta  StoreMeta
}

// PoolOption configures a Pool at construction time.
type PoolOption func(*Pool)

// WithPoolLogger overrides the default slog.Default() logger.
func WithPoolLogger(l *slog.Logger) PoolOption {
	return func(p *Pool) {
		if l != nil {
			p.logger = l
		}
	}
}

// NewPool constructs a Pool with the given concurrency and queue size. The
// dispatcher goroutine starts immediately. Callers MUST call Shutdown before
// process exit to drain pending jobs.
func NewPool(engine Engine, store Store, maxConcurrent, queueSize int, opts ...PoolOption) *Pool {
	if maxConcurrent <= 0 {
		maxConcurrent = 2
	}
	if queueSize <= 0 {
		queueSize = 10
	}
	p := &Pool{
		engine: engine,
		store:  store,
		logger: slog.Default(),
		queue:  make(chan job, queueSize),
		sem:    make(chan struct{}, maxConcurrent),
		stopCh: make(chan struct{}),
	}
	for _, o := range opts {
		o(p)
	}
	p.wg.Add(1)
	go p.dispatch()
	return p
}

// Enqueue offers one reflection job. It returns false (and logs a warning)
// when the queue is full or the Pool is shutting down. The call never blocks.
func (p *Pool) Enqueue(in Input, meta StoreMeta) bool {
	if p.stopped.Load() {
		p.logger.Warn("reflection enqueue on shutdown", "fingerprint", in.Fingerprint)
		return false
	}
	select {
	case p.queue <- job{input: in, meta: meta}:
		p.enqueued.Add(1)
		return true
	default:
		p.droppedQueueFull.Add(1)
		p.logger.Warn("reflection queue full", "dropped_fingerprint", in.Fingerprint)
		return false
	}
}

// Stats captures observability counters (consumed by status command / tests).
type Stats struct {
	Enqueued         int64
	Processed        int64
	DroppedQueueFull int64
}

// Stats returns a snapshot of counters.
func (p *Pool) Stats() Stats {
	return Stats{
		Enqueued:         p.enqueued.Load(),
		Processed:        p.processed.Load(),
		DroppedQueueFull: p.droppedQueueFull.Load(),
	}
}

// dispatch reads from the queue and spawns workers up to the concurrency limit.
func (p *Pool) dispatch() {
	defer p.wg.Done()
	for {
		select {
		case <-p.stopCh:
			return
		case j, ok := <-p.queue:
			if !ok {
				return
			}
			p.sem <- struct{}{}
			p.wg.Add(1)
			go func(j job) {
				defer p.wg.Done()
				defer func() { <-p.sem }()
				p.runJob(j)
			}(j)
		}
	}
}

// runJob executes one reflection with an independent 15s cap so parent
// context cancellation (user abort) does not starve the observation.
func (p *Pool) runJob(j job) {
	jobCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	report, err := p.engine.Reflect(jobCtx, j.input)
	if err != nil {
		p.logger.Warn("reflection failed",
			"stage", "engine",
			"err", err,
			"fingerprint", j.input.Fingerprint,
		)
		p.processed.Add(1)
		return
	}
	if storeErr := p.store.Append(jobCtx, report, j.meta); storeErr != nil {
		p.logger.Warn("reflection failed",
			"stage", "store",
			"err", storeErr,
			"fingerprint", j.input.Fingerprint,
		)
		p.processed.Add(1)
		return
	}
	p.logger.Info("reflection completed",
		"fingerprint", j.input.Fingerprint,
		"finish_reason", j.input.FinishReason,
		"error_category", j.input.ErrorCategory,
		"suggested_strategy", report.SuggestedStrategy,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	p.processed.Add(1)
}

// Shutdown stops accepting new jobs, drains any queued work up to the grace
// deadline, then returns. Subsequent calls are no-ops.
func (p *Pool) Shutdown(ctx context.Context) error {
	p.stopOnce.Do(func() {
		p.stopped.Store(true)
		close(p.queue)
	})

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		close(p.stopCh)
		return ctx.Err()
	}
}
