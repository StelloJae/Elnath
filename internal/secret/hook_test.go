package secret

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/audit"
	"github.com/stello/elnath/internal/tools"
)

func TestSecretScanHookPreToolUseAlwaysAllows(t *testing.T) {
	t.Parallel()

	hook := NewSecretScanHook(NewDetector(), nil)
	result, err := hook.PreToolUse(context.Background(), "bash", nil)
	if err != nil {
		t.Fatalf("PreToolUse() error = %v", err)
	}
	if result.Action != agent.HookAllow {
		t.Fatalf("Action = %v, want %v", result.Action, agent.HookAllow)
	}
}

func TestSecretScanHookPostToolUse(t *testing.T) {
	t.Parallel()

	secret := "sk-ant-api03-" + strings.Repeat("a", 80)
	tests := []struct {
		name     string
		trail    *audit.Trail
		result   *tools.Result
		want     string
		wantSame bool
	}{
		{
			name:   "redacts secret output",
			trail:  nil,
			result: &tools.Result{Output: "token=" + secret},
			want:   "token=[REDACTED:anthropic-api-key]",
		},
		{
			name:     "keeps safe output",
			trail:    nil,
			result:   &tools.Result{Output: "safe output"},
			want:     "safe output",
			wantSame: true,
		},
		{
			name:     "nil result is ignored",
			trail:    nil,
			result:   nil,
			wantSame: true,
		},
		{
			name:     "empty output is ignored",
			trail:    nil,
			result:   &tools.Result{},
			want:     "",
			wantSame: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hook := NewSecretScanHook(NewDetector(), tc.trail)
			var before string
			if tc.result != nil {
				before = tc.result.Output
			}
			if err := hook.PostToolUse(context.Background(), "bash", nil, tc.result); err != nil {
				t.Fatalf("PostToolUse() error = %v", err)
			}
			if tc.result == nil {
				return
			}
			if tc.result.Output != tc.want {
				t.Fatalf("Output = %q, want %q", tc.result.Output, tc.want)
			}
			if tc.wantSame && tc.result.Output != before {
				t.Fatalf("Output changed from %q to %q", before, tc.result.Output)
			}
		})
	}
}

func TestSecretScanHookPostToolUseWritesAuditEvent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "audit.jsonl")
	trail, err := audit.NewTrail(path)
	if err != nil {
		t.Fatalf("NewTrail() error = %v", err)
	}
	defer trail.Close()

	hook := NewSecretScanHook(NewDetector(), trail)
	result := &tools.Result{Output: "token=sk-ant-api03-" + strings.Repeat("a", 80)}
	if err := hook.PostToolUse(context.Background(), "bash", nil, result); err != nil {
		t.Fatalf("PostToolUse() error = %v", err)
	}
	if !strings.Contains(result.Output, "[REDACTED:anthropic-api-key]") {
		t.Fatalf("Output = %q, want redacted token", result.Output)
	}

	if err := trail.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	events := readAuditRecords(t, path)
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Type != audit.EventSecretRedacted {
		t.Fatalf("Type = %q, want %q", events[0].Type, audit.EventSecretRedacted)
	}
	if events[0].ToolName != "bash" {
		t.Fatalf("ToolName = %q, want bash", events[0].ToolName)
	}
	if events[0].RuleID != "anthropic-api-key" {
		t.Fatalf("RuleID = %q, want anthropic-api-key", events[0].RuleID)
	}
	if !strings.Contains(events[0].Detail, "redacted ") {
		t.Fatalf("Detail = %q, want redacted detail", events[0].Detail)
	}
}

func readAuditRecords(t *testing.T, path string) []audit.Event {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("os.Open(%q) error = %v", path, err)
	}
	defer file.Close()

	var events []audit.Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event audit.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner.Err() error = %v", err)
	}
	return events
}
