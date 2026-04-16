# Phase 7.2 Maturity Scorecard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `elnath debug scorecard` — measures four maturity axes (routing adaptation, outcome recording, lesson extraction, synthesis compounding), appends structured JSON to per-day files, and prints a Markdown report. One baseline run today produces baseline v1.

**Architecture:** New `internal/scorecard/` package — one file per axis, a `Compute()` orchestrator, a Markdown renderer, and a JSONL persister. Read-only consumer of existing `outcomes.jsonl`, `lessons.jsonl`, `consolidation_state.json`, and `wiki/synthesis/`. CLI wired as `cmd/elnath/cmd_debug_scorecard.go` following the existing `debug consolidation` pattern. No daemon changes, no schema migration.

**Tech Stack:** Go 1.25, stdlib only (no new deps). Existing `github.com/stello/elnath/internal/learning` types reused for outcomes/lessons/state. Table-driven tests with `-race`.

**Spec:** `docs/superpowers/specs/2026-04-17-phase-7-2-maturity-scorecard-design.md`

---

## File Map

### Create (`internal/scorecard/`)

| File | Responsibility |
|---|---|
| `scorecard.go` | `Score`, `AxisReport`, `Report`, `SourcesPaths`, `Compute()`, `aggregateOverall()` |
| `scorecard_test.go` | type + `aggregateOverall` + `Compute` end-to-end tests |
| `paths.go` | `ScorecardFilePath(dataDir string, day time.Time) string` |
| `paths_test.go` | paths unit tests |
| `axes_routing.go` | `computeRoutingAdaptation(SourcesPaths, time.Time) AxisReport` |
| `axes_routing_test.go` | table-driven fixture tests |
| `axes_outcome.go` | `computeOutcomeRecording(SourcesPaths, time.Time) AxisReport` |
| `axes_outcome_test.go` | |
| `axes_lesson.go` | `computeLessonExtraction(SourcesPaths, time.Time) AxisReport` |
| `axes_lesson_test.go` | |
| `axes_synthesis.go` | `computeSynthesisCompounding(SourcesPaths, time.Time) AxisReport` |
| `axes_synthesis_test.go` | |
| `markdown.go` | `RenderMarkdown(Report) string` |
| `markdown_test.go` | golden test |
| `persist.go` | `AppendJSON(Report, filePath string) error` |
| `persist_test.go` | tmpdir append test |

### Create (`cmd/elnath/`)

| File | Responsibility |
|---|---|
| `cmd_debug_scorecard.go` | `debugScorecard(ctx, args) error`, `--json` flag |

### Modify

- `cmd/elnath/cmd_debug.go` — add `case "scorecard"` dispatch + usage line.

---

## Task 1: Package skeleton — types, enums, `Report`

**Files:**
- Create: `internal/scorecard/scorecard.go`
- Create: `internal/scorecard/scorecard_test.go`

- [ ] **Step 1: Write failing test for `Score` String behavior and overall aggregation**

Create `internal/scorecard/scorecard_test.go`:

```go
package scorecard

import "testing"

func TestScoreStringValues(t *testing.T) {
	tests := []struct {
		score Score
		want  string
	}{
		{ScoreOK, "OK"},
		{ScoreNascent, "NASCENT"},
		{ScoreDegraded, "DEGRADED"},
		{ScoreUnknown, "UNKNOWN"},
	}
	for _, tc := range tests {
		if got := string(tc.score); got != tc.want {
			t.Errorf("Score %v: got %q, want %q", tc.score, got, tc.want)
		}
	}
}

func TestAggregateOverall(t *testing.T) {
	mk := func(s Score) AxisReport { return AxisReport{Score: s} }
	tests := []struct {
		name string
		axes AxesReport
		want Score
	}{
		{"all OK", AxesReport{mk(ScoreOK), mk(ScoreOK), mk(ScoreOK), mk(ScoreOK)}, ScoreOK},
		{"any DEGRADED wins", AxesReport{mk(ScoreOK), mk(ScoreDegraded), mk(ScoreOK), mk(ScoreOK)}, ScoreDegraded},
		{"DEGRADED beats UNKNOWN", AxesReport{mk(ScoreDegraded), mk(ScoreUnknown), mk(ScoreOK), mk(ScoreOK)}, ScoreDegraded},
		{"any UNKNOWN else", AxesReport{mk(ScoreOK), mk(ScoreUnknown), mk(ScoreOK), mk(ScoreOK)}, ScoreUnknown},
		{"mixed OK/NASCENT", AxesReport{mk(ScoreOK), mk(ScoreNascent), mk(ScoreOK), mk(ScoreNascent)}, ScoreNascent},
		{"all NASCENT", AxesReport{mk(ScoreNascent), mk(ScoreNascent), mk(ScoreNascent), mk(ScoreNascent)}, ScoreNascent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregateOverall(tc.axes)
			if got != tc.want {
				t.Errorf("aggregateOverall: got %v, want %v", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scorecard/... -run TestScore -race`
Expected: FAIL — package does not exist yet.

- [ ] **Step 3: Create `internal/scorecard/scorecard.go`**

