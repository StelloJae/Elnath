package learning

import (
	"errors"
	"sync"
	"testing"
)

func TestFailCounterAllowFresh(t *testing.T) {
	t.Parallel()

	if !NewFailCounter(3).Allow() {
		t.Fatal("Allow() = false, want true")
	}
}

func TestFailCounterDisablesAfterThreshold(t *testing.T) {
	t.Parallel()

	counter := NewFailCounter(3)
	for i := 0; i < 3; i++ {
		counter.Record(errors.New("boom"))
	}
	if counter.Allow() {
		t.Fatal("Allow() = true, want false after threshold")
	}
}

func TestFailCounterSuccessResetsBeforeThreshold(t *testing.T) {
	t.Parallel()

	counter := NewFailCounter(3)
	counter.Record(errors.New("boom"))
	counter.Record(errors.New("boom"))
	counter.Record(nil)
	counter.Record(errors.New("boom"))
	if !counter.Allow() {
		t.Fatal("Allow() = false, want true because success reset count before threshold")
	}
}

func TestFailCounterDisabledDoesNotRecover(t *testing.T) {
	t.Parallel()

	counter := NewFailCounter(3)
	for i := 0; i < 3; i++ {
		counter.Record(errors.New("boom"))
	}
	counter.Record(nil)
	if counter.Allow() {
		t.Fatal("Allow() = true, want false after disable")
	}
}

func TestFailCounterConcurrentRecord(t *testing.T) {
	t.Parallel()

	counter := NewFailCounter(1000)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 32; j++ {
				counter.Record(errors.New("boom"))
			}
		}()
	}
	wg.Wait()
	if counter.Allow() {
		t.Fatal("Allow() = true, want false after concurrent failures exceed threshold")
	}
}
