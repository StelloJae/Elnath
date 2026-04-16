package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/wiki"
)

// WikiWriter is the minimum surface the Consolidator needs from the wiki
// layer. The wiki.Store concrete type satisfies it; tests use a stub.
type WikiWriter interface {
	Create(page *wiki.Page) error
}

// ConsolidationState persists across runs so the orchestrator can reason
// about history and expose it through debug commands.
type ConsolidationState struct {
	LastRunAt       time.Time `json:"last_run_at"`
	LastSuccessAt   time.Time `json:"last_success_at,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	RunCount        int       `json:"run_count"`
	SuccessCount    int       `json:"success_count"`
	LastSynthCount  int       `json:"last_synth_count"`
	LastSuperseded  int       `json:"last_superseded"`
	LastActiveCount int       `json:"last_active_count"`
}

// RunResult summarises a single Consolidator.Run invocation.
type RunResult struct {
	Skipped         bool
	SkipReason      string
	SynthesisCount  int
	SupersededCount int
	Error           error
}

// ConsolidatorConfig collects the dependencies and knobs for NewConsolidator.
type ConsolidatorConfig struct {
	Store        *Store
	Wiki         WikiWriter
	Provider     llm.Provider
	Gate         *Gate
	Model        string
	StatePath    string
	MaxLessons   int // default 50 — recent-N window (replaces B.2's clustering step)
	SystemPrefix string
	Now          func() time.Time
}

// Consolidator orchestrates the lesson-consolidation run: gate → select
// recent lessons → prompt → LLM → parse → persist synthesis → mark lessons
// superseded → write state.
type Consolidator struct {
	store        *Store
	wiki         WikiWriter
	provider     llm.Provider
	gate         *Gate
	model        string
	statePath    string
	maxLessons   int
	systemPrefix string
	now          func() time.Time
}

func NewConsolidator(cfg ConsolidatorConfig) *Consolidator {
	if cfg.MaxLessons <= 0 {
		cfg.MaxLessons = 50
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Consolidator{
		store:        cfg.Store,
		wiki:         cfg.Wiki,
		provider:     cfg.Provider,
		gate:         cfg.Gate,
		model:        cfg.Model,
		statePath:    cfg.StatePath,
		maxLessons:   cfg.MaxLessons,
		systemPrefix: cfg.SystemPrefix,
		now:          now,
	}
}

// Run executes one consolidation attempt. A nil error with result.Skipped=true
// means a gate blocked the run (not a failure). result.Error carries failures
// that left the lock rolled back so the next attempt can retry.
func (c *Consolidator) Run(ctx context.Context) (RunResult, error) {
	if c == nil {
		return RunResult{}, errors.New("consolidator: nil receiver")
	}
	if c.store == nil || c.wiki == nil || c.provider == nil || c.gate == nil {
		return RunResult{}, errors.New("consolidator: missing dependency")
	}

	now := c.now()

	if ok, reason := c.gate.ShouldRun(now); !ok {
		return RunResult{Skipped: true, SkipReason: reason}, nil
	}

	release, err := c.gate.Acquire()
	if err != nil {
		return RunResult{Skipped: true, SkipReason: fmt.Sprintf("acquire: %v", err)}, nil
	}
	success := false
	defer func() {
		if !success {
			release()
		}
	}()

	state, _ := c.readState()
	state.RunCount++
	state.LastRunAt = now

	lessons, err := c.store.Recent(c.maxLessons)
	if err != nil {
		return c.failRun(state, fmt.Errorf("read lessons: %w", err)), nil
	}
	active := make([]Lesson, 0, len(lessons))
	for _, l := range lessons {
		if l.SupersededBy != "" {
			continue
		}
		active = append(active, l)
	}
	state.LastActiveCount = len(active)

	if len(active) < 2 {
		state.LastError = "insufficient active lessons"
		_ = c.writeState(state)
		return RunResult{Skipped: true, SkipReason: "insufficient active lessons"}, nil
	}

	req := ConsolidationRequest{
		Lessons:        active,
		SessionContext: fmt.Sprintf("run #%d, %d active lessons", state.RunCount, len(active)),
	}
	system, user := BuildConsolidationPrompt(req)
	if c.systemPrefix != "" {
		system = c.systemPrefix + system
	}

	resp, err := c.provider.Chat(ctx, llm.ChatRequest{
		Model:       c.model,
		System:      system,
		Messages:    []llm.Message{llm.NewUserMessage(user)},
		MaxTokens:   4096,
		Temperature: 0,
		EnableCache: true,
	})
	if err != nil {
		return c.failRun(state, fmt.Errorf("llm call: %w", err)), nil
	}

	validIDs := make(map[string]bool, len(active))
	for _, l := range active {
		validIDs[l.ID] = true
	}
	output, err := ParseConsolidationResponse(resp.Content, validIDs)
	if err != nil {
		return c.failRun(state, fmt.Errorf("parse response: %w", err)), nil
	}

	synthCount := 0
	supersededCount := 0
	for _, item := range output.Syntheses {
		synthID := wiki.SynthesisID(item.Text)
		primaryTopic := ""
		if len(item.TopicTags) > 0 {
			primaryTopic = item.TopicTags[0]
		}
		page := wiki.BuildSynthesisPage(synthID, primaryTopic, item.Text, item.TopicTags, now)
		if err := c.wiki.Create(page); err != nil {
			slog.Warn("consolidator: wiki create failed", "err", err, "path", page.Path)
			continue
		}
		synthCount++
		n, err := c.store.MarkSuperseded(item.SupersededLessonIDs, synthID)
		if err != nil {
			slog.Warn("consolidator: mark superseded failed", "err", err, "id", synthID)
			continue
		}
		supersededCount += n
	}

	success = true
	state.LastSuccessAt = now
	state.SuccessCount++
	state.LastSynthCount = synthCount
	state.LastSuperseded = supersededCount
	state.LastError = ""
	_ = c.writeState(state)

	return RunResult{SynthesisCount: synthCount, SupersededCount: supersededCount}, nil
}

func (c *Consolidator) failRun(state *ConsolidationState, err error) RunResult {
	state.LastError = err.Error()
	_ = c.writeState(state)
	return RunResult{Error: err}
}

func (c *Consolidator) readState() (*ConsolidationState, error) {
	if c.statePath == "" {
		return &ConsolidationState{}, nil
	}
	data, err := os.ReadFile(c.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ConsolidationState{}, nil
		}
		return nil, err
	}
	var state ConsolidationState
	if err := json.Unmarshal(data, &state); err != nil {
		return &ConsolidationState{}, err
	}
	return &state, nil
}

func (c *Consolidator) writeState(state *ConsolidationState) error {
	if c.statePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.statePath), 0o755); err != nil {
		return fmt.Errorf("consolidator state: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("consolidator state: marshal: %w", err)
	}
	tmp := c.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("consolidator state: write tmp: %w", err)
	}
	if err := os.Rename(tmp, c.statePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("consolidator state: rename: %w", err)
	}
	return nil
}