```go
// Package scorecard computes the Phase 7.2 maturity scorecard by reading
// outcomes, lessons, synthesis pages, and consolidation state without
// modifying them. It emits a structured Report intended for append-only
// per-day JSONL persistence.
package scorecard

import "time"

// Score is a four-level classification per axis and for the overall score.
type Score string

const (
	ScoreOK       Score = "OK"
	ScoreNascent  Score = "NASCENT"
	ScoreDegraded Score = "DEGRADED"
	ScoreUnknown  Score = "UNKNOWN"
)

// AxisReport captures one axis's score, raw metrics, and a one-line reason.
type AxisReport struct {
	Score   Score          `json:"score"`
	Metrics map[string]any `json:"metrics"`
	Reason  string         `json:"reason"`
}

// AxesReport groups all four axes in a fixed order for deterministic JSON.
type AxesReport struct {
	RoutingAdaptation    AxisReport `json:"routing_adaptation"`
	OutcomeRecording     AxisReport `json:"outcome_recording"`
	LessonExtraction     AxisReport `json:"lesson_extraction"`
	SynthesisCompounding AxisReport `json:"synthesis_compounding"`
}

// SourcesPaths describes where each axis reads from.
type SourcesPaths struct {
	OutcomesPath string `json:"outcomes_path"`
	LessonsPath  string `json:"lessons_path"`
	SynthesisDir string `json:"synthesis_dir"`
	StatePath    string `json:"state_path"`
}

// Report is the full scorecard snapshot at one instant.
type Report struct {
	Timestamp     time.Time    `json:"timestamp"`
	SchemaVersion string       `json:"schema_version"`
	ElnathVersion string       `json:"elnath_version"`
	Overall       Score        `json:"overall"`
	Axes          AxesReport   `json:"axes"`
	Sources       SourcesPaths `json:"sources"`
}

// SchemaVersion is the current JSON schema version.
const SchemaVersion = "1.0"

// aggregateOverall applies the composition rule:
// any DEGRADED wins; else all OK is OK; else any UNKNOWN is UNKNOWN; else NASCENT.
func aggregateOverall(a AxesReport) Score {
	all := []Score{
		a.RoutingAdaptation.Score,
		a.OutcomeRecording.Score,
		a.LessonExtraction.Score,
		a.SynthesisCompounding.Score,
	}
	for _, s := range all {
		if s == ScoreDegraded {
			return ScoreDegraded
		}
	}
	allOK := true
	anyUnknown := false
	for _, s := range all {
		if s != ScoreOK {
			allOK = false
		}
		if s == ScoreUnknown {
			anyUnknown = true
		}
	}
	switch {
	case allOK:
		return ScoreOK
	case anyUnknown:
		return ScoreUnknown
	default:
		return ScoreNascent
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/scorecard/... -race`
Expected: PASS, 2 tests, no races.

- [ ] **Step 5: Commit**

```bash
git add internal/scorecard/scorecard.go internal/scorecard/scorecard_test.go
git commit -m "feat(scorecard): add Report types and overall aggregation"
```

---

## Task 2: Paths helper

**Files:**
- Create: `internal/scorecard/paths.go`
- Create: `internal/scorecard/paths_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/scorecard/paths_test.go`:

```go
package scorecard

import (
	"testing"
	"time"
)

func TestScorecardFilePath(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Seoul")
	day := time.Date(2026, 4, 17, 8, 15, 0, 0, loc)
	got := ScorecardFilePath("/data", day)
	want := "/data/scorecard/2026-04-17.jsonl"
	if got != want {
		t.Errorf("ScorecardFilePath: got %q, want %q", got, want)
	}
}

func TestScorecardFilePathUsesLocalDate(t *testing.T) {
	utcDay := time.Date(2026, 4, 17, 23, 0, 0, 0, time.UTC)
	// local date may differ from UTC date; we just assert the shape & directory.
	got := ScorecardFilePath("/data", utcDay)
	if got[:16] != "/data/scorecard/" {
		t.Errorf("unexpected prefix: %q", got)
	}
	if len(got) != len("/data/scorecard/2026-04-17.jsonl") {
		t.Errorf("unexpected length: %q", got)
	}
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `go test ./internal/scorecard/ -run TestScorecardFilePath -race`
Expected: FAIL — `ScorecardFilePath` undefined.

- [ ] **Step 3: Create `internal/scorecard/paths.go`**

```go
package scorecard

import (
	"path/filepath"
	"time"
)

// ScorecardFilePath returns the per-day JSONL file path for a scorecard run.
// The filename uses the local-date portion of `day` (not UTC) so that a run
// at 2026-04-17 08:15+09:00 lands in 2026-04-17.jsonl.
func ScorecardFilePath(dataDir string, day time.Time) string {
	localDay := day.Local().Format("2006-01-02")
	return filepath.Join(dataDir, "scorecard", localDay+".jsonl")
}
```

- [ ] **Step 4: Run test, verify passes**

Run: `go test ./internal/scorecard/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scorecard/paths.go internal/scorecard/paths_test.go
git commit -m "feat(scorecard): add per-day JSONL path helper"
```

---

## Task 3: Axis — routing_adaptation

**Files:**
- Create: `internal/scorecard/axes_routing.go`
- Create: `internal/scorecard/axes_routing_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/scorecard/axes_routing_test.go`:

```go
package scorecard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeOutcomesFile(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "outcomes.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write outcomes: %v", err)
	}
	return p
}

func TestComputeRoutingAdaptationUnknown(t *testing.T) {
	paths := SourcesPaths{OutcomesPath: "/nonexistent/nope.jsonl"}
	got := computeRoutingAdaptation(paths, time.Now())
	if got.Score != ScoreUnknown {
		t.Errorf("missing file: got %v, want UNKNOWN", got.Score)
	}
}

func TestComputeRoutingAdaptationNascent(t *testing.T) {
	p := writeOutcomesFile(t, []string{
		`{"id":"a","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":true,"timestamp":"2026-04-16T20:46:51Z"}`,
		`{"id":"b","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":true,"timestamp":"2026-04-16T21:12:40Z"}`,
	})
	got := computeRoutingAdaptation(SourcesPaths{OutcomesPath: p}, time.Now())
	if got.Score != ScoreNascent {
		t.Errorf("2 outcomes: got %v, want NASCENT", got.Score)
	}
	if got.Metrics["outcomes_total"] != 2 {
		t.Errorf("outcomes_total: got %v, want 2", got.Metrics["outcomes_total"])
	}
}

func TestComputeRoutingAdaptationOK(t *testing.T) {
	var lines []string
	for i := 0; i < 10; i++ {
		success := "true"
		pref := "false"
		if i >= 5 {
			pref = "true"
		}
		lines = append(lines,
			`{"id":"`+string(rune('a'+i))+`","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":`+success+`,"preference_used":`+pref+`,"timestamp":"2026-04-16T`+twoDigits(i)+`:00:00Z"}`,
		)
	}
	p := writeOutcomesFile(t, lines)
	got := computeRoutingAdaptation(SourcesPaths{OutcomesPath: p}, time.Now())
	if got.Score != ScoreOK {
		t.Errorf("10 outcomes with PreferenceUsed: got %v (%s), want OK", got.Score, got.Reason)
	}
	if got.Metrics["preference_used_count"] != 5 {
		t.Errorf("preference_used_count: got %v, want 5", got.Metrics["preference_used_count"])
	}
}

