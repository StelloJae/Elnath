package wiki

import (
	"context"
	"sort"
)

// CrossProjectSearcher searches across multiple wiki indices.
type CrossProjectSearcher struct {
	indices map[string]*Index // project name → wiki index
}

// NewCrossProjectSearcher creates an empty CrossProjectSearcher.
func NewCrossProjectSearcher() *CrossProjectSearcher {
	return &CrossProjectSearcher{indices: make(map[string]*Index)}
}

// AddProject registers a wiki index under the given project name.
func (s *CrossProjectSearcher) AddProject(name string, idx *Index) {
	s.indices[name] = idx
}

// Len returns the number of registered projects.
func (s *CrossProjectSearcher) Len() int {
	return len(s.indices)
}

// CrossProjectResult wraps a SearchResult with the originating project name.
type CrossProjectResult struct {
	Project string
	SearchResult
}

// Search searches all registered project wikis and returns combined results
// sorted by score descending, capped at limit.
// Failed individual project searches are skipped without surfacing an error.
func (s *CrossProjectSearcher) Search(ctx context.Context, query string, limit int) ([]CrossProjectResult, error) {
	var all []CrossProjectResult
	for name, idx := range s.indices {
		results, err := idx.Search(ctx, SearchOpts{Query: query, Limit: limit})
		if err != nil {
			continue // skip failed projects
		}
		for _, r := range results {
			all = append(all, CrossProjectResult{Project: name, SearchResult: r})
		}
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}
