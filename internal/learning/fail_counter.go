package learning

import "sync"

type FailCounter struct {
	mu          sync.Mutex
	threshold   int
	consecutive int
	disabled    bool
}

func NewFailCounter(threshold int) *FailCounter {
	if threshold <= 0 {
		threshold = 3
	}
	return &FailCounter{threshold: threshold}
}

func (f *FailCounter) Allow() bool {
	if f == nil {
		return true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.disabled
}

func (f *FailCounter) Record(err error) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		f.consecutive = 0
		return
	}
	f.consecutive++
	if f.consecutive >= f.threshold {
		f.disabled = true
	}
}