func TestComputeRoutingAdaptationDegradedRegression(t *testing.T) {
	// 10 outcomes: first 5 success, last 5 error → trend = 0.0 - 1.0 = -1.0 (< -0.10)
	var lines []string
	for i := 0; i < 10; i++ {
		succ := "true"
		fr := "stop"
		if i >= 5 {
			succ = "false"
			fr = "error"
		}
		lines = append(lines,
			`{"id":"`+string(rune('a'+i))+`","project_id":"p","intent":"i","workflow":"w","finish_reason":"`+fr+`","success":`+succ+`,"preference_used":true,"timestamp":"2026-04-16T`+twoDigits(i)+`:00:00Z"}`,
		)
	}
	p := writeOutcomesFile(t, lines)
	got := computeRoutingAdaptation(SourcesPaths{OutcomesPath: p}, time.Now())
	if got.Score != ScoreDegraded {
		t.Errorf("regressing trend: got %v (%s), want DEGRADED", got.Score, got.Reason)
	}
}

func twoDigits(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `go test ./internal/scorecard/ -run TestComputeRoutingAdaptation -race`
Expected: FAIL — `computeRoutingAdaptation` undefined.

- [ ] **Step 3: Create `internal/scorecard/axes_routing.go`**

```go
package scorecard

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/stello/elnath/internal/learning"
)

// computeRoutingAdaptation measures whether RoutingAdvisor actually uses
// past outcomes to influence future routing. Reads outcomes.jsonl.
func computeRoutingAdaptation(paths SourcesPaths, now time.Time) AxisReport {
	if _, err := os.Stat(paths.OutcomesPath); err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("outcomes file missing: %s", paths.OutcomesPath),
		}
	}

	store := learning.NewOutcomeStore(paths.OutcomesPath)
	outcomes, err := store.Recent(0)
	if err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("load outcomes: %v", err),
		}
	}

	total := len(outcomes)
	prefUsed := 0
	successes := 0
	for _, o := range outcomes {
		if o.PreferenceUsed {
			prefUsed++
		}
		if o.Success {
			successes++
		}
	}
	var successRate float64
	if total > 0 {
		successRate = float64(successes) / float64(total)
	}
	var trend any // nil by default
	if total >= 10 {
		sort.Slice(outcomes, func(i, j int) bool {
			return outcomes[i].Timestamp.Before(outcomes[j].Timestamp)
		})
		mid := total / 2
		first := outcomes[:mid]
		second := outcomes[mid:]
		trend = rateOf(second) - rateOf(first)
	}
	metrics := map[string]any{
		"outcomes_total":        total,
		"preference_used_count": prefUsed,
		"preference_used_pct":   pct(prefUsed, total),
		"success_rate":          successRate,
		"trend":                 trend,
	}

	switch {
	case total < 10:
		return AxisReport{Score: ScoreNascent, Metrics: metrics, Reason: fmt.Sprintf("%d outcomes; need ≥10 for trend", total)}
	case prefUsed == 0:
		return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: "preference_used never true — advisor not consulted"}
	default:
		if t, ok := trend.(float64); ok && t < -0.10 {
			return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: fmt.Sprintf("trend %.2f below -0.10", t)}
		}
	}
	return AxisReport{Score: ScoreOK, Metrics: metrics, Reason: fmt.Sprintf("%d outcomes, %d with preference_used", total, prefUsed)}
}

func rateOf(records []learning.OutcomeRecord) float64 {
	if len(records) == 0 {
		return 0
	}
	succ := 0
	for _, r := range records {
		if r.Success {
			succ++
		}
	}
	return float64(succ) / float64(len(records))
}

func pct(num, denom int) float64 {
	if denom == 0 {
		return 0
	}
	return float64(num) / float64(denom)
}
```

- [ ] **Step 4: Run test, verify passes**

Run: `go test ./internal/scorecard/ -run TestComputeRoutingAdaptation -race -v`
Expected: PASS — 4 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/scorecard/axes_routing.go internal/scorecard/axes_routing_test.go
git commit -m "feat(scorecard): add routing_adaptation axis"
```

---

## Task 4: Axis — outcome_recording

**Files:**
- Create: `internal/scorecard/axes_outcome.go`
- Create: `internal/scorecard/axes_outcome_test.go`

- [ ] **Step 1: Write failing test**

```go
package scorecard

import (
	"testing"
	"time"
)

func TestComputeOutcomeRecordingUnknown(t *testing.T) {
	got := computeOutcomeRecording(SourcesPaths{OutcomesPath: "/nope"}, time.Now())
	if got.Score != ScoreUnknown {
		t.Errorf("missing file: got %v", got.Score)
	}
}

func TestComputeOutcomeRecordingNascent(t *testing.T) {
	p := writeOutcomesFile(t, []string{
		`{"id":"a","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":true,"timestamp":"2026-04-16T20:46:51Z"}`,
	})
	got := computeOutcomeRecording(SourcesPaths{OutcomesPath: p}, time.Now())
	if got.Score != ScoreNascent {
		t.Errorf("1 outcome: got %v", got.Score)
	}
}

func TestComputeOutcomeRecordingDegradedNoError(t *testing.T) {
	now := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
	var lines []string
	for i := 0; i < 5; i++ {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		lines = append(lines,
			`{"id":"id`+twoDigits(i)+`","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":true,"timestamp":"`+day+`T10:00:00Z"}`,
		)
	}
	p := writeOutcomesFile(t, lines)
	got := computeOutcomeRecording(SourcesPaths{OutcomesPath: p}, now)
	if got.Score != ScoreDegraded {
		t.Errorf("all success no error: got %v (%s)", got.Score, got.Reason)
	}
}

func TestComputeOutcomeRecordingOK(t *testing.T) {
	now := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
	var lines []string
	for i := 0; i < 5; i++ {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		succ := "true"
		fr := "stop"
		if i == 0 {
			succ = "false"
			fr = "error"
		}
		lines = append(lines,
			`{"id":"id`+twoDigits(i)+`","project_id":"p","intent":"i","workflow":"w","finish_reason":"`+fr+`","success":`+succ+`,"timestamp":"`+day+`T10:00:00Z"}`,
		)
	}
	p := writeOutcomesFile(t, lines)
	got := computeOutcomeRecording(SourcesPaths{OutcomesPath: p}, now)
	if got.Score != ScoreOK {
		t.Errorf("5 records with 1 error, 5 distinct days: got %v (%s)", got.Score, got.Reason)
	}
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `go test ./internal/scorecard/ -run TestComputeOutcomeRecording -race`
Expected: FAIL.

- [ ] **Step 3: Create `internal/scorecard/axes_outcome.go`**

```go
package scorecard

import (
	"fmt"
	"os"
	"time"

	"github.com/stello/elnath/internal/learning"
)

// computeOutcomeRecording measures whether outcome recording is happening
// across both success and error paths, and across multiple days recently.
func computeOutcomeRecording(paths SourcesPaths, now time.Time) AxisReport {
	if _, err := os.Stat(paths.OutcomesPath); err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("outcomes file missing: %s", paths.OutcomesPath),
		}
	}
	store := learning.NewOutcomeStore(paths.OutcomesPath)
	outcomes, err := store.Recent(0)
	if err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("load outcomes: %v", err),
		}
	}

	total := len(outcomes)
	success := 0
	errs := 0
	var last time.Time
	days := map[string]struct{}{}
	windowStart := now.Add(-7 * 24 * time.Hour)
	for _, o := range outcomes {
		if o.Success {
			success++
		} else {
			errs++
		}
		if o.Timestamp.After(last) {
			last = o.Timestamp
		}
		if !o.Timestamp.Before(windowStart) && !o.Timestamp.After(now) {
			days[o.Timestamp.Local().Format("2006-01-02")] = struct{}{}
		}
	}
	metrics := map[string]any{
		"outcomes_total":       total,
		"success_count":        success,
		"error_count":          errs,
		"distinct_days_last_7": len(days),
		"last_record_at":       last,
	}

	switch {
	case total < 5:
		return AxisReport{Score: ScoreNascent, Metrics: metrics, Reason: fmt.Sprintf("outcomes_total=%d < 5", total)}
	case errs == 0:
		return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: "no error outcomes — survivorship bias risk"}
	case len(days) < 3:
		return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: fmt.Sprintf("only %d distinct days in last 7", len(days))}
	}
	return AxisReport{Score: ScoreOK, Metrics: metrics, Reason: fmt.Sprintf("%d records across %d days, %d errors captured", total, len(days), errs)}
}
```

- [ ] **Step 4: Run test, verify passes**

Run: `go test ./internal/scorecard/ -run TestComputeOutcomeRecording -race -v`
Expected: PASS — 4 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/scorecard/axes_outcome.go internal/scorecard/axes_outcome_test.go
git commit -m "feat(scorecard): add outcome_recording axis"
```

---

## Task 5: Axis — lesson_extraction

**Files:**
- Create: `internal/scorecard/axes_lesson.go`
- Create: `internal/scorecard/axes_lesson_test.go`

- [ ] **Step 1: Write failing test**

```go
package scorecard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeLessonsFile(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "lessons.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write lessons: %v", err)
	}
	return p
}

