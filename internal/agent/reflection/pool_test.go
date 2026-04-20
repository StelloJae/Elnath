package reflection

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeEngine is a configurable Engine for pool tests.
type fakeEngine struct {
	delay   time.Duration
	err     error
	calls   atomic.Int64
	onStart func()
}

func (f *fakeEngine) Reflect(ctx context.Context, in Input) (Report, error) {
	f.calls.Add(1)
	if f.onStart != nil {
		f.onStart()
	}
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return Report{}, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.err != nil {
		return Report{}, f.err
	}
	return Report{
		Fingerprint:       in.Fingerprint,
		FinishReason:      in.FinishReason,
		SuggestedStrategy: StrategyRetrySmallerScope,
	}, nil
}

// recordingStore is a Store that collects Append calls in-memory.
type recordingStore struct {
	mu      sync.Mutex
	entries []Report
	err     error
}

func (s *recordingStore) Append(ctx context.Context, r Report, m StoreMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.entries = append(s.entries, r)
	return nil
}

func (s *recordingStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPool_EnqueueProcessed(t *testing.T) {
	eng := &fakeEngine{}
	store := &recordingStore{}
	pool := NewPool(eng, store, 2, 4, WithPoolLogger(quietLogger()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = pool.Shutdown(ctx)
	})

	if !pool.Enqueue(Input{Fingerprint: "X"}, StoreMeta{}) {
		t.Fatal("enqueue refused")
	}

	waitFor(t, 500*time.Millisecond, func() bool {
		return store.Count() == 1
	})
	stats := pool.Stats()
	if stats.Enqueued != 1 || stats.Processed != 1 {
		t.Fatalf("stats: %+v", stats)
	}
}

func TestPool_QueueFullDropsNewest(t *testing.T) {
	gate := make(chan struct{})
	eng := &fakeEngine{
		onStart: func() { <-gate },
	}
	store := &recordingStore{}
	pool := NewPool(eng, store, 1, 2, WithPoolLogger(quietLogger()))
	t.Cleanup(func() {
		close(gate)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = pool.Shutdown(ctx)
	})

	// First enqueue occupies the worker; next 2 fill the queue; 4th drops.
	accepted := 0
	dropped := 0
	for i := 0; i < 5; i++ {
		ok := pool.Enqueue(Input{Fingerprint: Fingerprint(string(rune('A' + i)))}, StoreMeta{})
		if ok {
			accepted++
		} else {
			dropped++
		}
	}
	if accepted == 0 {
		t.Fatal("pool accepted no jobs")
	}
	if dropped == 0 {
		t.Fatal("expected at least one drop once queue saturated")
	}
	if pool.Stats().DroppedQueueFull == 0 {
		t.Fatal("DroppedQueueFull counter not incremented")
	}
}

func TestPool_ShutdownDrainsQueued(t *testing.T) {
	eng := &fakeEngine{delay: 10 * time.Millisecond}
	store := &recordingStore{}
	pool := NewPool(eng, store, 2, 8, WithPoolLogger(quietLogger()))

	const n = 6
	for i := 0; i < n; i++ {
		if !pool.Enqueue(Input{Fingerprint: "Y"}, StoreMeta{}) {
			t.Fatalf("enqueue %d refused", i)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pool.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown err: %v", err)
	}
	if got := store.Count(); got != n {
		t.Fatalf("expected %d stored, got %d", n, got)
	}
}

func TestPool_ShutdownContextTimeout(t *testing.T) {
	blocker := make(chan struct{})
	eng := &fakeEngine{
		onStart: func() { <-blocker },
	}
	store := &recordingStore{}
	pool := NewPool(eng, store, 1, 2, WithPoolLogger(quietLogger()))
	t.Cleanup(func() { close(blocker) })

	pool.Enqueue(Input{Fingerprint: "stuck"}, StoreMeta{})
	time.Sleep(20 * time.Millisecond) // let the worker start

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := pool.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestPool_StoreError_DoesNotPanic(t *testing.T) {
	eng := &fakeEngine{}
	store := &recordingStore{err: errors.New("disk full")}
	pool := NewPool(eng, store, 1, 2, WithPoolLogger(quietLogger()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = pool.Shutdown(ctx)
	})

	pool.Enqueue(Input{Fingerprint: "Z"}, StoreMeta{})
	waitFor(t, 500*time.Millisecond, func() bool {
		return pool.Stats().Processed == 1
	})
}

func TestPool_EnqueueAfterShutdown_Rejected(t *testing.T) {
	pool := NewPool(&fakeEngine{}, &recordingStore{}, 1, 2, WithPoolLogger(quietLogger()))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = pool.Shutdown(ctx)

	if pool.Enqueue(Input{Fingerprint: "Q"}, StoreMeta{}) {
		t.Fatal("expected enqueue after shutdown to be rejected")
	}
}

// Integration smoke: Pool → FileStore end-to-end, one record persisted.
func TestPool_IntegrationWithFileStore(t *testing.T) {
	dir := t.TempDir()
	store := NewFileStore(filepath.Join(dir, "attempts.jsonl"))
	eng := &fakeEngine{}
	pool := NewPool(eng, store, 1, 4, WithPoolLogger(quietLogger()))

	pool.Enqueue(Input{Fingerprint: "INTEG"}, StoreMeta{TaskID: "11"})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pool.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	sum, err := store.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if sum.Total != 1 {
		t.Fatalf("expected 1 record, got %d", sum.Total)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met in %v", timeout)
}
