package main

import (
	"bufio"
	"context"
	"encoding/json"
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

type skillListOutput struct {
	Skills []skillListEntry `json:"skills"`
}

type skillListEntry struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Trigger       string   `json:"trigger,omitempty"`
	RequiredTools []string `json:"required_tools,omitempty"`
	Paths         []string `json:"paths,omitempty"`
	Model         string   `json:"model,omitempty"`
	Effort        string   `json:"effort,omitempty"`
	Status        string   `json:"status,omitempty"`
	Source        string   `json:"source,omitempty"`
	TrustLevel    string   `json:"trust_level,omitempty"`
	External      bool     `json:"external"`
	Hidden        bool     `json:"hidden,omitempty"`
	UserInvocable bool     `json:"user_invocable"`
}

type skillProposalListOutput struct {
	Proposals []skillProposalEntry `json:"proposals"`
}

type skillProposalEntry struct {
	FileName        string   `json:"file_name"`
	Path            string   `json:"path"`
	SkillName       string   `json:"skill_name"`
	SessionID       string   `json:"session_id,omitempty"`
	Reason          string   `json:"reason"`
	Evidence        []string `json:"evidence,omitempty"`
	SuggestedChange string   `json:"suggested_change"`
	CreatedAt       string   `json:"created_at,omitempty"`
	ModTime         string   `json:"mod_time,omitempty"`
}

func cmdSkill(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return cmdSkillList(ctx, nil)
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Println(skillUsage())
		return nil
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
	case "proposals":
		return cmdSkillProposals(ctx, args[1:])
	default:
		return fmt.Errorf("unknown skill subcommand: %q (try: list, show, create, delete, edit, stats, proposals)", args[0])
	}
}

func skillUsage() string {
	return `Usage: elnath skill <subcommand>

Subcommands:
  list [--json] [--all] [--compatible]  List installed skills
  show <name>                           Show a skill definition
  create <name>                         Create a new skill
  delete <name>                         Delete a skill
  edit <name>                           Open a skill in $EDITOR
  stats                                 Show skill registry stats
  proposals <list|show|apply>           Review skill improvement proposals`
}