func TestComputeLessonExtractionUnknown(t *testing.T) {
	got := computeLessonExtraction(SourcesPaths{LessonsPath: "/nope"}, time.Now())
	if got.Score != ScoreUnknown {
		t.Errorf("missing: got %v", got.Score)
	}
}

func TestComputeLessonExtractionNascent(t *testing.T) {
	p := writeLessonsFile(t, []string{
		`{"id":"1","topic":"t","content":"c","tags":[]}`,
		`{"id":"2","topic":"t","content":"c","tags":[]}`,
	})
	got := computeLessonExtraction(SourcesPaths{LessonsPath: p}, time.Now())
	if got.Score != ScoreNascent {
		t.Errorf("2 lessons: got %v", got.Score)
	}
}

func TestComputeLessonExtractionDegradedAllActive(t *testing.T) {
	var lines []string
	for i := 0; i < 6; i++ {
		lines = append(lines, `{"id":"id`+twoDigits(i)+`","topic":"t","content":"c","tags":[]}`)
	}
	p := writeLessonsFile(t, lines)
	got := computeLessonExtraction(SourcesPaths{LessonsPath: p}, time.Now())
	if got.Score != ScoreDegraded {
		t.Errorf("6 lessons none superseded: got %v (%s)", got.Score, got.Reason)
	}
}

