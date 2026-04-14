package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/self"
)

func TestLessonsList(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "help", run: runLessonsHelp},
		{name: "human", run: runLessonsListHuman},
		{name: "json", run: runLessonsListJSONFlag},
		{name: "json empty", run: runLessonsListJSONEmpty},
		{name: "topic filter", run: runLessonsListTopicFilter},
		{name: "source flag", run: runLessonsListSourceFlag},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestLessonsShow(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "unique", run: runLessonsShowUnique},
		{name: "ambiguous", run: runLessonsShowAmbiguous},
		{name: "not found", run: runLessonsShowNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestLessonsClear(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "topic dry run", run: runLessonsClearTopicDryRun},
		{name: "topic apply", run: runLessonsClearTopicApply},
		{name: "all without yes", run: runLessonsClearAllWithoutYesErrors},
		{name: "all apply", run: runLessonsClearAllApply},
		{name: "id ambiguous", run: runLessonsClearIDAmbiguous},
		{name: "id no match", run: runLessonsClearIDNoMatch},
		{name: "id no match with topic", run: runLessonsClearIDNoMatchWithTopic},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestLessonsRotate(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "keep last", run: runLessonsRotateKeepLast},
		{name: "max bytes", run: runLessonsRotateMaxBytes},
		{name: "no bound", run: runLessonsRotateNoBound},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestLessonsStats(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "human", run: runLessonsStatsHuman},
		{name: "llm enabled", run: runLessonsStatsLLMEnabled},
		{name: "llm breaker open", run: runLessonsStatsLLMBreakerOpen},
		{name: "json", run: runLessonsStatsJSON},
		{name: "includes by source", run: runLessonsStatsIncludesBySource},
		{name: "json includes by source", run: runLessonsStatsJSONIncludesBySource},
		{name: "large archive", run: runLessonsStatsHandlesLargeArchiveEntry},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func runLessonsListHuman(t *testing.T) {
	_, cfgPath, _, lessons := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"list"}); err != nil {
			t.Fatalf("cmdLessons(list) error = %v", err)
		}
	})

	for _, lesson := range lessons {
		if !strings.Contains(stdout, lesson.ID) {
			t.Fatalf("stdout = %q, want ID %q", stdout, lesson.ID)
		}
		if !strings.Contains(stdout, lesson.Text) {
			t.Fatalf("stdout = %q, want text %q", stdout, lesson.Text)
		}
	}
}

func runLessonsHelp(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "broken-config.yaml")
	if err := os.WriteFile(cfgPath, []byte("data_dir: [broken\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"help"}); err != nil {
			t.Fatalf("cmdLessons(help) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "Usage: elnath lessons") {
		t.Fatalf("stdout = %q, want lessons usage", stdout)
	}
}

func runLessonsListJSONFlag(t *testing.T) {
	_, cfgPath, _, lessons := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"list", "--json"}); err != nil {
			t.Fatalf("cmdLessons(list --json) error = %v", err)
		}
	})

	lines := nonEmptyLines(stdout)
	if len(lines) != len(lessons) {
		t.Fatalf("json line count = %d, want %d", len(lines), len(lessons))
	}
	for _, line := range lines {
		var lesson learning.Lesson
		if err := json.Unmarshal([]byte(line), &lesson); err != nil {
			t.Fatalf("json line %q unmarshal error = %v", line, err)
		}
	}
}

func runLessonsListJSONEmpty(t *testing.T) {
	dataDir := t.TempDir()
	cfgPath := writeLessonsTestConfig(t, dataDir)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"list", "--json"}); err != nil {
			t.Fatalf("cmdLessons(list --json) error = %v", err)
		}
	})
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("stdout = %q, want empty JSONL output", stdout)
	}
}

