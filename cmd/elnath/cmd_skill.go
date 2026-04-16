package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/skill"
	"github.com/stello/elnath/internal/wiki"
)

func cmdSkill(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return cmdSkillList(ctx, nil)
	}

	switch args[0] {
	case "list":
		return cmdSkillList(ctx, args[1:])
	case "show":
		return cmdSkillShow(ctx, args[1:])
	case "create":
		return cmdSkillCreate(ctx, args[1:])
	case "delete":
		return cmdSkillDelete(ctx, args[1:])
	case "edit":
		return cmdSkillEdit(ctx, args[1:])
	case "stats":
		return cmdSkillStats(ctx, args[1:])
	default:
		return fmt.Errorf("unknown skill subcommand: %q (try: list, show, create, delete, edit, stats)", args[0])
	}
}

func cmdSkillList(_ context.Context, args []string) error {
	cfg, err := loadSkillConfig()
	if err != nil {
		return err
	}
	store, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return err
	}
	showAll := hasFlag(args, "--all")
	pages, err := store.List()
	if err != nil {
		return err
	}

	var skills []*skill.Skill
	for _, page := range pages {
		sk := skill.FromPage(page)
		if sk == nil {
			continue
		}
		if !showAll && sk.Status == "draft" {
			continue
		}
		skills = append(skills, sk)
	}
	if len(skills) == 0 {
		fmt.Println("No skills found.")
		return nil
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	for _, sk := range skills {
		suffix := ""
		if sk.Status == "draft" {
			suffix = " [draft]"
		}
		fmt.Printf("  /%s — %s%s\n", sk.Name, sk.Description, suffix)
	}
	return nil
}

func cmdSkillShow(_ context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath skill show <name>")
	}
	if err := skill.ValidateSkillName(args[0]); err != nil {
		return err
	}
	cfg, err := loadSkillConfig()
	if err != nil {
		return err
	}
	store, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return err
	}
	page, err := store.Read(filepath.Join("skills", args[0]+".md"))
	if err != nil {
		return fmt.Errorf("skill %q not found", args[0])
	}
	sk := skill.FromPage(page)
	if sk == nil {
		return fmt.Errorf("page is not a valid skill")
	}
	tools := strings.Join(sk.RequiredTools, ", ")
	if tools == "" {
		tools = "-"
	}
	model := sk.Model
	if model == "" {
		model = "-"
	}
	source := sk.Source
	if source == "" {
		source = "-"
	}
	fmt.Printf("Name:        %s\n", sk.Name)
	fmt.Printf("Description: %s\n", sk.Description)
	fmt.Printf("Trigger:     %s\n", sk.Trigger)
	fmt.Printf("Status:      %s\n", sk.Status)
	fmt.Printf("Source:      %s\n", source)
	fmt.Printf("Tools:       %s\n", tools)
	fmt.Printf("Model:       %s\n", model)
	fmt.Printf("\n--- Prompt ---\n%s\n", sk.Prompt)
	return nil
}

func cmdSkillCreate(_ context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath skill create <name>")
	}
	if err := skill.ValidateSkillName(args[0]); err != nil {
		return err
	}
	cfg, err := loadSkillConfig()
	if err != nil {
		return err
	}
	store, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return err
	}
	creator := skill.NewCreator(store, skill.NewTracker(cfg.DataDir), nil)
	reader := bufio.NewReader(os.Stdin)

	desc, err := readSkillInput(reader, "Description: ")
	if err != nil {
		return err
	}
	trigger, err := readSkillInput(reader, "Trigger (e.g. /name <arg>): ")
	if err != nil {
		return err
	}
	prompt, err := readSkillInput(reader, "Prompt: ")
	if err != nil {
		return err
	}

	sk, err := creator.Create(skill.CreateParams{
		Name:        args[0],
		Description: desc,
		Trigger:     trigger,
		Prompt:      prompt,
		Status:      "active",
		Source:      "user",
	})
	if err != nil {
		return err
	}
	fmt.Printf("Created skill: /%s\n", sk.Name)
	return nil
}

func cmdSkillDelete(_ context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath skill delete <name>")
	}
	if err := skill.ValidateSkillName(args[0]); err != nil {
		return err
	}
	cfg, err := loadSkillConfig()
	if err != nil {
		return err
	}
	store, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return err
	}
	creator := skill.NewCreator(store, skill.NewTracker(cfg.DataDir), nil)
	reader := bufio.NewReader(os.Stdin)

	confirm, err := readSkillInput(reader, fmt.Sprintf("Delete skill %q? (y/N): ", args[0]))
	if err != nil {
		return err
	}
	if confirm != "y" && confirm != "Y" {
		fmt.Println("Cancelled.")
		return nil
	}
	if err := creator.Delete(args[0]); err != nil {
		return err
	}
	fmt.Printf("Deleted skill: %s\n", args[0])
	return nil
}

func cmdSkillEdit(_ context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath skill edit <name>")
	}
	if err := skill.ValidateSkillName(args[0]); err != nil {
		return err
	}
	cfg, err := loadSkillConfig()
	if err != nil {
		return err
	}
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
	}
	path := filepath.Join(cfg.WikiDir, "skills", args[0]+".md")
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func cmdSkillStats(_ context.Context, _ []string) error {
	cfg, err := loadSkillConfig()
	if err != nil {
		return err
	}
	stats, err := skill.NewTracker(cfg.DataDir).UsageStats()
	if err != nil {
		return err
	}
	if len(stats) == 0 {
		fmt.Println("No skill usage recorded yet.")
		return nil
	}
	names := make([]string, 0, len(stats))
	for name := range stats {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Println("Skill usage:")
	for _, name := range names {
		fmt.Printf("  %-20s %d invocations\n", name, stats[name])
	}
	return nil
}

func loadSkillConfig() (*config.Config, error) {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	return config.Load(cfgPath)
}

func readSkillInput(reader *bufio.Reader, label string) (string, error) {
	fmt.Print(label)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
