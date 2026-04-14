package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNewTrailCreatesFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "audit.jsonl")
	trail, err := NewTrail(path)
	if err != nil {
		t.Fatalf("NewTrail() error = %v", err)
	}
	t.Cleanup(func() {
		_ = trail.Close()
	})

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("os.Stat(%q) error = %v", path, err)
	}
}

func TestTrailLog(t *testing.T) {
	t.Parallel()

	preset := time.Date(2026, time.April, 13, 10, 20, 30, 0, time.UTC)
	tests := []struct {
		name    string
		events  []Event
		wantLen int
		check   func(t *testing.T, got []Event)
	}{
		{
			name:    "single event adds timestamp",
			events:  []Event{{Type: EventSecretDetected, ToolName: "bash"}},
			wantLen: 1,
			check: func(t *testing.T, got []Event) {
				t.Helper()
				if got[0].Type != EventSecretDetected {
					t.Fatalf("Type = %q, want %q", got[0].Type, EventSecretDetected)
				}
				if got[0].Timestamp.IsZero() {
					t.Fatal("Timestamp is zero, want auto-set value")
				}
			},
		},
		{
			name: "two events write two lines",
			events: []Event{
				{Type: EventSecretDetected, ToolName: "read"},
				{Type: EventSecretRedacted, ToolName: "bash"},
			},
			wantLen: 2,
			check: func(t *testing.T, got []Event) {
				t.Helper()
				if got[1].Type != EventSecretRedacted {
					t.Fatalf("second Type = %q, want %q", got[1].Type, EventSecretRedacted)
				}
			},
		},
		{
			name:    "preset timestamp preserved",
			events:  []Event{{Timestamp: preset, Type: EventPermissionGranted, SessionID: "sess-1"}},
			wantLen: 1,
			check: func(t *testing.T, got []Event) {
				t.Helper()
				if !got[0].Timestamp.Equal(preset) {
					t.Fatalf("Timestamp = %v, want %v", got[0].Timestamp, preset)
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit.jsonl")
			trail, err := NewTrail(path)
			if err != nil {
				t.Fatalf("NewTrail() error = %v", err)
			}

			for _, event := range tc.events {
				if err := trail.Log(event); err != nil {
					t.Fatalf("Log() error = %v", err)
				}
			}
			if err := trail.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			got := readAuditEvents(t, path)
			if len(got) != tc.wantLen {
				t.Fatalf("len(events) = %d, want %d", len(got), tc.wantLen)
			}
			tc.check(t, got)
		})
	}
}

func TestTrailLogAfterCloseReturnsError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "audit.jsonl")
	trail, err := NewTrail(path)
	if err != nil {
		t.Fatalf("NewTrail() error = %v", err)
	}
	if err := trail.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := trail.Log(Event{Type: EventSecretRedacted}); err == nil {
		t.Fatal("Log() error = nil, want closed file error")
	}
}

func TestTrailConcurrent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "audit.jsonl")
	trail, err := NewTrail(path)
	if err != nil {
		t.Fatalf("NewTrail() error = %v", err)
	}

	const goroutines = 10
	const perGoroutine = 10

	var wg sync.WaitGroup
	for i := range goroutines {
		for j := range perGoroutine {
			wg.Add(1)
			go func(i, j int) {
				defer wg.Done()
				event := Event{
					Type:      EventSecretDetected,
					SessionID: "sess",
					Detail:    time.Date(2026, time.April, 13, i, j, 0, 0, time.UTC).Format(time.RFC3339Nano),
				}
				if err := trail.Log(event); err != nil {
					t.Errorf("Log() error = %v", err)
				}
			}(i, j)
		}
	}
	wg.Wait()

	if err := trail.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	got := readAuditEvents(t, path)
	if len(got) != goroutines*perGoroutine {
		t.Fatalf("len(events) = %d, want %d", len(got), goroutines*perGoroutine)
	}
}

func TestTrailNilAndZeroValueAreNoOps(t *testing.T) {
	t.Parallel()

	var nilTrail *Trail
	if err := nilTrail.Log(Event{Type: EventSecretDetected}); err != nil {
		t.Fatalf("nil Trail Log() error = %v, want nil", err)
	}
	if err := nilTrail.Close(); err != nil {
		t.Fatalf("nil Trail Close() error = %v, want nil", err)
	}

	var zero Trail
	if err := zero.Log(Event{Type: EventSecretDetected}); err != nil {
		t.Fatalf("zero Trail Log() error = %v, want nil", err)
	}
	if err := zero.Close(); err != nil {
		t.Fatalf("zero Trail Close() error = %v, want nil", err)
	}
}

func readAuditEvents(t *testing.T, path string) []Event {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("os.Open(%q) error = %v", path, err)
	}
	defer file.Close()

	var events []Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner.Err() = %v", err)
	}
	return events
}