func runLessonsListTopicFilter(t *testing.T) {
	_, cfgPath, _, _ := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"list", "--topic", "bravo", "--json"}); err != nil {
			t.Fatalf("cmdLessons(list --topic bravo --json) error = %v", err)
		}
	})

	lines := nonEmptyLines(stdout)
	if len(lines) != 1 {
		t.Fatalf("json line count = %d, want 1", len(lines))
	}
	var lesson learning.Lesson
	if err := json.Unmarshal([]byte(lines[0]), &lesson); err != nil {
		t.Fatalf("json line %q unmarshal error = %v", lines[0], err)
	}
	if lesson.Topic != "bravo" {
		t.Fatalf("Topic = %q, want bravo", lesson.Topic)
	}
}

func runLessonsListSourceFlag(t *testing.T) {
	_, cfgPath, store, _ := newLessonsFixture(t)
	appendManualLessons(t, store,
		learning.Lesson{Text: "Ralph lesson found a flaky edge.", Topic: "ops", Source: "agent:ralph", Confidence: "high"},
		learning.Lesson{Text: "Team lesson captured a shared workflow.", Topic: "ops", Source: "agent:team", Confidence: "medium"},
	)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"list", "--source", "agent:ralph", "--json"}); err != nil {
			t.Fatalf("cmdLessons(list --source agent:ralph --json) error = %v", err)
		}
	})

	lines := nonEmptyLines(stdout)
	if len(lines) != 1 {
		t.Fatalf("json line count = %d, want 1", len(lines))
	}
	var lesson learning.Lesson
	if err := json.Unmarshal([]byte(lines[0]), &lesson); err != nil {
		t.Fatalf("json line %q unmarshal error = %v", lines[0], err)
	}
	if lesson.Source != "agent:ralph" {
		t.Fatalf("Source = %q, want agent:ralph", lesson.Source)
	}
}

func runLessonsShowUnique(t *testing.T) {
	_, cfgPath, _, lessons := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"show", lessons[0].ID}); err != nil {
			t.Fatalf("cmdLessons(show) error = %v", err)
		}
	})

	for _, want := range []string{"ID:", lessons[0].ID, "Created:", "Topic:", "Confidence:", "Source:", "Text:", "Persona delta:", "persistence"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want substring %q", stdout, want)
		}
	}
}

func runLessonsShowAmbiguous(t *testing.T) {
	_, cfgPath, store, _ := newLessonsFixture(t)
	appendManualLessons(t, store,
		learning.Lesson{ID: "abcd1111", Text: "Ambiguous lesson one.", Topic: "ambiguous", Source: "research", Confidence: "medium"},
		learning.Lesson{ID: "abcd2222", Text: "Ambiguous lesson two.", Topic: "ambiguous", Source: "research", Confidence: "medium"},
	)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	err := cmdLessons(context.Background(), []string{"show", "abcd"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous prefix") {
		t.Fatalf("cmdLessons(show abcd) err = %v, want ambiguous prefix", err)
	}
}

func runLessonsShowNotFound(t *testing.T) {
	_, cfgPath, _, _ := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	err := cmdLessons(context.Background(), []string{"show", "ffff"})
	if err == nil || !strings.Contains(err.Error(), "no lesson matched") {
		t.Fatalf("cmdLessons(show ffff) err = %v, want no lesson matched", err)
	}
}

func runLessonsClearTopicDryRun(t *testing.T) {
	path, cfgPath, store, _ := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"clear", "--topic", "alpha", "--dry-run"}); err != nil {
			t.Fatalf("cmdLessons(clear --dry-run) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "Would delete 2 lesson(s)") {
		t.Fatalf("stdout = %q, want dry-run count", stdout)
	}
	if got := activeLessonCount(t, store); got != 3 {
		t.Fatalf("active lesson count = %d, want 3", got)
	}
	if got := archiveLessonCount(t, path); got != 0 {
		t.Fatalf("archive lesson count = %d, want 0", got)
	}
}

