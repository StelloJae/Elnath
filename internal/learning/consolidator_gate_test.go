package learning

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestGate(t *testing.T, opts ...GateOption) (*Gate, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".consolidate-lock")
	baseline := []GateOption{
		WithMinInterval(time.Hour),
		WithMinSessions(3),
		WithHolderStale(10 * time.Minute),
		WithPID(func() int { return 99_999 }),
		WithPIDAlive(func(int) bool { return false }),
		WithSessionCount(func(time.Time) (int, error) { return 10, nil }),
	}
	return NewGate(path, append(baseline, opts...)...), path
}

func seedLock(t *testing.T, path string, mtime time.Time, pid int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGateShouldRun(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name       string
		seed       func(t *testing.T, path string)
		opts       []GateOption
		want       bool
		wantReason string
	}{
		{
			name: "no lock, enough sessions -> ready",
			seed: func(*testing.T, string) {},
			want: true,
		},
		{
			name:       "recent lock blocks on time-gate",
			seed:       func(t *testing.T, p string) { seedLock(t, p, now.Add(-30*time.Minute), 1234) },
			want:       false,
			wantReason: "time-gate",
		},
		{
			name:       "old lock, sessions insufficient -> session-gate",
			seed:       func(t *testing.T, p string) { seedLock(t, p, now.Add(-2*time.Hour), 1234) },
			opts:       []GateOption{WithSessionCount(func(time.Time) (int, error) { return 1, nil })},
			want:       false,
			wantReason: "session-gate",
		},
		{
			name: "old lock, enough sessions -> ready",
			seed: func(t *testing.T, p string) { seedLock(t, p, now.Add(-2*time.Hour), 1234) },
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate, path := newTestGate(t, tc.opts...)
			tc.seed(t, path)
			got, reason := gate.ShouldRun(now)
			if got != tc.want {
				t.Fatalf("ShouldRun=%v (reason=%q), want %v", got, reason, tc.want)
			}
			if tc.wantReason != "" && !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reason=%q, want substring %q", reason, tc.wantReason)
			}
		})
	}
}

func TestGateAcquireNoPrior(t *testing.T) {
	gate, path := newTestGate(t, WithPID(func() int { return 42 }))
	release, err := gate.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "42" {
		t.Errorf("PID in lock = %q, want %q", got, "42")
	}
	release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("release did not remove lock (stat err=%v)", err)
	}
}

func TestGateAcquireBlockedByLiveHolder(t *testing.T) {
	now := time.Now()
	gate, path := newTestGate(t,
		WithClock(func() time.Time { return now }),
		WithPIDAlive(func(pid int) bool { return pid == 1234 }),
	)
	seedLock(t, path, now.Add(-30*time.Second), 1234)
	if _, err := gate.Acquire(); err == nil {
		t.Fatal("expected Acquire to block, got nil")
	}
}

func TestGateAcquireReclaimsDeadHolder(t *testing.T) {
	now := time.Now()
	gate, path := newTestGate(t,
		WithClock(func() time.Time { return now }),
		WithPID(func() int { return 42 }),
		WithPIDAlive(func(int) bool { return false }),
	)
	seedLock(t, path, now.Add(-30*time.Second), 1234)
	release, err := gate.Acquire()
	if err != nil {
		t.Fatalf("reclaim failed: %v", err)
	}
	data, _ := os.ReadFile(path)
	if got := strings.TrimSpace(string(data)); got != "42" {
		t.Errorf("after reclaim PID = %q, want %q", got, "42")
	}
	release()
}

func TestGateAcquireReclaimsStuckLock(t *testing.T) {
	now := time.Now()
	gate, path := newTestGate(t,
		WithClock(func() time.Time { return now }),
		WithHolderStale(10*time.Minute),
		WithPID(func() int { return 42 }),
		WithPIDAlive(func(int) bool { return true }),
	)
	seedLock(t, path, now.Add(-20*time.Minute), 1234)
	release, err := gate.Acquire()
	if err != nil {
		t.Fatalf("stuck-lock reclaim failed: %v", err)
	}
	data, _ := os.ReadFile(path)
	if got := strings.TrimSpace(string(data)); got != "42" {
		t.Errorf("after stuck-lock reclaim PID = %q, want %q", got, "42")
	}
	release()
}

func TestGateAcquireConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".consolidate-lock")

	const goroutines = 8
	var wg sync.WaitGroup
	var successes atomic.Int32

	for i := 0; i < goroutines; i++ {
		pid := 1000 + i
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate := NewGate(path,
				WithHolderStale(10*time.Minute),
				WithPID(func() int { return pid }),
				WithPIDAlive(func(int) bool { return true }),
				WithSessionCount(func(time.Time) (int, error) { return 10, nil }),
			)
			if _, err := gate.Acquire(); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Errorf("expected exactly 1 successful acquire, got %d", got)
	}
}

func TestGateAcquireReleaseRestoresPriorMtime(t *testing.T) {
	prior := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	gate, path := newTestGate(t,
		WithPID(func() int { return 42 }),
		WithPIDAlive(func(int) bool { return false }),
	)
	seedLock(t, path, prior, 1234)
	release, err := gate.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	release()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	diff := fi.ModTime().Sub(prior)
	if diff > time.Second || diff < -time.Second {
		t.Errorf("mtime=%s after release, want ~%s (diff=%s)", fi.ModTime(), prior, diff)
	}
}

func TestGateAcquireReleaseNoPriorRemovesFile(t *testing.T) {
	gate, path := newTestGate(t, WithPID(func() int { return 42 }))
	release, err := gate.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected lock file absent after release, got err=%v", err)
	}
}
