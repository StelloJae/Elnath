package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/wiki"
)

// consolidationDeps is the minimum set of pre-built dependencies a
// Consolidator needs. The CLI and the daemon both construct their own
// flavour of these values, so the heavy wiring lives here once.
type consolidationDeps struct {
	cfg          *config.Config
	provider     llm.Provider
	providerName string
	model        string
	wikiStore    *wiki.Store
	lessonStore  *learning.Store
}

// buildConsolidationDepsFromConfig resolves the lesson-consolidation
// dependencies from config + an already-built main provider. Used by the
// daemon scheduler path so the main provider is shared rather than rebuilt.
func buildConsolidationDepsFromConfig(cfg *config.Config, mainProvider llm.Provider, wikiStore *wiki.Store, lessonStore *learning.Store, mainModel string) (consolidationDeps, error) {
	if cfg == nil {
		return consolidationDeps{}, fmt.Errorf("consolidation setup: nil config")
	}
	if wikiStore == nil {
		return consolidationDeps{}, fmt.Errorf("consolidation setup: wiki store required")
	}
	if lessonStore == nil {
		return consolidationDeps{}, fmt.Errorf("consolidation setup: lesson store required")
	}

	lessonProvider, lessonModel := buildLessonProvider(cfg, mainProvider)
	if lessonProvider == nil {
		return consolidationDeps{}, fmt.Errorf("consolidation setup: no provider available (set anthropic.api_key or enable Codex OAuth)")
	}
	if lessonModel == "" {
		lessonModel = mainModel
	}
	return consolidationDeps{
		cfg:          cfg,
		provider:     lessonProvider,
		providerName: lessonProvider.Name(),
		model:        lessonModel,
		wikiStore:    wikiStore,
		lessonStore:  lessonStore,
	}, nil
}

// buildConsolidationDepsFromCLI is the CLI entry point: it loads config and
// constructs the stores from scratch.
func buildConsolidationDepsFromCLI(cfg *config.Config) (consolidationDeps, error) {
	provider, model, err := buildProvider(cfg)
	if err != nil {
		return consolidationDeps{}, fmt.Errorf("consolidation setup: build provider: %w", err)
	}
	wikiStore, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return consolidationDeps{}, fmt.Errorf("consolidation setup: wiki store: %w", err)
	}
	lessonStore := learning.NewStore(filepath.Join(cfg.DataDir, "lessons.jsonl"))
	return buildConsolidationDepsFromConfig(cfg, provider, wikiStore, lessonStore, model)
}

// newConsolidator assembles the Consolidator with the right gate knobs.
// When force=true the gate's time and session gates are zeroed so --force
// behaves like a manual kick.
func newConsolidator(deps consolidationDeps, force bool) *learning.Consolidator {
	lockPath := filepath.Join(deps.cfg.DataDir, ".consolidate-lock")
	statePath := filepath.Join(deps.cfg.DataDir, "consolidation_state.json")

	gateOpts := []learning.GateOption{
		learning.WithHolderStale(60 * time.Minute),
		learning.WithMinSessions(0),
	}
	if force {
		gateOpts = append(gateOpts, learning.WithMinInterval(0))
	} else {
		gateOpts = append(gateOpts, learning.WithMinInterval(24*time.Hour))
	}
	gate := learning.NewGate(lockPath, gateOpts...)

	systemPrefix := ""
	if deps.cfg.LLMExtraction.ClaudeCodeSignature {
		systemPrefix = "You are Claude Code, Anthropic's official CLI for Claude.\n\n"
	}

	return learning.NewConsolidator(learning.ConsolidatorConfig{
		Store:        deps.lessonStore,
		Wiki:         deps.wikiStore,
		Provider:     deps.provider,
		Gate:         gate,
		Model:        deps.model,
		StatePath:    statePath,
		SystemPrefix: systemPrefix,
	})
}
