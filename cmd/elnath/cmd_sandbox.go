package main

import (
	"context"
	"fmt"
	"strings"
)

type starterAllowlistGroup struct {
	name     string
	entries  []string
	advanced string
}

var starterAllowlistGroups = []starterAllowlistGroup{
	{
		name:    "git-hosting",
		entries: []string{"github.com:443", "gitlab.com:443", "bitbucket.org:443"},
	},
	{
		name:    "python",
		entries: []string{"pypi.org:443", "files.pythonhosted.org:443"},
	},
	{
		name:    "node",
		entries: []string{"registry.npmjs.org:443"},
	},
	{
		name:    "go",
		entries: []string{"proxy.golang.org:443", "sum.golang.org:443"},
	},
	{
		name:    "rust",
		entries: []string{"crates.io:443", "static.crates.io:443"},
	},
	{
		name:     "containers",
		entries:  []string{"registry-1.docker.io:443", "auth.docker.io:443"},
		advanced: "advanced; registry workflows may require additional domains",
	},
}

func cmdSandbox(_ context.Context, args []string) error {
	if len(args) == 0 {
		return printCommandHelp("sandbox")
	}
	switch args[0] {
	case "print-starter-allowlist":
		return cmdSandboxPrintStarterAllowlist(args[1:])
	case "help":
		return printCommandHelp("sandbox")
	default:
		return fmt.Errorf("unknown sandbox subcommand: %s", args[0])
	}
}

func cmdSandboxPrintStarterAllowlist(args []string) error {
	opts, err := parseStarterAllowlistArgs(args)
	if err != nil {
		return err
	}
	if opts.listGroups || len(opts.groups) == 0 {
		printStarterAllowlistCatalog()
		return nil
	}
	if opts.mode == "" {
		return fmt.Errorf("print-starter-allowlist requires --mode seatbelt or --mode bwrap when --group is provided")
	}
	if opts.mode != "seatbelt" && opts.mode != "bwrap" {
		return fmt.Errorf("unknown sandbox mode %q; valid modes: seatbelt, bwrap", opts.mode)
	}
	groups, err := resolveStarterAllowlistGroups(opts.groups)
	if err != nil {
		return err
	}
	printStarterAllowlistYAML(opts.mode, groups)
	return nil
}

type starterAllowlistOptions struct {
	mode       string
	groups     []string
	listGroups bool
}

func parseStarterAllowlistArgs(args []string) (starterAllowlistOptions, error) {
	var opts starterAllowlistOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--list-groups":
			opts.listGroups = true
		case arg == "--mode":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("print-starter-allowlist: --mode requires a value")
			}
			i++
			opts.mode = strings.ToLower(strings.TrimSpace(args[i]))
		case strings.HasPrefix(arg, "--mode="):
			opts.mode = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--mode=")))
		case arg == "--group":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("print-starter-allowlist: --group requires a value")
			}
			i++
			opts.groups = append(opts.groups, splitGroupList(args[i])...)
		case strings.HasPrefix(arg, "--group="):
			opts.groups = append(opts.groups, splitGroupList(strings.TrimPrefix(arg, "--group="))...)
		default:
			return opts, fmt.Errorf("print-starter-allowlist: unknown flag %q", arg)
		}
	}
	return opts, nil
}

func splitGroupList(raw string) []string {
	parts := strings.Split(raw, ",")
	groups := make([]string, 0, len(parts))
	for _, part := range parts {
		group := strings.TrimSpace(part)
		if group != "" {
			groups = append(groups, group)
		}
	}
	return groups
}

func resolveStarterAllowlistGroups(names []string) ([]starterAllowlistGroup, error) {
	known := make(map[string]starterAllowlistGroup, len(starterAllowlistGroups))
	for _, group := range starterAllowlistGroups {
		known[group.name] = group
	}
	seen := make(map[string]bool, len(names))
	resolved := make([]starterAllowlistGroup, 0, len(names))
	for _, name := range names {
		groupName := strings.TrimSpace(name)
		if groupName == "" || seen[groupName] {
			continue
		}
		group, ok := known[groupName]
		if !ok {
			return nil, fmt.Errorf("unknown starter allowlist group %q", groupName)
		}
		resolved = append(resolved, group)
		seen[groupName] = true
	}
	return resolved, nil
}

func printStarterAllowlistCatalog() {
	fmt.Println("Available starter allowlist groups (explicit opt-in; no config is modified):")
	for _, group := range starterAllowlistGroups {
		label := group.name
		if group.advanced != "" {
			fmt.Printf("  %s: %s; use --group %s to print entries\n", label, group.advanced, group.name)
			continue
		}
		fmt.Printf("  %s: %s\n", label, strings.Join(group.entries, ", "))
	}
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  elnath sandbox print-starter-allowlist --mode seatbelt --group git-hosting,go")
	fmt.Println("  elnath sandbox print-starter-allowlist --mode bwrap --group python,node")
}

func printStarterAllowlistYAML(mode string, groups []starterAllowlistGroup) {
	fmt.Println("sandbox:")
	fmt.Printf("  mode: %s\n", mode)
	fmt.Println("  network_allowlist:")
	seenEntries := make(map[string]bool)
	for _, group := range groups {
		entries := make([]string, 0, len(group.entries))
		for _, entry := range group.entries {
			if seenEntries[entry] {
				continue
			}
			seenEntries[entry] = true
			entries = append(entries, entry)
		}
		if len(entries) == 0 {
			continue
		}
		fmt.Printf("    # %s\n", group.name)
		for _, entry := range entries {
			fmt.Printf("    - %s\n", entry)
		}
	}
	fmt.Println()
	fmt.Println("# Notes:")
	fmt.Println("# - Network allowlist changes require Elnath restart.")
	fmt.Println("# - UDP and QUIC egress are blocked in this sandbox version.")
	fmt.Println("# - DNS rebinding is still not fully defended. Sustained DNS hijack or malicious DNS responses at policy-resolution time remain in scope. If hostile DNS is in scope, enforce egress at a lower layer.")
}
