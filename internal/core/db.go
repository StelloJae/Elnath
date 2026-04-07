package core

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	Main *sql.DB // elnath.db: conversation + usage + queue
	Wiki *sql.DB // wiki.db: pages + FTS5
}

func OpenDB(dataDir string) (*DB, error) {
	mainPath := filepath.Join(dataDir, "elnath.db")
	mainDB, err := openSQLite(mainPath)
	if err != nil {
		return nil, fmt.Errorf("open main db: %w", err)
	}

	wikiPath := filepath.Join(dataDir, "wiki.db")
	wikiDB, err := openSQLite(wikiPath)
	if err != nil {
		mainDB.Close()
		return nil, fmt.Errorf("open wiki db: %w", err)
	}

	return &DB{Main: mainDB, Wiki: wikiDB}, nil
}

func (db *DB) Close() error {
	if db == nil {
		return nil
	}
	var errs []error
	if db.Main != nil {
		errs = append(errs, db.Main.Close())
	}
	if db.Wiki != nil {
		errs = append(errs, db.Wiki.Close())
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("exec %q: %w", p, err)
		}
	}

	return db, nil
}

// HasFTS5 checks if the FTS5 extension is available.
// Falls back to LIKE queries if not available (Stella lcm/schema.go pattern).
func HasFTS5(db *sql.DB) bool {
	_, err := db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS _fts5_test USING fts5(test)")
	if err != nil {
		return false
	}
	db.Exec("DROP TABLE IF EXISTS _fts5_test")
	return true
}
