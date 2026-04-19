package main

import (
	"fmt"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/wiki"
)

// openWikiStoreWithIndex opens the wiki DB, builds an Index, and returns a
// Store wired with WithIndex(idx) so Create/Update/Delete mirror their
// filesystem writes into SQLite (and FTS5 via triggers).
//
// Returns (nil, nil, nil) when cfg.WikiDir is blank — callers should check
// store before use. The returned *core.DB must be closed by the caller
// (typically `defer db.Close()`); on setup failure the helper closes the
// DB itself before returning the error.
func openWikiStoreWithIndex(cfg *config.Config) (*wiki.Store, *core.DB, error) {
	if cfg.WikiDir == "" {
		return nil, nil, nil
	}
	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("open wiki db: %w", err)
	}
	idx, err := wiki.NewIndex(db.Wiki)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("open wiki index: %w", err)
	}
	store, err := wiki.NewStore(cfg.WikiDir, wiki.WithIndex(idx))
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("open wiki store: %w", err)
	}
	return store, db, nil
}
