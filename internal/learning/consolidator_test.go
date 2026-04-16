package learning

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/wiki"
)

type stubProvider struct {
	response  string
	err       error
	callCount int
	lastReq   llm.ChatRequest
}

func (s *stubProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	s.callCount++
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	return &llm.ChatResponse{Content: s.response}, nil
}

func (s *stubProvider) Stream(context.Context, llm.ChatRequest, func(llm.StreamEvent)) error {
	return nil
}

func (s *stubProvider) Name() string             { return "stub" }
func (s *stubProvider) Models() []llm.ModelInfo  { return nil }

type stubWiki struct {
	created map[string]*wiki.Page
	err     error
}

func (w *stubWiki) Create(page *wiki.Page) error {
	if w.err != nil {
		return w.err
	}
	if w.created == nil {
		w.created = map[string]*wiki.Page{}
	}
	w.created[page.Path] = page
	return nil
}

func consolidatorHarness(t *testing.T) (string, *Store, *stubWiki, *stubProvider, *Gate) {
	t.Helper()
	dir := t.TempDir()
	lessonsPath := filepath.Join(dir, "lessons.jsonl")
	statePath := filepath.Join(dir, "consolidation_state.json")
	store := NewStore(lessonsPath)
	w := &stubWiki{}
	p := &stubProvider{}
	gate := NewGate(filepath.Join(dir, ".consolidate-lock"),
		WithMinInterval(time.Hour),
		WithMinSessions(0),
		WithHolderStale(10*time.Minute),
		WithSessionCount(func(time.Time) (int, error) { return 99, nil }),
	)
	_ = statePath
	return dir, store, w, p, gate
}

