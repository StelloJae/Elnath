package promptcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// APIResponseFixture is the test-only shape that fixtures under testdata/
// deserialize into. It mirrors the shape Anthropic's Messages API returns
// for usage attribution: input/output token counts plus cache attribution
// fields the break-detection step will compare.
//
// This type lives in a _test.go file so it does not ship in production
// binaries. Production break-detection types land in Phase 8.1 Commit 4.
type APIResponseFixture struct {
	Scenario              string    `json:"scenario"`
	Description           string    `json:"description"`
	Model                 string    `json:"model"`
	Betas                 []string  `json:"betas"`
	Usage                 struct {
		InputTokens              int `json:"input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		OutputTokens             int `json:"output_tokens"`
	} `json:"usage"`
	ReceivedAt            time.Time `json:"received_at"`
	ExpectedAttribution   string    `json:"expected_attribution"`
	Notes                 string    `json:"notes"`
}

// fixtureDir is where all redacted API response fixtures live.
const fixtureDir = "testdata"

// loadFixture reads a named fixture from testdata/ and unmarshals it. It
// fails the test on any I/O or parse error rather than returning them, so
// call sites stay focused on scenario logic.
func loadFixture(t *testing.T, name string) APIResponseFixture {
	t.Helper()
	path := filepath.Join(fixtureDir, name+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("loadFixture(%q): %v", name, err)
	}
	var f APIResponseFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("loadFixture(%q): unmarshal: %v", name, err)
	}
	return f
}

// listFixtures enumerates every fixture in testdata/ in sorted order so
// coverage tests can assert a baseline set exists.
func listFixtures(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatalf("listFixtures: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		names = append(names, name[:len(name)-len(".json")])
	}
	sort.Strings(names)
	return names
}

// TestFixtures_AllParseAndExposeRequiredFields walks every fixture and
// asserts the minimal invariants the break-detection harness will rely
// on: scenario name, model, non-zero usage, and a valid ReceivedAt.
// Regression guard against malformed or half-redacted fixtures.
func TestFixtures_AllParseAndExposeRequiredFields(t *testing.T) {
	names := listFixtures(t)
	if len(names) == 0 {
		t.Fatal("testdata/ has no *.json fixtures")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			f := loadFixture(t, name)
			if f.Scenario == "" {
				t.Error("scenario is empty")
			}
			if f.Scenario != name {
				t.Errorf("scenario field = %q, want %q (must match filename)", f.Scenario, name)
			}
			if f.Model == "" {
				t.Error("model is empty")
			}
			// At least one usage counter must be populated; otherwise the
			// fixture has nothing to attribute.
			u := f.Usage
			if u.InputTokens == 0 && u.CacheReadInputTokens == 0 && u.CacheCreationInputTokens == 0 {
				t.Error("usage has no populated token counts")
			}
			if f.ReceivedAt.IsZero() {
				t.Error("received_at is zero")
			}
		})
	}
}

// TestFixtures_BaselineSetPresent pins the set of scenarios the Commit 4
// break-detector will exercise. If this list changes, the detector's test
// table must change in lock-step.
func TestFixtures_BaselineSetPresent(t *testing.T) {
	want := []string{
		"cache_hit_cold",
		"cache_hit_warm",
		"cache_miss_beta_delta",
		"cache_miss_small",
		"cache_miss_system_edit",
		"cache_miss_tool_edit",
		"cache_miss_ttl_gap",
	}
	got := listFixtures(t)
	gotSet := make(map[string]bool, len(got))
	for _, n := range got {
		gotSet[n] = true
	}
	for _, w := range want {
		if !gotSet[w] {
			t.Errorf("missing baseline fixture: %s", w)
		}
	}
}

// TestFixtures_CacheHitScenariosHaveReadTokens is a cheap sanity check
// that every "cache_hit_*" fixture actually reports cache_read_input_tokens
// or cache_creation_input_tokens. Prevents a future refactor from stripping
// the cache attribution from hit scenarios unnoticed.
func TestFixtures_CacheHitScenariosHaveReadTokens(t *testing.T) {
	for _, name := range listFixtures(t) {
		if len(name) < 10 || name[:10] != "cache_hit_" {
			continue
		}
		f := loadFixture(t, name)
		if f.Usage.CacheReadInputTokens == 0 && f.Usage.CacheCreationInputTokens == 0 {
			t.Errorf("%s: neither cache_read nor cache_creation populated", name)
		}
	}
}
