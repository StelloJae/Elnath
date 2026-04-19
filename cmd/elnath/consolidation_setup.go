package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/wiki"
)

// consolidationDeps is the minimum set of pre-built dependencies a
// Consolidator needs. The CLI and the daemon both construct their own
// flavour of these values, so the heavy wiring lives here once.
//
// wikiDB is owned by the CLI path (set via openWikiStoreWithIndex) and
// must be closed by the caller; the daemon path leaves it nil because
// the runtime owns the DB lifecycle.
type consolidationDeps struct {
	cfg          *config.Config
	provider     llm.Provider
	providerName string
	model        string
	wikiStore    *wiki.Store
	wikiDB       *core.DB
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
// constructs the stores from scratch. The returned deps.wikiDB is non-nil
// when a wiki dir is configured and must be closed by the caller.
func buildConsolidationDepsFromCLI(cfg *config.Config) (consolidationDeps, error) {
	provider, model, err := buildProvider(cfg)
	if err != nil {
		return consolidationDeps{}, fmt.Errorf("consolidation setup: build provider: %w", err)
	}
	wikiStore, wikiDB, err := openWikiStoreWithIndex(cfg)
	if err != nil {
		return consolidationDeps{}, fmt.Errorf("consolidation setup: wiki: %w", err)
	}
	if wikiStore == nil {
		return consolidationDeps{}, fmt.Errorf("consolidation setup: wiki dir not configured")
	}
	lessonStore := learning.NewStore(filepath.Join(cfg.DataDir, "lessons.jsonl"))
	deps, err := buildConsolidationDepsFromConfig(cfg, provider, wikiStore, lessonStore, model)
	if err != nil {
		wikiDB.Close()
		return consolidationDeps{}, err
	}
	deps.wikiDB = wikiDB
	return deps, nil
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