func runLessonsClearTopicApply(t *testing.T) {
	_, cfgPath, store, _ := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"clear", "-y", "--topic", "alpha"}); err != nil {
			t.Fatalf("cmdLessons(clear --topic alpha) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "Deleted 2 lesson(s).") {
		t.Fatalf("stdout = %q, want delete count", stdout)
	}

	remaining, err := store.List()
	if err != nil {
		t.Fatalf("store.List() error = %v", err)
	}
	if len(remaining) != 1 || remaining[0].Topic != "bravo" {
		t.Fatalf("remaining = %#v, want single bravo lesson", remaining)
	}
}

func runLessonsClearAllWithoutYesErrors(t *testing.T) {
	_, cfgPath, _, _ := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	withNonTTYStdin(t)

	err := cmdLessons(context.Background(), []string{"clear", "--all"})
	if err == nil || !strings.Contains(err.Error(), "without -y") {
		t.Fatalf("cmdLessons(clear --all) err = %v, want no-tty rejection", err)
	}
}

func runLessonsClearAllApply(t *testing.T) {
	path, cfgPath, store, _ := newLessonsFixture(t)
	writeArchiveEntry(t, path, "archive-keep", strings.Repeat("archived-", 8))
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"clear", "--all", "-y"}); err != nil {
			t.Fatalf("cmdLessons(clear --all -y) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "Deleted 3 lesson(s).") {
		t.Fatalf("stdout = %q, want delete count", stdout)
	}
	if got := activeLessonCount(t, store); got != 0 {
		t.Fatalf("active lesson count = %d, want 0", got)
	}
	if got := archiveLessonCount(t, path); got != 1 {
		t.Fatalf("archive lesson count = %d, want 1", got)
	}
}

func runLessonsClearIDAmbiguous(t *testing.T) {
	_, cfgPath, store, _ := newLessonsFixture(t)
	appendManualLessons(t, store,
		learning.Lesson{ID: "abcd1111", Text: "Ambiguous lesson one.", Topic: "ambiguous", Source: "research", Confidence: "medium"},
		learning.Lesson{ID: "abcd2222", Text: "Ambiguous lesson two.", Topic: "ambiguous", Source: "research", Confidence: "medium"},
	)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	err := cmdLessons(context.Background(), []string{"clear", "--id", "abcd"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous prefix") {
		t.Fatalf("cmdLessons(clear --id abcd) err = %v, want ambiguous prefix", err)
	}
}

func runLessonsClearIDNoMatch(t *testing.T) {
	_, cfgPath, store, _ := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"clear", "--id", "deadbeef", "-y"}); err != nil {
			t.Fatalf("cmdLessons(clear --id deadbeef -y) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "Deleted 0 lesson(s).") {
		t.Fatalf("stdout = %q, want zero delete output", stdout)
	}
	if got := activeLessonCount(t, store); got != 3 {
		t.Fatalf("active lesson count = %d, want 3", got)
	}
}

func runLessonsClearIDNoMatchWithTopic(t *testing.T) {
	_, cfgPath, store, _ := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"clear", "--id", "deadbeef", "--topic", "alpha", "-y"}); err != nil {
			t.Fatalf("cmdLessons(clear --id deadbeef --topic alpha -y) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "Deleted 2 lesson(s).") {
		t.Fatalf("stdout = %q, want topic delete count", stdout)
	}
	remaining, err := store.List()
	if err != nil {
		t.Fatalf("store.List() error = %v", err)
	}
	if len(remaining) != 1 || remaining[0].Topic != "bravo" {
		t.Fatalf("remaining = %#v, want single bravo lesson", remaining)
	}
}

func runLessonsRotateKeepLast(t *testing.T) {
	path, cfgPath, store, lessons := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"rotate", "--keep", "1"}); err != nil {
			t.Fatalf("cmdLessons(rotate --keep 1) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "Rotated 2 lesson(s).") {
		t.Fatalf("stdout = %q, want rotate count", stdout)
	}
	for _, want := range []string{path, archivePathForTest(path), "1 entries", "2 entries"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want substring %q", stdout, want)
		}
	}

	active, err := store.List()
	if err != nil {
		t.Fatalf("store.List() error = %v", err)
	}
	if len(active) != 1 || active[0].ID != lessons[2].ID {
		t.Fatalf("active = %#v, want newest lesson %q", active, lessons[2].ID)
	}
	if got := archiveLessonCount(t, path); got != 2 {
		t.Fatalf("archive lesson count = %d, want 2", got)
	}
}

