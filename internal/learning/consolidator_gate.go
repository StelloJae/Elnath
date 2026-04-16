package learning

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Gate controls when lesson consolidation may run. It guards three failure
// modes: running too often (time gate), running without enough new material
// (session gate), and two processes consolidating at once (mtime-CAS lock).
//
// The lock file's mtime doubles as "last consolidated at": writing the file
// updates mtime, so a single stat yields both presence and timestamp.
//
// Adapted from Claude Code's autoDream
// (src/services/autoDream/consolidationLock.ts).
type Gate struct {
	lockPath     string
	minInterval  time.Duration
	minSessions  int
	holderStale  time.Duration
	now          func() time.Time
	pid          func() int
	pidAlive     func(pid int) bool
	sessionCount func(since time.Time) (int, error)
}

type GateOption func(*Gate)

func WithMinInterval(d time.Duration) GateOption {
	return func(g *Gate) { g.minInterval = d }
}

func WithMinSessions(n int) GateOption {
	return func(g *Gate) { g.minSessions = n }
}

// WithHolderStale sets the threshold past which a lock is reclaimed regardless
// of PID liveness. Guards against PID reuse after a hung holder.
func WithHolderStale(d time.Duration) GateOption {
	return func(g *Gate) { g.holderStale = d }
}

func WithClock(now func() time.Time) GateOption {
	return func(g *Gate) { g.now = now }
}

func WithPID(pid func() int) GateOption {
	return func(g *Gate) { g.pid = pid }
}

func WithPIDAlive(alive func(pid int) bool) GateOption {
	return func(g *Gate) { g.pidAlive = alive }
}

// WithSessionCount injects a function returning the number of sessions touched
// since the given instant. Default returns zero so the session gate always
// blocks until a real counter is wired.
func WithSessionCount(fn func(since time.Time) (int, error)) GateOption {
	return func(g *Gate) { g.sessionCount = fn }
}

func NewGate(lockPath string, opts ...GateOption) *Gate {
	g := &Gate{
		lockPath:     lockPath,
		minInterval:  24 * time.Hour,
		minSessions:  5,
		holderStale:  60 * time.Minute,
		now:          time.Now,
		pid:          os.Getpid,
		pidAlive:     isProcessAlive,
		sessionCount: func(time.Time) (int, error) { return 0, nil },
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// LastConsolidatedAt returns the mtime of the lock file, or the zero time if
// the lock does not yet exist.
func (g *Gate) LastConsolidatedAt() (time.Time, error) {
	fi, err := os.Stat(g.lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("consolidation gate: stat: %w", err)
	}
	return fi.ModTime(), nil
}

// ShouldRun reports whether the time and session gates both pass. It does not
// check the lock — that is Acquire's job.
func (g *Gate) ShouldRun(now time.Time) (bool, string) {
	lastAt, err := g.LastConsolidatedAt()
	if err != nil {
		return false, fmt.Sprintf("last-consolidated read failed: %v", err)
	}
	if !lastAt.IsZero() {
		since := now.Sub(lastAt)
		if since < g.minInterval {
			return false, fmt.Sprintf("time-gate: %s since last (need %s)", since.Truncate(time.Minute), g.minInterval)
		}
	}
	count, err := g.sessionCount(lastAt)
	if err != nil {
		return false, fmt.Sprintf("session-count failed: %v", err)
	}
	if count < g.minSessions {
		return false, fmt.Sprintf("session-gate: %d sessions since last (need %d)", count, g.minSessions)
	}
	return true, "ready"
}

// Acquire attempts to take the consolidation lock.
//
// On success, the returned release func rolls mtime back to the pre-acquire
// state. Call it only if the consolidation work failed. On success, discard
// the func so the new mtime becomes the next lastConsolidatedAt.
//
// Returns an error if another process holds the lock, or if filesystem
// operations fail.
func (g *Gate) Acquire() (func(), error) {
	path := g.lockPath
	now := g.now()

	var priorMtime time.Time
	if fi, statErr := os.Stat(path); statErr == nil {
		priorMtime = fi.ModTime()
		stale := now.Sub(priorMtime) >= g.holderStale
		if !stale {
			if !g.holderDead(path) {
				return nil, fmt.Errorf("consolidation lock: held (mtime=%s)", priorMtime.Format(time.RFC3339))
			}
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("consolidation lock: remove prior: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("consolidation lock: stat: %w", statErr)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("consolidation lock: mkdir: %w", err)
	}

	// O_EXCL gives atomic single-winner semantics on concurrent create.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("consolidation lock: lost race")
		}
		return nil, fmt.Errorf("consolidation lock: create: %w", err)
	}
	if _, err := f.WriteString(strconv.Itoa(g.pid())); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("consolidation lock: write: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("consolidation lock: close: %w", err)
	}

	release := func() {
		if priorMtime.IsZero() {
			_ = os.Remove(path)
			return
		}
		// Rewind mtime so the next time-gate sees the previous lastConsolidatedAt.
		_ = os.WriteFile(path, nil, 0o600)
		_ = os.Chtimes(path, priorMtime, priorMtime)
	}
	return release, nil
}

// holderDead reports whether the PID recorded in the lock file is not
// running. Unreadable or unparseable bodies are treated as alive (conservative
// hold) because the common case for an empty body with a fresh mtime is
// another process mid-acquire between OpenFile and WriteString. Once mtime
// crosses holderStale, Acquire reclaims regardless of body.
func (g *Gate) holderDead(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
	if perr != nil || pid <= 0 {
		return false
	}
	return !g.pidAlive(pid)
}

// isProcessAlive reports whether a process with the given PID is running, by
// sending signal 0 (existence check on POSIX). EPERM counts as alive because
// the process exists — just not ours to signal.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == syscall.EPERM {
		return true
	}
	return false
}
