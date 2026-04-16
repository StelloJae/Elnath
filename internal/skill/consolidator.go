package skill

import (
	"context"
	"time"

	"github.com/stello/elnath/internal/wiki"
)

type ConsolidatorConfig struct {
	MinSessions   int
	MinPrevalence int
	MaxDraftAge   time.Duration
}

func DefaultConsolidatorConfig() ConsolidatorConfig {
	return ConsolidatorConfig{
		MinSessions:   5,
		MinPrevalence: 2,
		MaxDraftAge:   90 * 24 * time.Hour,
	}
}

type DefaultConsolidator struct {
	creator  *Creator
	tracker  *Tracker
	registry *Registry
	store    *wiki.Store
	config   ConsolidatorConfig
}

func NewConsolidator(creator *Creator, tracker *Tracker, registry *Registry, store *wiki.Store, config ConsolidatorConfig) *DefaultConsolidator {
	return &DefaultConsolidator{
		creator:  creator,
		tracker:  tracker,
		registry: registry,
		store:    store,
		config:   config,
	}
}

func (c *DefaultConsolidator) Run(ctx context.Context) (*ConsolidationResult, error) {
	drafts, err := c.loadDrafts()
	if err != nil {
		return nil, err
	}
	patterns, err := c.tracker.LoadPatterns()
	if err != nil {
		return nil, err
	}
	return c.consolidateDrafts(ctx, drafts, patterns)
}

func (c *DefaultConsolidator) consolidateDrafts(ctx context.Context, drafts []*Skill, patterns []PatternRecord) (*ConsolidationResult, error) {
	result := &ConsolidationResult{}
	for _, draft := range drafts {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		if draft == nil || draft.Name == "" {
			continue
		}

		matched := matchingPatterns(draft.Name, patterns)
		prevalence := len(matched)
		totalSessions := distinctSessionCount(matched)

		if prevalence >= c.config.MinPrevalence && totalSessions >= c.config.MinSessions {
			if err := c.creator.Promote(draft.Name); err != nil {
				return nil, err
			}
			result.Promoted = append(result.Promoted, draft.Name)
			continue
		}

		expired, err := c.isDraftExpired(draft.Name)
		if err != nil {
			return nil, err
		}
		if expired {
			if err := c.creator.Delete(draft.Name); err != nil {
				return nil, err
			}
			result.Cleaned = append(result.Cleaned, draft.Name)
		}
	}
	return result, nil
}

func (c *DefaultConsolidator) loadDrafts() ([]*Skill, error) {
	pages, err := c.store.List()
	if err != nil {
		return nil, err
	}

	drafts := make([]*Skill, 0, len(pages))
	for _, page := range pages {
		sk := FromPage(page)
		if sk == nil || sk.Status != "draft" {
			continue
		}
		drafts = append(drafts, sk)
	}
	return drafts, nil
}

func (c *DefaultConsolidator) isDraftExpired(name string) (bool, error) {
	page, err := c.store.Read(skillPagePath(name))
	if err != nil {
		return false, err
	}
	return time.Since(page.Created) > c.config.MaxDraftAge, nil
}

func matchingPatterns(name string, patterns []PatternRecord) []PatternRecord {
	matched := make([]PatternRecord, 0, len(patterns))
	for _, pattern := range patterns {
		if pattern.DraftSkill == name {
			matched = append(matched, pattern)
		}
	}
	return matched
}

func distinctSessionCount(patterns []PatternRecord) int {
	sessions := make(map[string]struct{})
	for _, pattern := range patterns {
		for _, sessionID := range pattern.SessionIDs {
			sessions[sessionID] = struct{}{}
		}
	}
	return len(sessions)
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