func runLessonsRotateMaxBytes(t *testing.T) {
	path, cfgPath, store, _ := newLessonsFixture(t)
	appendExtraLessons(t, store, 8)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	before := lessonFileSize(t, path)

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"rotate", "--max-bytes", fmt.Sprintf("%d", before/2)}); err != nil {
			t.Fatalf("cmdLessons(rotate --max-bytes) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "Rotated ") {
		t.Fatalf("stdout = %q, want rotate summary", stdout)
	}
	after := lessonFileSize(t, path)
	if after >= before {
		t.Fatalf("active file size = %d, want less than %d", after, before)
	}
}

func runLessonsRotateNoBound(t *testing.T) {
	_, cfgPath, _, _ := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	err := cmdLessons(context.Background(), []string{"rotate"})
	if err == nil || !strings.Contains(err.Error(), "requires --keep or --max-bytes") {
		t.Fatalf("cmdLessons(rotate) err = %v, want bound error", err)
	}
}

func runLessonsStatsHuman(t *testing.T) {
	_, cfgPath, _, _ := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"stats"}); err != nil {
			t.Fatalf("cmdLessons(stats) error = %v", err)
		}
	})
	for _, want := range []string{"Active file:", "Total:", "Range:", "By confidence:", "By topic", "LLM extraction:", "Disabled"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want substring %q", stdout, want)
		}
	}
}

func runLessonsStatsLLMEnabled(t *testing.T) {
	path, _, _, _ := newLessonsFixture(t)
	cfgPath := writeLessonsTestConfigWithLLM(t, filepath.Dir(path), true)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"stats"}); err != nil {
			t.Fatalf("cmdLessons(stats) error = %v", err)
		}
	})
	for _, want := range []string{"LLM extraction:", "Enabled (model=claude-haiku-4-5)", "Breaker: closed"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want substring %q", stdout, want)
		}
	}
}

func runLessonsStatsLLMBreakerOpen(t *testing.T) {
	path, _, _, _ := newLessonsFixture(t)
	dataDir := filepath.Dir(path)
	statePath := filepath.Join(dataDir, "llm_extraction_state.json")
	breaker := learning.NewBreaker(nil, learning.BreakerConfig{StatePath: statePath})
	for i := 0; i < 5; i++ {
		breaker.Record(errors.New("boom"))
	}
	cfgPath := writeLessonsTestConfigWithLLM(t, dataDir, true)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"stats"}); err != nil {
			t.Fatalf("cmdLessons(stats) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "Breaker: OPEN") {
		t.Fatalf("stdout = %q, want open breaker", stdout)
	}
}

