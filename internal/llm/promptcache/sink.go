package promptcache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event is the per-call snapshot a provider emits after each successful
// API call. It mirrors the cmd/elnath/cmd_debug_promptcache.go
// promptCacheEvent shape so the debug CLI's reader and this package's
// writer stay format-compatible.
type Event struct {
	Turn      int          `json:"turn"`
	Timestamp time.Time    `json:"ts"`
	Model     string       `json:"model"`
	Report    *BreakReport `json:"report"`
}

// EventSink is the contract a provider uses to record prompt-cache
// outcomes. Implementations decide the durability policy (file, stderr,
// in-memory test double). Nil sinks are tolerated by providers as a
// no-op; callers that want telemetry must wire a concrete sink.
type EventSink interface {
	Record(ctx context.Context, sessionID string, event Event) error
}

// NoopSink discards every event. Useful as a zero-value when a provider
// is configured without explicit telemetry intent but the Stream path
// still expects a non-nil sink reference.
type NoopSink struct{}

// Record implements EventSink. Always returns nil without side effects.
func (NoopSink) Record(context.Context, string, Event) error { return nil }

// FileSink persists one Event per line to
// <DataDir>/prompt-cache/<sessionID>.jsonl. Per-session file selection
// mirrors the `elnath debug prompt-cache --session=<id>` reader path.
// Turn numbering is assigned by the sink itself so callers do not have
// to thread per-session counters through their stack.
type FileSink struct {
	DataDir string

	mu    sync.Mutex
	turns map[string]int // sessionID → next turn counter
}

// NewFileSink returns a FileSink rooted at dataDir. The sink creates the
// prompt-cache/ subdirectory lazily on first write; passing a
// non-existent dataDir does not error at construction time.
func NewFileSink(dataDir string) *FileSink {
	return &FileSink{DataDir: dataDir, turns: make(map[string]int)}
}

// Record appends one JSON line to the session's cache file. Empty
// sessionID is treated as "skip" to keep the Stream path safe for
// callers that do not thread a session id. Turn auto-assignment
// preserves caller-supplied values (ev.Turn != 0) so tests can pin
// specific turn numbers.
func (s *FileSink) Record(ctx context.Context, sessionID string, ev Event) error {
	if sessionID == "" {
		return nil
	}
	_ = ctx // ctx is accepted for cancellation parity with richer sinks

	s.mu.Lock()
	if ev.Turn == 0 {
		s.turns[sessionID]++
		ev.Turn = s.turns[sessionID]
	} else if ev.Turn > s.turns[sessionID] {
		s.turns[sessionID] = ev.Turn
	}
	s.mu.Unlock()

	path := filepath.Join(s.DataDir, "prompt-cache", sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("promptcache: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("promptcache: open %s: %w", path, err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(ev); err != nil {
		return fmt.Errorf("promptcache: encode: %w", err)
	}
	return nil
}