func seedActiveLessons(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	base := time.Now().UTC().Add(-time.Hour)
	for i, id := range ids {
		if err := s.Append(Lesson{
			ID:         id,
			Text:       "lesson " + id,
			Topic:      "topic " + id,
			Source:     "test",
			Confidence: "high",
			Created:    base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func newConsolidator(t *testing.T, dir string, store *Store, w *stubWiki, p *stubProvider, gate *Gate) *Consolidator {
	t.Helper()
	return NewConsolidator(ConsolidatorConfig{
		Store:      store,
		Wiki:       w,
		Provider:   p,
		Gate:       gate,
		Model:      "test-model",
		StatePath:  filepath.Join(dir, "consolidation_state.json"),
		MaxLessons: 50,
	})
}

func TestConsolidator_Run_SuccessWritesPageAndMarksSuperseded(t *testing.T) {
	dir, store, w, p, gate := consolidatorHarness(t)
	seedActiveLessons(t, store, "l1", "l2", "l3")
	p.response = `{"syntheses":[
		{"synthesis_text":"Atomic swap beats in-place writes",
		 "topic_tags":["file-io"],
		 "superseded_lesson_ids":["l1","l2"],
		 "confidence":"high"}
	]}`
	c := newConsolidator(t, dir, store, w, p, gate)

	result, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Skipped {
		t.Fatalf("Skipped=true, reason=%s", result.SkipReason)
	}
	if result.SynthesisCount != 1 {
		t.Errorf("SynthesisCount=%d, want 1", result.SynthesisCount)
	}
	if result.SupersededCount != 2 {
		t.Errorf("SupersededCount=%d, want 2", result.SupersededCount)
	}
	if len(w.created) != 1 {
		t.Errorf("wiki pages created=%d, want 1", len(w.created))
	}

	lessons, _ := store.List()
	marks := map[string]string{}
	for _, l := range lessons {
		marks[l.ID] = l.SupersededBy
	}
	if marks["l1"] == "" || marks["l2"] == "" {
		t.Errorf("l1/l2 not marked, got %v", marks)
	}
	if marks["l3"] != "" {
		t.Errorf("l3 should remain active, got %q", marks["l3"])
	}

	stateData, err := os.ReadFile(filepath.Join(dir, "consolidation_state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var state ConsolidationState
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatal(err)
	}
	if state.RunCount != 1 || state.SuccessCount != 1 {
		t.Errorf("state run=%d success=%d, want 1 1", state.RunCount, state.SuccessCount)
	}
	if state.LastSynthCount != 1 || state.LastSuperseded != 2 {
		t.Errorf("state synth=%d superseded=%d", state.LastSynthCount, state.LastSuperseded)
	}
}

func TestConsolidator_Run_SkipWhenGateBlocks(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "lessons.jsonl"))
	seedActiveLessons(t, store, "l1", "l2")
	w := &stubWiki{}
	p := &stubProvider{}
	gate := NewGate(filepath.Join(dir, ".consolidate-lock"),
		WithMinInterval(time.Hour),
		WithMinSessions(5),
		WithSessionCount(func(time.Time) (int, error) { return 1, nil }),
	)
	c := newConsolidator(t, dir, store, w, p, gate)
	result, err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Skipped {
		t.Fatal("expected Skipped, got false")
	}
	if p.callCount != 0 {
		t.Errorf("provider called %d times despite gate block", p.callCount)
	}
}

func TestConsolidator_Run_SkipWhenInsufficientActiveLessons(t *testing.T) {
	dir, store, w, p, gate := consolidatorHarness(t)
	seedActiveLessons(t, store, "l1")
	c := newConsolidator(t, dir, store, w, p, gate)
	result, err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Skipped {
		t.Fatal("expected Skipped for <2 active lessons")
	}
	if !strings.Contains(result.SkipReason, "insufficient") {
		t.Errorf("reason=%q, want insufficient", result.SkipReason)
	}
	if p.callCount != 0 {
		t.Errorf("provider called %d times despite insufficient lessons", p.callCount)
	}
}

func TestConsolidator_Run_LLMErrorRollsBackLock(t *testing.T) {
	dir, store, w, p, gate := consolidatorHarness(t)
	seedActiveLessons(t, store, "l1", "l2", "l3")
	p.err = errors.New("provider down")
	c := newConsolidator(t, dir, store, w, p, gate)

	result, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned hard error: %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected Run to report failure via RunResult.Error")
	}

	// Second run immediately should succeed (lock rolled back, gate lets through)
	p.err = nil
	p.response = `{"syntheses":[]}`
	result, err = c.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Skipped {
		t.Fatalf("second Run skipped: %s", result.SkipReason)
	}
}

func TestConsolidator_Run_MalformedJSONErrorsButRollsBack(t *testing.T) {
	dir, store, w, p, gate := consolidatorHarness(t)
	seedActiveLessons(t, store, "l1", "l2")
	p.response = `not json at all`
	c := newConsolidator(t, dir, store, w, p, gate)

	result, _ := c.Run(context.Background())
	if result.Error == nil {
		t.Fatal("expected error for malformed JSON")
	}

	// Lock should be rolled back so a clean retry works
	p.response = `{"syntheses":[]}`
	result2, _ := c.Run(context.Background())
	if result2.Error != nil {
		t.Fatalf("retry after malformed failed: %v", result2.Error)
	}
}

func TestConsolidator_Run_SkipsAlreadySupersededLessons(t *testing.T) {
	dir, store, w, p, gate := consolidatorHarness(t)
	seedActiveLessons(t, store, "l1", "l2", "l3")
	if _, err := store.MarkSuperseded([]string{"l1"}, "synth-prior"); err != nil {
		t.Fatal(err)
	}
	p.response = `{"syntheses":[]}`
	c := newConsolidator(t, dir, store, w, p, gate)

	if _, err := c.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The prompt should only have carried the active lessons (l2, l3)
	if strings.Contains(p.lastReq.Messages[0].Text(), "l1") {
		t.Errorf("prompt mentions already-superseded l1")
	}
	for _, want := range []string{"l2", "l3"} {
		if !strings.Contains(p.lastReq.Messages[0].Text(), want) {
			t.Errorf("prompt missing active lesson %s", want)
		}
	}
}

func TestConsolidator_Run_ZeroSynthesesStillCountsAsSuccess(t *testing.T) {
	dir, store, w, p, gate := consolidatorHarness(t)
	seedActiveLessons(t, store, "l1", "l2")
	p.response = `{"syntheses":[]}`
	c := newConsolidator(t, dir, store, w, p, gate)

	result, err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped {
		t.Fatalf("expected success with 0 syntheses, got Skipped: %s", result.SkipReason)
	}
	if result.SynthesisCount != 0 {
		t.Errorf("SynthesisCount=%d, want 0", result.SynthesisCount)
	}
	// Lock mtime should NOT roll back — next immediate Run should be blocked by time gate
	result2, _ := c.Run(context.Background())
	if !result2.Skipped {
		t.Fatal("expected second Run to be time-gated after successful zero-synth run")
	}
}