func runLessonsStatsJSON(t *testing.T) {
	_, cfgPath, _, _ := newLessonsFixture(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"stats", "--json"}); err != nil {
			t.Fatalf("cmdLessons(stats --json) error = %v", err)
		}
	})

	var payload struct {
		Total        int            `json:"Total"`
		ByTopic      map[string]int `json:"ByTopic"`
		ArchiveBytes int64          `json:"archive_bytes"`
		ArchiveLines int            `json:"archive_lines"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stats json unmarshal error = %v", err)
	}
	if payload.Total != 3 {
		t.Fatalf("Total = %d, want 3", payload.Total)
	}
	if payload.ByTopic["alpha"] != 1 || payload.ByTopic["alpha-ops"] != 1 || payload.ByTopic["bravo"] != 1 {
		t.Fatalf("ByTopic = %#v, want alpha/alpha-ops/bravo counts", payload.ByTopic)
	}
}

func runLessonsStatsIncludesBySource(t *testing.T) {
	_, cfgPath, store, _ := newLessonsFixture(t)
	appendManualLessons(t, store,
		learning.Lesson{Text: "Ralph lesson found a flaky edge.", Topic: "ops", Source: "agent:ralph", Confidence: "high"},
		learning.Lesson{Text: "Team lesson captured a shared workflow.", Topic: "ops", Source: "agent:team", Confidence: "medium"},
	)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"stats"}); err != nil {
			t.Fatalf("cmdLessons(stats) error = %v", err)
		}
	})

	for _, want := range []string{"By source:", "research", "agent:ralph", "agent:team"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want substring %q", stdout, want)
		}
	}
}

func runLessonsStatsJSONIncludesBySource(t *testing.T) {
	_, cfgPath, store, _ := newLessonsFixture(t)
	appendManualLessons(t, store,
		learning.Lesson{Text: "Ralph lesson found a flaky edge.", Topic: "ops", Source: "agent:ralph", Confidence: "high"},
		learning.Lesson{Text: "Team lesson captured a shared workflow.", Topic: "ops", Source: "agent:team", Confidence: "medium"},
	)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"stats", "--json"}); err != nil {
			t.Fatalf("cmdLessons(stats --json) error = %v", err)
		}
	})

	var payload struct {
		BySource map[string]int `json:"by_source"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stats json unmarshal error = %v", err)
	}
	if payload.BySource["research"] != 3 || payload.BySource["agent:ralph"] != 1 || payload.BySource["agent:team"] != 1 {
		t.Fatalf("BySource = %#v, want research=3 agent:ralph=1 agent:team=1", payload.BySource)
	}
}

func runLessonsStatsHandlesLargeArchiveEntry(t *testing.T) {
	path, cfgPath, _, _ := newLessonsFixture(t)
	writeArchiveEntry(t, path, "archive-large", strings.Repeat("payload-", 12000))
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdLessons(context.Background(), []string{"stats"}); err != nil {
			t.Fatalf("cmdLessons(stats) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "Archive:") {
		t.Fatalf("stdout = %q, want archive summary", stdout)
	}
}

func TestParseTimeFlag(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		assert func(t *testing.T, got time.Time, err error)
	}{
		{
			name: "rfc3339",
			raw:  "2026-04-13T12:00:00Z",
			assert: func(t *testing.T, got time.Time, err error) {
				if err != nil {
					t.Fatalf("parseTimeFlag() error = %v", err)
				}
				want := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
				if !got.Equal(want) {
					t.Fatalf("time = %v, want %v", got, want)
				}
			},
		},
		{
			name: "duration hours",
			raw:  "24h",
			assert: func(t *testing.T, got time.Time, err error) {
				assertRelativeTime(t, got, err, 24*time.Hour)
			},
		},
		{
			name: "duration days",
			raw:  "7d",
			assert: func(t *testing.T, got time.Time, err error) {
				assertRelativeTime(t, got, err, 7*24*time.Hour)
			},
		},
		{
			name: "invalid",
			raw:  "later",
			assert: func(t *testing.T, got time.Time, err error) {
				if err == nil {
					t.Fatal("parseTimeFlag() error = nil, want error")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTimeFlag(tt.raw)
			tt.assert(t, got, err)
		})
	}
}

func TestParseBytesFlag(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int64
		wantErr bool
	}{
		{name: "plain", raw: "1024", want: 1024},
		{name: "kilobytes", raw: "512KB", want: 512 * 1024},
		{name: "megabytes", raw: "1MB", want: 1024 * 1024},
		{name: "invalid", raw: "bad", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBytesFlag(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseBytesFlag() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBytesFlag() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("bytes = %d, want %d", got, tt.want)
			}
		})
	}
}

