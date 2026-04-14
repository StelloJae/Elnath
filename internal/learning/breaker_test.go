package learning

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type testBreakerClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testBreakerClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testBreakerClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestBreakerAllowFresh(t *testing.T) {
	t.Parallel()

	clock := &testBreakerClock{now: time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)}
	breaker := NewBreaker(nil, BreakerConfig{Now: clock.Now})
	if !breaker.Allow() {
		t.Fatal("Allow() = false, want true")
	}
	status := breaker.Status()
	if status.Open {
		t.Fatal("Status().Open = true, want false")
	}
	if status.RecentFails != 0 {
		t.Fatalf("Status().RecentFails = %d, want 0", status.RecentFails)
	}
	if status.Threshold != 5 {
		t.Fatalf("Status().Threshold = %d, want 5", status.Threshold)
	}
}

func TestBreakerOpensAfterThresholdInWindow(t *testing.T) {
	t.Parallel()

	clock := &testBreakerClock{now: time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)}
	breaker := NewBreaker(nil, BreakerConfig{Now: clock.Now})
	for i := 0; i < 5; i++ {
		breaker.Record(errors.New("boom"))
	}
	if breaker.Allow() {
		t.Fatal("Allow() = true, want false after threshold")
	}
	status := breaker.Status()
	if !status.Open {
		t.Fatal("Status().Open = false, want true")
	}
	wantPauseUntil := clock.Now().Add(10 * time.Minute)
	if !status.PauseUntil.Equal(wantPauseUntil) {
		t.Fatalf("Status().PauseUntil = %v, want %v", status.PauseUntil, wantPauseUntil)
	}
	if status.RecentFails != 5 {
		t.Fatalf("Status().RecentFails = %d, want 5", status.RecentFails)
	}
}

func TestBreakerSuccessResetsFailures(t *testing.T) {
	t.Parallel()

	clock := &testBreakerClock{now: time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)}
	breaker := NewBreaker(nil, BreakerConfig{Now: clock.Now})
	breaker.Record(errors.New("boom"))
	breaker.Record(errors.New("boom"))
	breaker.Record(nil)
	status := breaker.Status()
	if status.RecentFails != 0 {
		t.Fatalf("Status().RecentFails = %d, want 0", status.RecentFails)
	}
	if !breaker.Allow() {
		t.Fatal("Allow() = false, want true after success reset")
	}
}

func TestBreakerPrunesFailuresOutsideWindow(t *testing.T) {
	t.Parallel()

	clock := &testBreakerClock{now: time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)}
	breaker := NewBreaker(nil, BreakerConfig{Now: clock.Now})
	breaker.Record(errors.New("boom"))
	clock.Advance(11 * time.Minute)
	for i := 0; i < 4; i++ {
		breaker.Record(errors.New("boom"))
	}
	if !breaker.Allow() {
		t.Fatal("Allow() = false, want true because old failure should be pruned")
	}
	status := breaker.Status()
	if status.RecentFails != 4 {
		t.Fatalf("Status().RecentFails = %d, want 4", status.RecentFails)
	}
}

func TestBreakerAllowsAgainAfterPauseExpires(t *testing.T) {
	t.Parallel()

	clock := &testBreakerClock{now: time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)}
	breaker := NewBreaker(nil, BreakerConfig{Now: clock.Now})
	for i := 0; i < 5; i++ {
		breaker.Record(errors.New("boom"))
	}
	clock.Advance(10*time.Minute + time.Second)
	if !breaker.Allow() {
		t.Fatal("Allow() = false, want true after pause expiry")
	}
	if breaker.Status().Open {
		t.Fatal("Status().Open = true, want false after pause expiry")
	}
}

func TestBreakerPersistsFailuresAcrossInstances(t *testing.T) {
	t.Parallel()

	clock := &testBreakerClock{now: time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)}
	statePath := filepath.Join(t.TempDir(), "llm_extraction_state.json")

	first := NewBreaker(nil, BreakerConfig{Now: clock.Now, StatePath: statePath})
	for i := 0; i < 2; i++ {
		first.Record(errors.New("boom"))
	}

	second := NewBreaker(nil, BreakerConfig{Now: clock.Now, StatePath: statePath})
	if second.Status().RecentFails != 2 {
		t.Fatalf("persisted RecentFails = %d, want 2", second.Status().RecentFails)
	}
	for i := 0; i < 3; i++ {
		second.Record(errors.New("boom"))
	}

	third := NewBreaker(nil, BreakerConfig{Now: clock.Now, StatePath: statePath})
	if third.Allow() {
		t.Fatal("Allow() = true, want false after persisted threshold reached")
	}
	if third.LastRun().IsZero() {
		t.Fatal("LastRun() = zero, want persisted timestamp")
	}
}

func TestBreakerConcurrentRecord(t *testing.T) {
	t.Parallel()

	clock := &testBreakerClock{now: time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)}
	breaker := NewBreaker(nil, BreakerConfig{Now: clock.Now, FailThreshold: 1000})
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 32; j++ {
				breaker.Record(errors.New("boom"))
			}
		}()
	}
	wg.Wait()
	if breaker.Allow() {
		t.Fatal("Allow() = true, want false after concurrent failures exceed threshold")
	}
}
