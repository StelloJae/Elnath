package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/llm/promptcache"
)

func writePromptCacheEvents(t *testing.T, dir, sessionID string, events []promptCacheEvent) string {
	t.Helper()
	path := filepath.Join(dir, "prompt-cache", sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	return path
}

func TestLoadPromptCacheEvents_MissingFileReturnsEmpty(t *testing.T) {
	got, err := loadPromptCacheEvents(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatalf("loadPromptCacheEvents err = %v, want nil for missing file", err)
	}
	if len(got) != 0 {
		t.Errorf("events = %v, want empty", got)
	}
}

func TestLoadPromptCacheEvents_ParsesValidJSONL(t *testing.T) {
	events := []promptCacheEvent{
		{Turn: 1, Timestamp: time.Now().UTC(), Model: "claude-opus-4-7[1m]", Report: &promptcache.BreakReport{ReadTokens: 3200}},
		{Turn: 2, Timestamp: time.Now().UTC(), Model: "claude-opus-4-7[1m]", Report: &promptcache.BreakReport{Happened: true, CreationTokens: 3400, Reasons: []promptcache.BreakDetail{{Reason: promptcache.ReasonSystemPrompt, Detail: "len 30→48"}}}},
	}
	path := writePromptCacheEvents(t, t.TempDir(), "sess-1", events)
	got, err := loadPromptCacheEvents(path)
	if err != nil {
		t.Fatalf("loadPromptCacheEvents err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("events len = %d, want 2", len(got))
	}
	if got[1].Report.CreationTokens != 3400 {
		t.Errorf("turn2 creation = %d, want 3400", got[1].Report.CreationTokens)
	}
	if got[1].Report.Reasons[0].Reason != promptcache.ReasonSystemPrompt {
		t.Errorf("turn2 reason = %v, want system_prompt", got[1].Report.Reasons[0].Reason)
	}
}

func TestLoadPromptCacheEvents_MalformedLineErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prompt-cache", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadPromptCacheEvents(path); err == nil {
		t.Fatal("err = nil, want malformed-json failure")
	}
}

func TestRenderPromptCacheReport_EmptyMentionsWriter(t *testing.T) {
	var buf bytes.Buffer
	renderPromptCacheReport(&buf, "sess-1", "/tmp/whatever.jsonl", nil)
	out := buf.String()
	if !strings.Contains(out, "No events recorded") {
		t.Errorf("output missing 'No events recorded' hint:\n%s", out)
	}
	if !strings.Contains(out, "Phase 8.1.8") {
		t.Errorf("output missing Phase 8.1.8 follow-up note:\n%s", out)
	}
}

func TestRenderPromptCacheReport_TotalsMatchVerdictMix(t *testing.T) {
	events := []promptCacheEvent{
		{Turn: 1, Timestamp: time.Unix(1_700_000_000, 0), Model: "claude-opus-4-7[1m]", Report: &promptcache.BreakReport{ReadTokens: 3200}},                                                                                            // hit
		{Turn: 2, Timestamp: time.Unix(1_700_000_060, 0), Model: "claude-opus-4-7[1m]", Report: &promptcache.BreakReport{CreationTokens: 800, BelowThreshold: true}},                                                                    // below
		{Turn: 3, Timestamp: time.Unix(1_700_000_120, 0), Model: "claude-opus-4-7[1m]", Report: &promptcache.BreakReport{Happened: true, CreationTokens: 3400, Reasons: []promptcache.BreakDetail{{Reason: promptcache.ReasonSystemPrompt}}}}, // miss
	}
	var buf bytes.Buffer
	renderPromptCacheReport(&buf, "sess-abc", "/tmp/whatever.jsonl", events)
	out := buf.String()
	if !strings.Contains(out, "Totals: 1 hits / 1 misses / 1 below-threshold") {
		t.Errorf("totals line missing or wrong:\n%s", out)
	}
	if !strings.Contains(out, "system_prompt") {
		t.Errorf("miss reason not rendered:\n%s", out)
	}
}

func TestDebugPromptCache_FlagParsing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"no session", []string{}, "--session=<id> is required"},
		{"unknown flag", []string{"--session=x", "--nope"}, "unknown flag"},
		{"bare --session without value", []string{"--session"}, "--session requires a value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := debugPromptCache(tc.args)
			if err == nil {
				t.Fatalf("err = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestDebugPromptCache_HelpExits(t *testing.T) {
	for _, flag := range []string{"-h", "--help", "help"} {
		if err := debugPromptCache([]string{flag}); err != nil {
			t.Errorf("debugPromptCache(%q) = %v, want nil", flag, err)
		}
	}
}

func TestClassifyVerdict(t *testing.T) {
	cases := []struct {
		report *promptcache.BreakReport
		want   string
	}{
		{nil, "n/a"},
		{&promptcache.BreakReport{Happened: true}, "miss"},
		{&promptcache.BreakReport{BelowThreshold: true}, "below"},
		{&promptcache.BreakReport{ReadTokens: 3200}, "hit"},
	}
	for _, c := range cases {
		if got := classifyVerdict(c.report); got != c.want {
			t.Errorf("classifyVerdict(%+v) = %q, want %q", c.report, got, c.want)
		}
	}
}

func TestFormatReasons(t *testing.T) {
	got := formatReasons([]promptcache.BreakDetail{
		{Reason: promptcache.ReasonSystemPrompt, Detail: "len 10→20"},
		{Reason: promptcache.ReasonServerSide},
	})
	if !strings.Contains(got, "system_prompt(len 10→20)") {
		t.Errorf("formatReasons missing detail-formatted reason: %q", got)
	}
	if !strings.Contains(got, "server_side") {
		t.Errorf("formatReasons missing bare reason: %q", got)
	}
	if formatReasons(nil) != "" {
		t.Errorf("formatReasons(nil) = %q, want empty", formatReasons(nil))
	}
}

func TestTruncateModel(t *testing.T) {
	if got := truncateModel("short"); got != "short" {
		t.Errorf("short model changed: %q", got)
	}
	long := "claude-opus-4-7[1m]"
	got := truncateModel(long)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncateModel did not append ellipsis: %q", got)
	}
	if !strings.HasPrefix(got, "claude-op") {
		t.Errorf("truncateModel prefix wrong: %q", got)
	}
}
