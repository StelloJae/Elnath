package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/userfacingerr"
	"github.com/stello/elnath/internal/wiki"
)

func cmdWiki(ctx context.Context, args []string) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if _, err := os.Stat(cfg.WikiDir); err != nil {
		return userfacingerr.Wrap(userfacingerr.ELN010, err, "wiki dir")
	}

	store, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return fmt.Errorf("wiki store: %w", err)
	}

	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	idx, err := wiki.NewIndex(db.Wiki)
	if err != nil {
		return fmt.Errorf("wiki index: %w", err)
	}

	if len(args) == 0 {
		fmt.Println(`Usage: elnath wiki <subcommand> [args]

Subcommands:
  search <query>   Search wiki pages
  lint             Check wiki health
  rebuild          Rebuild FTS index
  list             List all pages`)
		return nil
	}

	switch args[0] {
	case "search":
		if len(args) < 2 {
			return fmt.Errorf("usage: elnath wiki search <query>")
		}
		query := strings.Join(args[1:], " ")
		results, err := idx.Search(ctx, wiki.SearchOpts{Query: query, Limit: 10})
		if err != nil {
			return fmt.Errorf("wiki search: %w", err)
		}
		if len(results) == 0 {
			fmt.Println("No results found.")
			return nil
		}
		for i, r := range results {
			fmt.Printf("%d. [%.2f] %s — %s\n", i+1, r.Score, r.Page.Path, r.Page.Title)
			for _, h := range r.Highlights {
				fmt.Printf("   %s\n", h)
			}
		}

	case "lint":
		linter := wiki.NewLinter(store, idx)
		issues, err := linter.Lint(ctx)
		if err != nil {
			return fmt.Errorf("wiki lint: %w", err)
		}
		if len(issues) == 0 {
			fmt.Println("Wiki is healthy — no issues found.")
			return nil
		}
		for _, issue := range issues {
			fmt.Printf("[%s] %s: %s\n", issue.Severity, issue.Path, issue.Message)
		}

	case "rebuild":
		if err := idx.Rebuild(store); err != nil {
			return fmt.Errorf("wiki rebuild: %w", err)
		}
		fmt.Println("Wiki FTS index rebuilt.")

	case "list":
		pages, err := store.List()
		if err != nil {
			return fmt.Errorf("wiki list: %w", err)
		}
		if len(pages) == 0 {
			fmt.Println("No wiki pages found.")
			return nil
		}
		for _, p := range pages {
			fmt.Printf("  %s — %s [%s]\n", p.Path, p.Title, p.Type)
		}

	default:
		return fmt.Errorf("unknown wiki subcommand: %s", args[0])
	}

	return nil
}

func cmdSearch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath search <query>")
	}
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	if err := conversation.InitSchema(db.Main); err != nil {
		return fmt.Errorf("init conversation schema: %w", err)
	}

	store := conversation.NewHistoryStore(db.Main)
	query := strings.Join(args, " ")
	results, err := store.Search(ctx, query, 20)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	for i, r := range results {
		fmt.Printf("%d. [%s] session:%s (%s)\n   %s\n",
			i+1, r.Role, r.SessionID,
			r.CreatedAt.Format("2006-01-02 15:04"),
			r.Snippet)
	}
	return nil
}