func TestComputeLessonExtractionOK(t *testing.T) {
	var lines []string
	for i := 0; i < 6; i++ {
		sup := ""
		if i >= 3 {
			sup = `,"superseded_by":"synth-x"`
		}
		lines = append(lines, `{"id":"id`+twoDigits(i)+`","topic":"t","content":"c","tags":[]`+sup+`}`)
	}
	p := writeLessonsFile(t, lines)
	got := computeLessonExtraction(SourcesPaths{LessonsPath: p}, time.Now())
	if got.Score != ScoreOK {
		t.Errorf("6 lessons 3 superseded: got %v (%s)", got.Score, got.Reason)
	}
	if got.Metrics["lessons_active"] != 3 || got.Metrics["lessons_superseded"] != 3 {
		t.Errorf("split counts wrong: %v", got.Metrics)
	}
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `go test ./internal/scorecard/ -run TestComputeLessonExtraction -race`
Expected: FAIL.

- [ ] **Step 3: Create `internal/scorecard/axes_lesson.go`**

```go
package scorecard

import (
	"fmt"
	"os"
	"time"

	"github.com/stello/elnath/internal/learning"
)

// computeLessonExtraction measures whether lessons are being extracted and
// then superseded by synthesis (the compounding cycle).
func computeLessonExtraction(paths SourcesPaths, _ time.Time) AxisReport {
	if _, err := os.Stat(paths.LessonsPath); err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("lessons file missing: %s", paths.LessonsPath),
		}
	}
	store := learning.NewStore(paths.LessonsPath)
	lessons, err := store.List()
	if err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("load lessons: %v", err),
		}
	}

	total := len(lessons)
	active := 0
	superseded := 0
	for _, l := range lessons {
		if l.SupersededBy == "" {
			active++
		} else {
			superseded++
		}
	}
	ratio := 0.0
	if total > 0 {
		ratio = float64(superseded) / float64(total)
	}
	metrics := map[string]any{
		"lessons_total":       total,
		"lessons_active":      active,
		"lessons_superseded":  superseded,
		"supersession_ratio":  ratio,
	}

	switch {
	case total < 5:
		return AxisReport{Score: ScoreNascent, Metrics: metrics, Reason: fmt.Sprintf("lessons_total=%d < 5", total)}
	case superseded == 0:
		return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: "no lessons superseded — consolidation inactive"}
	}
	return AxisReport{Score: ScoreOK, Metrics: metrics, Reason: fmt.Sprintf("%d lessons, %d superseded (ratio=%.2f)", total, superseded, ratio)}
}
```

- [ ] **Step 4: Run test, verify passes**

Run: `go test ./internal/scorecard/ -run TestComputeLessonExtraction -race -v`
Expected: PASS — 4 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/scorecard/axes_lesson.go internal/scorecard/axes_lesson_test.go
git commit -m "feat(scorecard): add lesson_extraction axis"
```

---

## Task 6: Axis — synthesis_compounding

**Files:**
- Create: `internal/scorecard/axes_synthesis.go`
- Create: `internal/scorecard/axes_synthesis_test.go`

- [ ] **Step 1: Write failing test**

```go
package scorecard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func buildSynthesisFixture(t *testing.T, synthCount int, lessons []string, state string) SourcesPaths {
	t.Helper()
	dir := t.TempDir()
	synDir := filepath.Join(dir, "synthesis")
	for i := 0; i < synthCount; i++ {
		sub := filepath.Join(synDir, "cluster")
		_ = os.MkdirAll(sub, 0o755)
		fp := filepath.Join(sub, "page"+twoDigits(i)+".md")
		if err := os.WriteFile(fp, []byte("# synth"), 0o600); err != nil {
			t.Fatalf("write synth: %v", err)
		}
	}
	lp := writeLessonsFile(t, lessons)
	sp := filepath.Join(dir, "state.json")
	if err := os.WriteFile(sp, []byte(state), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	return SourcesPaths{
		LessonsPath:  lp,
		SynthesisDir: synDir,
		StatePath:    sp,
	}
}

func TestComputeSynthesisUnknown(t *testing.T) {
	got := computeSynthesisCompounding(SourcesPaths{StatePath: "/nope"}, time.Now())
	if got.Score != ScoreUnknown {
		t.Errorf("missing state: got %v", got.Score)
	}
}

func TestComputeSynthesisNascent(t *testing.T) {
	paths := buildSynthesisFixture(t, 0, nil, `{"run_count":0,"success_count":0}`)
	got := computeSynthesisCompounding(paths, time.Now())
	if got.Score != ScoreNascent {
		t.Errorf("run_count=0: got %v", got.Score)
	}
}

func TestComputeSynthesisOK(t *testing.T) {
	lessons := []string{
		`{"id":"1","topic":"t","content":"c","tags":[],"superseded_by":"synth-1"}`,
		`{"id":"2","topic":"t","content":"c","tags":[],"superseded_by":"synth-1"}`,
		`{"id":"3","topic":"t","content":"c","tags":[]}`,
	}
	paths := buildSynthesisFixture(t, 1, lessons, `{"run_count":1,"success_count":1,"last_success_at":"2026-04-17T07:01:28+09:00"}`)
	got := computeSynthesisCompounding(paths, time.Now())
	if got.Score != ScoreOK {
		t.Errorf("1 synth, 1 success, supersession>0: got %v (%s)", got.Score, got.Reason)
	}
}

func TestComputeSynthesisDegradedRunWithoutSuccess(t *testing.T) {
	paths := buildSynthesisFixture(t, 0, nil, `{"run_count":3,"success_count":0}`)
	got := computeSynthesisCompounding(paths, time.Now())
	if got.Score != ScoreDegraded {
		t.Errorf("runs without success: got %v (%s)", got.Score, got.Reason)
	}
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `go test ./internal/scorecard/ -run TestComputeSynthesis -race`
Expected: FAIL.

- [ ] **Step 3: Create `internal/scorecard/axes_synthesis.go`**

```go
package scorecard

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stello/elnath/internal/learning"
)

// computeSynthesisCompounding measures whether consolidation actually
// produces compounding synthesis pages.
func computeSynthesisCompounding(paths SourcesPaths, _ time.Time) AxisReport {
	if _, err := os.Stat(paths.StatePath); err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("state file missing: %s", paths.StatePath),
		}
	}
	state, err := learning.LoadConsolidationState(paths.StatePath)
	if err != nil {
		return AxisReport{
			Score:   ScoreUnknown,
			Metrics: map[string]any{},
			Reason:  fmt.Sprintf("load state: %v", err),
		}
	}

	// Count synthesis pages.
	synthCount := 0
	if paths.SynthesisDir != "" {
		pattern := filepath.Join(paths.SynthesisDir, "*", "*.md")
		matches, _ := filepath.Glob(pattern)
		synthCount = len(matches)
	}

	// Supersession ratio (cross-reference with lessons).
	ratio := 0.0
	if paths.LessonsPath != "" {
		if _, err := os.Stat(paths.LessonsPath); err == nil {
			store := learning.NewStore(paths.LessonsPath)
			lessons, _ := store.List()
			total := len(lessons)
			superseded := 0
			for _, l := range lessons {
				if l.SupersededBy != "" {
					superseded++
				}
			}
			if total > 0 {
				ratio = float64(superseded) / float64(total)
			}
		}
	}

	metrics := map[string]any{
		"synthesis_count":    synthCount,
		"run_count":          state.RunCount,
		"success_count":      state.SuccessCount,
		"last_success_at":    state.LastSuccessAt,
		"supersession_ratio": ratio,
	}

	switch {
	case state.RunCount == 0:
		return AxisReport{Score: ScoreNascent, Metrics: metrics, Reason: "no consolidation runs yet"}
	case state.SuccessCount == 0 || synthCount == 0:
		return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: fmt.Sprintf("runs=%d but success=%d, synth=%d", state.RunCount, state.SuccessCount, synthCount)}
	case ratio == 0:
		return AxisReport{Score: ScoreDegraded, Metrics: metrics, Reason: "synthesis pages exist but no lessons superseded"}
	}
	return AxisReport{Score: ScoreOK, Metrics: metrics, Reason: fmt.Sprintf("%d syntheses, %d successful run(s), supersession=%.2f", synthCount, state.SuccessCount, ratio)}
}

var _ = time.Now // retained import placeholder; unused
```

Note the unused-time guard at the end is only to keep the import if we later add time-based checks. If it emits a lint warning, remove it — `time` is needed if any other function uses it; verify before removing.

- [ ] **Step 4: Run test, verify passes**

Run: `go test ./internal/scorecard/ -run TestComputeSynthesis -race -v`
Expected: PASS — 4 tests.

- [ ] **Step 5: Clean unused import if lint complains**

If `time` is genuinely unused, drop both the import and the `var _ = time.Now` line.

Run: `go vet ./internal/scorecard/...` to confirm.

- [ ] **Step 6: Commit**

```bash
git add internal/scorecard/axes_synthesis.go internal/scorecard/axes_synthesis_test.go
git commit -m "feat(scorecard): add synthesis_compounding axis"
```

---

## Task 7: Compute orchestrator + end-to-end test

**Files:**
- Modify: `internal/scorecard/scorecard.go` (add `Compute`)
- Modify: `internal/scorecard/scorecard_test.go` (add end-to-end test)

- [ ] **Step 1: Write failing end-to-end test**

Append to `internal/scorecard/scorecard_test.go`:

```go
func TestComputeEndToEnd(t *testing.T) {
	// Build a fixture: 2 outcomes, 12 lessons (10 superseded), 2 synthesis pages, state with 1 success.
	outcomeLines := []string{
		`{"id":"a","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":true,"timestamp":"2026-04-16T20:46:51Z"}`,
		`{"id":"b","project_id":"p","intent":"i","workflow":"w","finish_reason":"stop","success":true,"timestamp":"2026-04-16T21:12:40Z"}`,
	}
	outcomesPath := writeOutcomesFile(t, outcomeLines)

	lessonLines := []string{}
	for i := 0; i < 12; i++ {
		sup := ""
		if i < 10 {
			sup = `,"superseded_by":"synth-x"`
		}
		lessonLines = append(lessonLines, `{"id":"id`+twoDigits(i)+`","topic":"t","content":"c","tags":[]`+sup+`}`)
	}
	paths := buildSynthesisFixture(t, 2, lessonLines, `{"run_count":1,"success_count":1,"last_success_at":"2026-04-17T07:01:28+09:00"}`)
	paths.OutcomesPath = outcomesPath

	now := time.Date(2026, 4, 17, 8, 15, 0, 0, time.UTC)
	r := Compute(paths, now, "0.6.0-test")

	if r.SchemaVersion != SchemaVersion {
		t.Errorf("schema version: got %q", r.SchemaVersion)
	}
	if r.ElnathVersion != "0.6.0-test" {
		t.Errorf("version not propagated")
	}
	if r.Timestamp != now {
		t.Errorf("timestamp: got %v", r.Timestamp)
	}
	if r.Axes.RoutingAdaptation.Score != ScoreNascent {
		t.Errorf("routing: expected NASCENT, got %v", r.Axes.RoutingAdaptation.Score)
	}
	if r.Axes.OutcomeRecording.Score != ScoreNascent {
		t.Errorf("outcome: expected NASCENT, got %v", r.Axes.OutcomeRecording.Score)
	}
	if r.Axes.LessonExtraction.Score != ScoreOK {
		t.Errorf("lesson: expected OK, got %v", r.Axes.LessonExtraction.Score)
	}
	if r.Axes.SynthesisCompounding.Score != ScoreOK {
		t.Errorf("synthesis: expected OK, got %v", r.Axes.SynthesisCompounding.Score)
	}
	if r.Overall != ScoreNascent {
		t.Errorf("overall: expected NASCENT (mixed OK/NASCENT), got %v", r.Overall)
	}
	if r.Sources.OutcomesPath != outcomesPath {
		t.Errorf("sources not recorded")
	}
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `go test ./internal/scorecard/ -run TestComputeEndToEnd -race`
Expected: FAIL — `Compute` undefined.

- [ ] **Step 3: Add `Compute` to `internal/scorecard/scorecard.go`**

```go
// Compute reads the four source artifacts and returns a complete Report.
// It never writes. Missing files produce UNKNOWN axes rather than errors.
func Compute(paths SourcesPaths, now time.Time, elnathVersion string) Report {
	axes := AxesReport{
		RoutingAdaptation:    computeRoutingAdaptation(paths, now),
		OutcomeRecording:     computeOutcomeRecording(paths, now),
		LessonExtraction:     computeLessonExtraction(paths, now),
		SynthesisCompounding: computeSynthesisCompounding(paths, now),
	}
	return Report{
		Timestamp:     now,
		SchemaVersion: SchemaVersion,
		ElnathVersion: elnathVersion,
		Overall:       aggregateOverall(axes),
		Axes:          axes,
		Sources:       paths,
	}
}
```

- [ ] **Step 4: Run test, verify passes**

Run: `go test ./internal/scorecard/ -race -v`
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scorecard/scorecard.go internal/scorecard/scorecard_test.go
git commit -m "feat(scorecard): add Compute orchestrator"
```

---

## Task 8: Markdown renderer

**Files:**
- Create: `internal/scorecard/markdown.go`
- Create: `internal/scorecard/markdown_test.go`

- [ ] **Step 1: Write failing test**

```go
package scorecard

import (
	"strings"
	"testing"
	"time"
)

func TestRenderMarkdownContainsAllAxes(t *testing.T) {
	r := Report{
		Timestamp:     time.Date(2026, 4, 17, 8, 15, 0, 0, time.UTC),
		SchemaVersion: "1.0",
		ElnathVersion: "0.6.0",
		Overall:       ScoreNascent,
		Axes: AxesReport{
			RoutingAdaptation:    AxisReport{Score: ScoreNascent, Reason: "2 outcomes"},
			OutcomeRecording:     AxisReport{Score: ScoreNascent, Reason: "outcomes_total < 5"},
			LessonExtraction:     AxisReport{Score: ScoreOK, Reason: "12 lessons, 10 superseded"},
			SynthesisCompounding: AxisReport{Score: ScoreOK, Reason: "2 syntheses, 1 successful run"},
		},
		Sources: SourcesPaths{
			OutcomesPath: "/tmp/outcomes.jsonl",
			LessonsPath:  "/tmp/lessons.jsonl",
			SynthesisDir: "/tmp/synthesis",
			StatePath:    "/tmp/state.json",
		},
	}
	md := RenderMarkdown(r)
	for _, want := range []string{
		"Maturity Scorecard",
		"Overall:",
		"NASCENT",
		"routing_adaptation",
		"outcome_recording",
		"lesson_extraction",
		"synthesis_compounding",
		"2 outcomes",
		"12 lessons, 10 superseded",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, md)
		}
	}
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `go test ./internal/scorecard/ -run TestRenderMarkdown -race`
Expected: FAIL.

- [ ] **Step 3: Create `internal/scorecard/markdown.go`**

```go
package scorecard

import (
	"fmt"
	"strings"
)

// RenderMarkdown produces a human-readable report from a Report. All data
// is derived from the Report; no independent computation is performed.
func RenderMarkdown(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Maturity Scorecard — %s\n\n", r.Timestamp.Local().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "  Overall:                %s\n\n", r.Overall)
	axes := []struct {
		name   string
		report AxisReport
	}{
		{"routing_adaptation", r.Axes.RoutingAdaptation},
		{"outcome_recording", r.Axes.OutcomeRecording},
		{"lesson_extraction", r.Axes.LessonExtraction},
		{"synthesis_compounding", r.Axes.SynthesisCompounding},
	}
	for _, a := range axes {
		fmt.Fprintf(&b, "  %-24s%-10s%s\n", a.name, a.report.Score, a.report.Reason)
	}
	fmt.Fprintf(&b, "\n  Sources:\n")
	fmt.Fprintf(&b, "    outcomes:    %s\n", r.Sources.OutcomesPath)
	fmt.Fprintf(&b, "    lessons:     %s\n", r.Sources.LessonsPath)
	fmt.Fprintf(&b, "    synthesis:   %s\n", r.Sources.SynthesisDir)
	fmt.Fprintf(&b, "    state:       %s\n", r.Sources.StatePath)
	return b.String()
}
```

- [ ] **Step 4: Run test, verify passes**

Run: `go test ./internal/scorecard/ -run TestRenderMarkdown -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scorecard/markdown.go internal/scorecard/markdown_test.go
git commit -m "feat(scorecard): add markdown renderer"
```

---

## Task 9: Persist — JSONL append

**Files:**
- Create: `internal/scorecard/persist.go`
- Create: `internal/scorecard/persist_test.go`

- [ ] **Step 1: Write failing test**

```go
package scorecard

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendJSONCreatesDirectoryAndAppends(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "scorecard", "2026-04-17.jsonl")

	r1 := Report{Timestamp: time.Now(), SchemaVersion: "1.0", Overall: ScoreNascent}
	if err := AppendJSON(r1, filePath); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	r2 := Report{Timestamp: time.Now(), SchemaVersion: "1.0", Overall: ScoreOK}
	if err := AppendJSON(r2, filePath); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	count := 0
	for s.Scan() {
		count++
		var parsed Report
		if err := json.Unmarshal(s.Bytes(), &parsed); err != nil {
			t.Errorf("line %d invalid JSON: %v", count, err)
		}
	}
	if count != 2 {
		t.Errorf("lines: got %d, want 2", count)
	}

	// Verify file content is two trailing-newline lines.
	raw, _ := os.ReadFile(filePath)
	if strings.Count(string(raw), "\n") != 2 {
		t.Errorf("expected 2 trailing newlines, got %d", strings.Count(string(raw), "\n"))
	}
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `go test ./internal/scorecard/ -run TestAppendJSON -race`
Expected: FAIL.

- [ ] **Step 3: Create `internal/scorecard/persist.go`**

```go
package scorecard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AppendJSON appends one Report as a newline-delimited JSON line to filePath.
// Parent directories are created if missing. The file permission is 0o600.
func AppendJSON(r Report, filePath string) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("scorecard: mkdir: %w", err)
	}
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("scorecard: open %s: %w", filePath, err)
	}
	defer f.Close()
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("scorecard: marshal: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("scorecard: write: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test, verify passes**

Run: `go test ./internal/scorecard/ -race -v`
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scorecard/persist.go internal/scorecard/persist_test.go
git commit -m "feat(scorecard): add JSONL append persister"
```

---

## Task 10: CLI — `elnath debug scorecard`

**Files:**
- Create: `cmd/elnath/cmd_debug_scorecard.go`
- Modify: `cmd/elnath/cmd_debug.go` (add dispatch + usage)

- [ ] **Step 1: Modify `cmd/elnath/cmd_debug.go` switch**

Read the current switch block near line 23–32. Add one case and one usage line.

**Find:**
```go
	switch args[0] {
	case "info":
		return debugInfo()
	case "cost":
		return debugCost(args[1:])
	case "consolidation":
		return debugConsolidation(context.Background(), args[1:])
	default:
```

**Replace with:**
```go
	switch args[0] {
	case "info":
		return debugInfo()
	case "cost":
		return debugCost(args[1:])
	case "consolidation":
		return debugConsolidation(context.Background(), args[1:])
	case "scorecard":
		return debugScorecard(args[1:])
	default:
```

**Find in `printDebugUsage`:**
```go
  consolidation <action>  Lesson consolidation controls (run [--force], help)
  help                    Show this help
```

**Replace with:**
```go
  consolidation <action>  Lesson consolidation controls (run [--force], help)
  scorecard [--json]      Phase 7.2 maturity scorecard (current snapshot)
  help                    Show this help
```

- [ ] **Step 2: Create `cmd/elnath/cmd_debug_scorecard.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/scorecard"
)

// debugScorecard implements `elnath debug scorecard [--json]`.
//
// Default: compute the scorecard, append a JSON line to
// <data_dir>/scorecard/YYYY-MM-DD.jsonl, and print the Markdown report.
// With --json: print the JSON only; still appends to the daily file.
func debugScorecard(args []string) error {
	jsonOnly := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOnly = true
		case "-h", "--help", "help":
			fmt.Fprintln(os.Stdout, "Usage: elnath debug scorecard [--json]")
			return nil
		default:
			return fmt.Errorf("debug scorecard: unknown flag %q", a)
		}
	}

	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("debug scorecard: load config: %w", err)
	}

	paths := scorecard.SourcesPaths{
		OutcomesPath: filepath.Join(cfg.DataDir, "outcomes.jsonl"),
		LessonsPath:  filepath.Join(cfg.DataDir, "lessons.jsonl"),
		SynthesisDir: filepath.Join(cfg.WikiDir, "synthesis"),
		StatePath:    filepath.Join(cfg.DataDir, "consolidation_state.json"),
	}

	now := time.Now()
	report := scorecard.Compute(paths, now, version)

	outFile := scorecard.ScorecardFilePath(cfg.DataDir, now)
	if err := scorecard.AppendJSON(report, outFile); err != nil {
		return fmt.Errorf("debug scorecard: persist: %w", err)
	}

	if jsonOnly {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	fmt.Fprint(os.Stdout, scorecard.RenderMarkdown(report))
	fmt.Fprintf(os.Stdout, "\n  Appended to: %s\n", outFile)
	return nil
}
```

- [ ] **Step 3: Build binary**

Run: `make build`
Expected: build succeeds with no errors.

- [ ] **Step 4: Smoke test usage help**

Run: `./elnath debug scorecard --help`
Expected output: `Usage: elnath debug scorecard [--json]`

Run: `./elnath debug help`
Expected: includes `scorecard [--json] ...` line.

- [ ] **Step 5: Run full test suite**

Run: `go test -race ./...`
Expected: all packages PASS.

- [ ] **Step 6: Run lint**

Run: `go vet ./...` and `make lint`
Expected: no new issues attributable to `internal/scorecard/` or `cmd/elnath/cmd_debug_scorecard.go`.

- [ ] **Step 7: Commit**

```bash
git add cmd/elnath/cmd_debug_scorecard.go cmd/elnath/cmd_debug.go
git commit -m "feat(cli): add elnath debug scorecard"
```

---

## Task 11: Live probe — capture baseline v1

**Files:**
- Running binary against real `~/.elnath/` data
- New file produced: `~/.elnath/data/scorecard/2026-04-17.jsonl`

- [ ] **Step 1: Run scorecard against real data**

Run: `./elnath debug scorecard`

Expected output shape (exact values may vary; note them):
```
Maturity Scorecard — 2026-04-17 HH:MM

  Overall:                NASCENT

  routing_adaptation      NASCENT   (reason about 2 outcomes)
  outcome_recording       NASCENT   outcomes_total=2 < 5
  lesson_extraction       OK        12 lessons, 10 superseded (ratio=0.83)
  synthesis_compounding   OK        2 syntheses, 1 successful run(s), supersession=0.83
  ...
  Appended to: /Users/stello/.elnath/data/scorecard/2026-04-17.jsonl
```

- [ ] **Step 2: Verify JSON file**

Run: `cat ~/.elnath/data/scorecard/2026-04-17.jsonl | tail -1 | python3 -m json.tool`

Confirm:
- `schema_version == "1.0"`
- `overall == "NASCENT"`
- `axes.routing_adaptation.score == "NASCENT"`
- `axes.outcome_recording.score == "NASCENT"`
- `axes.lesson_extraction.score == "OK"`
- `axes.synthesis_compounding.score == "OK"`
- `sources.outcomes_path` ends with `/outcomes.jsonl`
- timestamp is today

- [ ] **Step 3: Re-run to confirm append works**

Run: `./elnath debug scorecard`
Run: `wc -l ~/.elnath/data/scorecard/2026-04-17.jsonl`
Expected: 2 lines.

- [ ] **Step 4: Cross-check with `debug consolidation show`**

Run: `./elnath debug consolidation show`
Verify the reported counts match the scorecard metrics (lessons active/superseded, synthesis count, run/success count).

- [ ] **Step 5: Commit no-op verification + status note**

This step has no code changes. It is the verification gate. If all of Steps 1–4 pass, proceed. If any fail, fix the underlying issue and re-run the relevant step before continuing.

- [ ] **Step 6: Archive the baseline**

The per-day JSONL lives in `~/.elnath/data/`, outside the repo — intentional (user data). Capture the baseline in the repo by appending a short note to the Phase 7.2 spec or creating a `docs/superpowers/research/2026-04-17-scorecard-baseline-v1.md` file with the literal JSON contents and a 3-line narrative.

Create `docs/superpowers/research/2026-04-17-scorecard-baseline-v1.md`:

```markdown
# Scorecard Baseline v1 — 2026-04-17

First run of `elnath debug scorecard` against real data after Stage A/B
landed. Overall NASCENT: routing/outcome axes have insufficient samples;
lesson/synthesis axes functional. This is the reference point against
which all subsequent runs are compared.

## Snapshot

<paste the JSON content of the first line of 2026-04-17.jsonl here, pretty-printed>
```

- [ ] **Step 7: Commit baseline artifact**

```bash
git add docs/superpowers/research/2026-04-17-scorecard-baseline-v1.md
git commit -m "docs(phase-7-2): capture scorecard baseline v1"
```

---

## Verification Gate

Before declaring Phase 7.2 complete:

- [ ] All 11 task commits landed on `main`.
- [ ] `go test -race ./...` PASSES with no new failures.
- [ ] `make build` produces a binary that executes `elnath debug scorecard`.
- [ ] `make lint` has no new warnings in `internal/scorecard/` or `cmd_debug_scorecard.go`.
- [ ] `~/.elnath/data/scorecard/2026-04-17.jsonl` exists with ≥1 valid JSON line.
- [ ] Baseline v1 snapshot committed under `docs/superpowers/research/`.
- [ ] Cross-check with `elnath debug consolidation show` confirms metrics agree.

## Follow-ups (out of scope for this plan)

- **FU-A** `--show` / `--diff` / `history` subcommands (after 2-3 runs exist)
- **FU-B** Scorecard consumption by RoutingAdvisor (Phase 7.3+)
- **FU-C** Quality axis using human override/undo counts
- **FU-D** Tunable thresholds via config file

---

## Self-review Notes

- Task 6 includes a guard for an unused `time` import; if your build environment complains, drop the `import "time"` and the `var _ = time.Now` guard together.
- `map[string]any` metrics rely on JSON alphabetical key ordering in output, which is stable for Go stdlib `encoding/json`.
- Task 11 Step 6 is the only step that writes into the repo; all others either write to `/tmp` (tests) or `~/.elnath/` (live probe).
- **Shared test helpers — important for parallel execution.** Tasks 3–6 reference helpers (`writeOutcomesFile`, `writeLessonsFile`, `buildSynthesisFixture`, `twoDigits`) that belong to the same Go test package. If running Tasks 3–6 as parallel subagents, the first agent to land MUST place these helpers in a dedicated `internal/scorecard/testhelpers_test.go` file so later tasks can reference them without duplicate declarations. If implementing sequentially, the helpers defined in Task 3's test file are automatically visible to Tasks 4–7; either model works, but mix-and-match will fail to compile.
