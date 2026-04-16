package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/profile"
	"github.com/stello/elnath/internal/wiki"
)

func cmdProfile(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return cmdProfileList(ctx, nil)
	}

	switch args[0] {
	case "list":
		return cmdProfileList(ctx, args[1:])
	case "show":
		return cmdProfileShow(ctx, args[1:])
	default:
		return fmt.Errorf("unknown profile subcommand: %q (try: list, show)", args[0])
	}
}

func cmdProfileList(_ context.Context, _ []string) error {
	cfg, err := loadProfileConfig()
	if err != nil {
		return err
	}
	store, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return err
	}

	profiles, err := profile.LoadAll(store)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		fmt.Println("No profiles found.")
		return nil
	}

	for _, name := range profile.SortedNames(profiles) {
		p := profiles[name]
		model := p.Model
		if model == "" {
			model = "-"
		}
		fmt.Printf("  %-20s model=%-20s tools=%d\n", p.Name, model, len(p.Tools))
	}
	return nil
}

func cmdProfileShow(_ context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath profile show <name>")
	}
	cfg, err := loadProfileConfig()
	if err != nil {
		return err
	}
	store, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return err
	}

	profiles, err := profile.LoadAll(store)
	if err != nil {
		return err
	}
	p, ok := profiles[args[0]]
	if !ok {
		return fmt.Errorf("profile %q not found", args[0])
	}

	tools := strings.Join(p.Tools, ", ")
	if tools == "" {
		tools = "-"
	}
	model := p.Model
	if model == "" {
		model = "-"
	}
	fmt.Printf("Name:           %s\n", p.Name)
	fmt.Printf("Model:          %s\n", model)
	fmt.Printf("Tools:          %s\n", tools)
	fmt.Printf("MaxIterations:  %d\n", p.MaxIterations)
	fmt.Printf("\n--- System Extra ---\n%s\n", p.SystemExtra)
	return nil
}

func loadProfileConfig() (*config.Config, error) {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	return config.Load(cfgPath)
}