func cmdSkillList(_ context.Context, args []string) error {
	showAll := hasFlag(args, "--all")
	includeCompatible := hasFlag(args, "--compatible")
	asJSON := hasFlag(args, "--json")
	skills, err := loadSkillList(showAll, includeCompatible)
	if err != nil {
		return err
	}

	if asJSON {
		raw, err := json.MarshalIndent(skillListOutput{Skills: skillListEntries(skills)}, "", "  ")
		if err != nil {
			return fmt.Errorf("skill list: marshal JSON: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}
	if len(skills) == 0 {
		fmt.Println("No skills found.")
		return nil
	}
	for _, sk := range skills {
		var markers []string
		if sk.Status == "draft" {
			markers = append(markers, "draft")
		}
		if !sk.UserInvocable() {
			markers = append(markers, "hidden")
		}
		if includeCompatible && sk.Source != "" {
			markers = append(markers, sk.Source)
		}
		suffix := ""
		if len(markers) > 0 {
			suffix = " [" + strings.Join(markers, ", ") + "]"
		}
		fmt.Printf("  /%s — %s%s\n", sk.Name, sk.Description, suffix)
	}
	return nil
}

func loadSkillList(showAll, includeCompatible bool) ([]*skill.Skill, error) {
	cfg, err := loadSkillConfig()
	if err != nil {
		return nil, err
	}
	store, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return nil, err
	}
	wikiSkills, err := skill.ListAllFromStore(store)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]*skill.Skill)
	for _, sk := range wikiSkills {
		if sk == nil {
			continue
		}
		if !showAll && sk.Status == "draft" {
			continue
		}
		byName[sk.Name] = sk
	}

	if includeCompatible {
		projectRoot, _ := os.Getwd()
		homeDir, _ := os.UserHomeDir()
		rootOpts := skill.CompatibleSkillRootOptions{DisablePluginCache: !config.SkillsPluginCacheEnabled(cfg)}
		for _, root := range skill.DefaultCompatibleSkillRootsWithOptions(projectRoot, homeDir, rootOpts) {
			compatibleSkills, err := skill.LoadCompatibleSkillRoot(root)
			if err != nil {
				return nil, err
			}
			for _, sk := range compatibleSkills {
				if sk == nil {
					continue
				}
				if !showAll && sk.Status == "draft" {
					continue
				}
				byName[sk.Name] = sk
			}
		}
	}

	skills := make([]*skill.Skill, 0, len(byName))
	for _, sk := range byName {
		skills = append(skills, sk)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

func skillListEntries(skills []*skill.Skill) []skillListEntry {
	out := make([]skillListEntry, 0, len(skills))
	for _, sk := range skills {
		if sk == nil {
			continue
		}
		out = append(out, skillListEntry{
			Name:          sk.Name,
			Description:   sk.Description,
			Trigger:       sk.Trigger,
			RequiredTools: append([]string(nil), sk.RequiredTools...),
			Paths:         append([]string(nil), sk.Paths...),
			Model:         sk.Model,
			Effort:        sk.Effort,
			Status:        sk.Status,
			Source:        sk.Source,
			TrustLevel:    sk.TrustLevel(),
			External:      sk.External(),
			Hidden:        !sk.UserInvocable(),
			UserInvocable: sk.UserInvocable(),
		})
	}
	return out
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
	store, db, err := openWikiStoreWithIndex(cfg)
	if err != nil {
		return err
	}
	if store == nil {
		return fmt.Errorf("skill create: wiki dir is not configured")
	}
	defer db.Close()
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
	store, db, err := openWikiStoreWithIndex(cfg)
	if err != nil {
		return err
	}
	if store == nil {
		return fmt.Errorf("skill delete: wiki dir is not configured")
	}
	defer db.Close()
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
	store, db, err := openWikiStoreWithIndex(cfg)
	if err != nil {
		return err
	}
	if store == nil {
		return fmt.Errorf("skill edit: wiki dir is not configured")
	}
	defer db.Close()

	relPath := filepath.Join("skills", args[0]+".md")
	absPath := filepath.Join(cfg.WikiDir, relPath)
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, absPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if err := store.ResyncIndex(relPath); err != nil {
		return fmt.Errorf("skill edit: sync index: %w", err)
	}
	return nil
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

func cmdSkillProposals(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return cmdSkillProposalsList(ctx, nil)
	}
	switch args[0] {
	case "list":
		return cmdSkillProposalsList(ctx, args[1:])
	case "show":
		return cmdSkillProposalsShow(ctx, args[1:])
	case "apply":
		return cmdSkillProposalsApply(ctx, args[1:])
	default:
		return fmt.Errorf("unknown skill proposals subcommand: %q (try: list, show, apply)", args[0])
	}
}

func cmdSkillProposalsList(_ context.Context, args []string) error {
	asJSON := hasFlag(args, "--json")
	cfg, err := loadSkillConfig()
	if err != nil {
		return err
	}
	proposals, err := skill.NewTracker(cfg.DataDir).ListImprovementProposals()
	if err != nil {
		return err
	}
	entries := skillProposalEntries(proposals)
	if asJSON {
		raw, err := json.MarshalIndent(skillProposalListOutput{Proposals: entries}, "", "  ")
		if err != nil {
			return fmt.Errorf("skill proposals list: marshal JSON: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}
	if len(entries) == 0 {
		fmt.Println("No skill improvement proposals.")
		return nil
	}
	fmt.Println("Skill improvement proposals:")
	for _, entry := range entries {
		fmt.Printf("  %s  /%s  %s\n", entry.FileName, entry.SkillName, entry.Reason)
	}
	return nil
}

func cmdSkillProposalsShow(_ context.Context, args []string) error {
	asJSON := hasFlag(args, "--json")
	path := firstNonFlagArg(args)
	if path == "" {
		return fmt.Errorf("usage: elnath skill proposals show <proposal-file> [--json]")
	}
	cfg, err := loadSkillConfig()
	if err != nil {
		return err
	}
	proposal, err := skill.NewTracker(cfg.DataDir).ReadImprovementProposal(path)
	if err != nil {
		return err
	}
	entry := skillProposalEntryFromProposal(path, proposal)
	if asJSON {
		raw, err := json.MarshalIndent(entry, "", "  ")
		if err != nil {
			return fmt.Errorf("skill proposals show: marshal JSON: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}
	fmt.Printf("File:             %s\n", entry.FileName)
	fmt.Printf("Skill:            /%s\n", entry.SkillName)
	if entry.SessionID != "" {
		fmt.Printf("Session:          %s\n", entry.SessionID)
	}
	fmt.Printf("Reason:           %s\n", entry.Reason)
	if len(entry.Evidence) > 0 {
		fmt.Println("Evidence:")
		for _, evidence := range entry.Evidence {
			fmt.Printf("  - %s\n", evidence)
		}
	}
	fmt.Printf("Suggested change: %s\n", entry.SuggestedChange)
	return nil
}

func cmdSkillProposalsApply(_ context.Context, args []string) error {
	approved := hasFlag(args, "--yes")
	path := firstNonFlagArg(args)
	if path == "" {
		return fmt.Errorf("usage: elnath skill proposals apply <proposal-file> [--yes]")
	}
	cfg, err := loadSkillConfig()
	if err != nil {
		return err
	}
	tracker := skill.NewTracker(cfg.DataDir)
	proposal, err := tracker.ReadImprovementProposal(path)
	if err != nil {
		return err
	}
	if !approved {
		reader := bufio.NewReader(os.Stdin)
		confirm, err := readSkillInput(reader, fmt.Sprintf("Apply proposal %q to /%s? (y/N): ", filepath.Base(path), proposal.SkillName))
		if err != nil {
			return err
		}
		if confirm != "y" && confirm != "Y" {
			fmt.Println("Cancelled.")
			return nil
		}
	}
	store, db, err := openWikiStoreWithIndex(cfg)
	if err != nil {
		return err
	}
	if store == nil {
		return fmt.Errorf("skill proposals apply: wiki dir is not configured")
	}
	defer db.Close()
	creator := skill.NewCreator(store, tracker, nil)
	sk, err := creator.ApplyImprovementProposal(path)
	if err != nil {
		return err
	}
	fmt.Printf("Applied proposal %s to /%s\n", filepath.Base(path), sk.Name)
	return nil
}

func skillProposalEntries(files []skill.ImprovementProposalFile) []skillProposalEntry {
	entries := make([]skillProposalEntry, 0, len(files))
	for _, file := range files {
		entry := skillProposalEntryFromProposal(file.FileName, file.Proposal)
		entry.Path = file.Path
		if !file.ModTime.IsZero() {
			entry.ModTime = file.ModTime.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		entries = append(entries, entry)
	}
	return entries
}

func skillProposalEntryFromProposal(path string, proposal skill.ImprovementProposal) skillProposalEntry {
	entry := skillProposalEntry{
		FileName:        filepath.Base(path),
		SkillName:       proposal.SkillName,
		SessionID:       proposal.SessionID,
		Reason:          proposal.Reason,
		Evidence:        append([]string(nil), proposal.Evidence...),
		SuggestedChange: proposal.SuggestedChange,
	}
	if !proposal.CreatedAt.IsZero() {
		entry.CreatedAt = proposal.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return entry
}

func firstNonFlagArg(args []string) string {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg != "" && !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
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
