package conversation

import (
	"context"
	"sort"
)

// CrossProjectConversationSearcher searches conversation history across multiple projects.
type CrossProjectConversationSearcher struct {
	stores map[string]*DBHistoryStore // project name → store
}

// NewCrossProjectConversationSearcher creates an empty CrossProjectConversationSearcher.
func NewCrossProjectConversationSearcher() *CrossProjectConversationSearcher {
	return &CrossProjectConversationSearcher{stores: make(map[string]*DBHistoryStore)}
}

// AddProject registers a history store under the given project name.
func (s *CrossProjectConversationSearcher) AddProject(name string, store *DBHistoryStore) {
	s.stores[name] = store
}

// Len returns the number of registered projects.
func (s *CrossProjectConversationSearcher) Len() int {
	return len(s.stores)
}

// CrossProjectConversationResult wraps a HistoryResult with the originating project name.
type CrossProjectConversationResult struct {
	Project string
	HistoryResult
}

// Search searches all registered project conversation stores and returns combined
// results sorted by creation time descending, capped at limit.
// Failed individual project searches are skipped without surfacing an error.
func (s *CrossProjectConversationSearcher) Search(ctx context.Context, query string, limit int) ([]CrossProjectConversationResult, error) {
	var all []CrossProjectConversationResult
	for name, store := range s.stores {
		results, err := store.Search(ctx, query, limit)
		if err != nil {
			continue // skip failed projects
		}
		for _, r := range results {
			all = append(all, CrossProjectConversationResult{Project: name, HistoryResult: r})
		}
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}