func newLessonsFixture(t *testing.T) (string, string, *learning.Store, []learning.Lesson) {
	t.Helper()

	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "lessons.jsonl")
	store := learning.NewStore(path)
	lessons := []learning.Lesson{
		{
			Text:       "Alpha lesson for persistence tuning.",
			Topic:      "alpha",
			Source:     "research",
			Confidence: "high",
			PersonaDelta: []self.Lesson{{
				Param: "persistence",
				Delta: 0.02,
			}},
		},
		{
			Text:       "Alpha ops lesson needs more evidence.",
			Topic:      "alpha-ops",
			Source:     "research",
			Confidence: "medium",
		},
		{
			Text:       "Bravo lesson captured a stable regression fix.",
			Topic:      "bravo",
			Source:     "research",
			Confidence: "low",
		},
	}
	appendManualLessons(t, store, lessons...)
	stored, err := store.List()
	if err != nil {
		t.Fatalf("store.List() error = %v", err)
	}

	return path, writeLessonsTestConfig(t, dataDir), store, stored
}

func writeLessonsTestConfig(t *testing.T, dataDir string) string {
	return writeLessonsTestConfigWithLLM(t, dataDir, false)
}

func writeLessonsTestConfigWithLLM(t *testing.T, dataDir string, enabled bool) string {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	data := "data_dir: " + dataDir + "\n" +
		"wiki_dir: " + filepath.Join(dataDir, "wiki") + "\n" +
		"locale: en\n" +
		"permission:\n  mode: default\n"
	if enabled {
		data += "llm_extraction:\n  enabled: true\n"
	}
	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

func appendExtraLessons(t *testing.T, store *learning.Store, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		if err := store.Append(learning.Lesson{
			ID:         fmt.Sprintf("cc%06d", i),
			Text:       fmt.Sprintf("Expanded lesson payload %02d %s", i, strings.Repeat("payload-", 12)),
			Topic:      "bulk",
			Source:     "research",
			Confidence: "medium",
			Created:    time.Date(2026, 4, 13, 12, i, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("store.Append(extra %d) error = %v", i, err)
		}
	}
}

func appendManualLessons(t *testing.T, store *learning.Store, lessons ...learning.Lesson) {
	t.Helper()
	for _, lesson := range lessons {
		if err := store.Append(lesson); err != nil {
			t.Fatalf("store.Append(%q) error = %v", lesson.Text, err)
		}
	}
}

func activeLessonCount(t *testing.T, store *learning.Store) int {
	t.Helper()
	lessons, err := store.List()
	if err != nil {
		t.Fatalf("store.List() error = %v", err)
	}
	return len(lessons)
}

func archiveLessonCount(t *testing.T, activePath string) int {
	t.Helper()
	archivePath := archivePathForTest(activePath)
	data, err := os.ReadFile(archivePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("ReadFile(%q) error = %v", archivePath, err)
	}
	return len(nonEmptyLines(string(data)))
}

func writeArchiveEntry(t *testing.T, activePath string, id string, text string) {
	t.Helper()

	archivePath := archivePathForTest(activePath)
	line := fmt.Sprintf("{\"id\":%q,\"text\":%q,\"topic\":\"archive\",\"source\":\"test\",\"confidence\":\"high\",\"created\":\"2026-04-13T12:00:00Z\"}\n", id, text)
	if err := os.WriteFile(archivePath, []byte(line), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", archivePath, err)
	}
}

func archivePathForTest(activePath string) string {
	return strings.TrimSuffix(activePath, ".jsonl") + ".archive.jsonl"
}

func lessonFileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	return fi.Size()
}

func nonEmptyLines(s string) []string {
	parts := strings.Split(s, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func withNonTTYStdin(t *testing.T) {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "stdin-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	if _, err := file.WriteString("n\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatalf("Seek() error = %v", err)
	}

	oldStdin := os.Stdin
	os.Stdin = file
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = file.Close()
	})
}

func assertRelativeTime(t *testing.T, got time.Time, err error, delta time.Duration) {
	t.Helper()
	if err != nil {
		t.Fatalf("parseTimeFlag() error = %v", err)
	}
	after := time.Now().UTC()
	min := after.Add(-delta).Add(-2 * time.Second)
	max := after.Add(-delta).Add(2 * time.Second)
	if got.Before(min) || got.After(max) {
		t.Fatalf("time = %v, want within [%v, %v]", got, min, max)
	}
}
