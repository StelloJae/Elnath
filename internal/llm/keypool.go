package llm

import (
	"fmt"
	"sync"
	"time"
)

// cooldown durations by error class.
const (
	cooldownRateLimit  = 1 * time.Hour
	cooldownQuotaLimit = 24 * time.Hour
	cooldownServerErr  = 5 * time.Minute
)

type keyEntry struct {
	Key       string
	CoolUntil time.Time
	Reason    string
}

// KeyPool manages a pool of API keys for a single provider, applying per-key
// cooldowns when rate-limit or server errors are encountered.
type KeyPool struct {
	mu      sync.Mutex
	keys    []keyEntry
	current int
}

// NewKeyPool constructs a KeyPool from a list of API keys.
// Duplicate and empty keys are silently dropped.
func NewKeyPool(keys []string) *KeyPool {
	seen := make(map[string]struct{}, len(keys))
	entries := make([]keyEntry, 0, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		entries = append(entries, keyEntry{Key: k})
	}
	return &KeyPool{keys: entries}
}

// Next returns the next available (non-cooled-down) key via round-robin.
// Returns an error if all keys are currently on cooldown.
func (p *KeyPool) Next() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.keys)
	if n == 0 {
		return "", fmt.Errorf("keypool: no keys configured")
	}

	now := time.Now()
	for i := 0; i < n; i++ {
		idx := (p.current + i) % n
		e := &p.keys[idx]
		if now.After(e.CoolUntil) {
			p.current = (idx + 1) % n
			return e.Key, nil
		}
	}
	return "", fmt.Errorf("keypool: all %d keys are on cooldown", n)
}

// ReportError applies a cooldown to the key based on the HTTP status code.
//   - 429 → 1 hour  (rate limit)
//   - 402 → 24 hours (quota / payment required)
//   - 5xx → 5 minutes (server error)
//
// Other status codes are ignored.
func (p *KeyPool) ReportError(key string, statusCode int) {
	var d time.Duration
	var reason string

	switch {
	case statusCode == 429:
		d = cooldownRateLimit
		reason = "rate_limit"
	case statusCode == 402:
		d = cooldownQuotaLimit
		reason = "quota_exceeded"
	case statusCode >= 500 && statusCode < 600:
		d = cooldownServerErr
		reason = fmt.Sprintf("server_error_%d", statusCode)
	default:
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.keys {
		e := &p.keys[i]
		if e.Key == key {
			until := time.Now().Add(d)
			if until.After(e.CoolUntil) {
				e.CoolUntil = until
				e.Reason = reason
			}
			return
		}
	}
}

// MarkError is an alias for ReportError kept for backwards compatibility.
func (p *KeyPool) MarkError(key string, statusCode int) { p.ReportError(key, statusCode) }

// ActiveCount returns the number of keys not currently on cooldown.
func (p *KeyPool) ActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	count := 0
	for i := range p.keys {
		if now.After(p.keys[i].CoolUntil) {
			count++
		}
	}
	return count
}

// Available is an alias for ActiveCount kept for backwards compatibility.
func (p *KeyPool) Available() int { return p.ActiveCount() }

// Len returns the total number of keys in the pool (including cooled-down ones).
func (p *KeyPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.keys)
}
