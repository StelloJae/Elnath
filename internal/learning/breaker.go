package learning

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultBreakerWindowSize    = 10 * time.Minute
	defaultBreakerFailThreshold = 5
	defaultBreakerPauseDuration = 10 * time.Minute
)

type Breaker struct {
	mu            sync.Mutex
	failures      []time.Time
	pauseUntil    time.Time
	lastRun       time.Time
	windowSize    time.Duration
	failThreshold int
	pauseDur      time.Duration
	logger        *slog.Logger
	now           func() time.Time
	statePath     string
}

type BreakerConfig struct {
	WindowSize    time.Duration
	FailThreshold int
	PauseDuration time.Duration
	StatePath     string
	Now           func() time.Time
}

type BreakerStatus struct {
	Open        bool      `json:"open"`
	PauseUntil  time.Time `json:"pause_until,omitempty"`
	RecentFails int       `json:"recent_fails"`
	Threshold   int       `json:"threshold"`
}

type breakerState struct {
	Failures   []time.Time `json:"failures,omitempty"`
	PauseUntil time.Time   `json:"pause_until,omitempty"`
	LastRun    time.Time   `json:"last_run,omitempty"`
}

func NewBreaker(logger *slog.Logger, cfg BreakerConfig) *Breaker {
	if logger == nil {
		logger = slog.Default()
	}
	b := &Breaker{
		windowSize:    cfg.WindowSize,
		failThreshold: cfg.FailThreshold,
		pauseDur:      cfg.PauseDuration,
		logger:        logger,
		now:           cfg.Now,
		statePath:     cfg.StatePath,
	}
	if b.windowSize <= 0 {
		b.windowSize = defaultBreakerWindowSize
	}
	if b.failThreshold <= 0 {
		b.failThreshold = defaultBreakerFailThreshold
	}
	if b.pauseDur <= 0 {
		b.pauseDur = defaultBreakerPauseDuration
	}
	if b.now == nil {
		b.now = time.Now
	}
	if err := b.loadState(); err != nil {
		b.logger.Warn("llm lesson: breaker state load failed", "error", err)
	}
	return b
}

func (b *Breaker) Allow() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(b.currentTime())
	return b.pauseUntil.IsZero()
}

func (b *Breaker) Record(err error) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.currentTime()
	b.lastRun = now
	b.pruneLocked(now)
	if err == nil {
		b.failures = nil
		b.pauseUntil = time.Time{}
		b.saveStateLocked()
		return
	}

	b.failures = append(b.failures, now)
	wasOpen := !b.pauseUntil.IsZero() && now.Before(b.pauseUntil)
	if len(b.failures) >= b.failThreshold {
		b.pauseUntil = now.Add(b.pauseDur)
		if !wasOpen {
			b.logger.Warn("llm lesson: breaker open",
				"pause_until", b.pauseUntil,
				"recent_fails", len(b.failures),
				"threshold", b.failThreshold,
			)
		}
	}
	b.saveStateLocked()
}

func (b *Breaker) Status() BreakerStatus {
	if b == nil {
		return BreakerStatus{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(b.currentTime())
	status := BreakerStatus{
		Open:        !b.pauseUntil.IsZero(),
		PauseUntil:  b.pauseUntil,
		RecentFails: len(b.failures),
		Threshold:   b.failThreshold,
	}
	if !status.Open {
		status.PauseUntil = time.Time{}
	}
	return status
}

func (b *Breaker) LastRun() time.Time {
	if b == nil {
		return time.Time{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastRun
}

func (b *Breaker) currentTime() time.Time {
	if b == nil || b.now == nil {
		return time.Now().UTC()
	}
	return b.now().UTC()
}

func (b *Breaker) pruneLocked(now time.Time) {
	cutoff := now.Add(-b.windowSize)
	if len(b.failures) > 0 {
		kept := make([]time.Time, 0, len(b.failures))
		for _, failure := range b.failures {
			failure = failure.UTC()
			if !failure.Before(cutoff) {
				kept = append(kept, failure)
			}
		}
		b.failures = kept
	}
	if !b.pauseUntil.IsZero() && !now.Before(b.pauseUntil) {
		b.pauseUntil = time.Time{}
	}
}

func (b *Breaker) loadState() error {
	if b == nil || b.statePath == "" {
		return nil
	}
	data, err := os.ReadFile(b.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read breaker state: %w", err)
	}
	var state breakerState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode breaker state: %w", err)
	}
	b.failures = append([]time.Time(nil), state.Failures...)
	b.pauseUntil = state.PauseUntil.UTC()
	b.lastRun = state.LastRun.UTC()
	return nil
}

func (b *Breaker) saveStateLocked() {
	if b == nil || b.statePath == "" {
		return
	}
	state := breakerState{
		Failures:   append([]time.Time(nil), b.failures...),
		PauseUntil: b.pauseUntil,
		LastRun:    b.lastRun,
	}
	data, err := json.Marshal(state)
	if err != nil {
		b.logger.Warn("llm lesson: breaker state encode failed", "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(b.statePath), 0o755); err != nil {
		b.logger.Warn("llm lesson: breaker state mkdir failed", "error", err)
		return
	}
	if err := os.WriteFile(b.statePath, data, 0o600); err != nil {
		b.logger.Warn("llm lesson: breaker state write failed", "error", err)
	}
}
