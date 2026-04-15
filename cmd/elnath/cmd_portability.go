package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/stello/elnath/internal/portability"
)

func cmdPortability(ctx context.Context, args []string) error {
	var err error
	args, err = stripPortabilityGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return printPortabilityHelp()
	}
	switch args[0] {
	case "export":
		return runPortabilityExport(ctx, args[1:])
	case "import":
		return runPortabilityImport(ctx, args[1:])
	case "list":
		return runPortabilityList(ctx, args[1:])
	case "verify":
		return runPortabilityVerify(ctx, args[1:])
	case "help", "--help", "-h":
		return printPortabilityHelp()
	default:
		return fmt.Errorf("unknown portability subcommand: %s", args[0])
	}
}

func printPortabilityHelp() error {
	fmt.Println(`Usage: elnath portability <subcommand> [args]

Subcommands:
  export    Create an encrypted portability bundle
  import    Restore a portability bundle into a target directory
  list      Show local export history
  verify    Decrypt and verify a portability bundle

Global flags:
  --data-dir <path>   Portability root (default: $HOME/.elnath)`)
	return nil
}

func runPortabilityExport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("portability-export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	outPath := fs.String("out", "", "")
	passFile := fs.String("passphrase-file", "", "")
	scopeRaw := fs.String("scope", "", "")
	if err := fs.Parse(normalizePortabilityArgs(args, map[string]bool{"--out": true, "--passphrase-file": true, "--scope": true})); err != nil {
		return err
	}
	if *outPath == "" {
		return fmt.Errorf("usage: elnath portability export --out <bundle.eln>")
	}
	scope, err := parsePortabilityScope(*scopeRaw)
	if err != nil {
		return err
	}
	passphrase, err := readPortabilityPassphrase(*passFile)
	if err != nil {
		return err
	}
	if err := validatePortabilityPassphrase(passphrase); err != nil {
		return err
	}
	root := portabilityRootDir()
	if err := portability.Export(ctx, portability.ExportOptions{
		DataDir:    root,
		WikiDir:    portabilityWikiDir(root),
		OutPath:    *outPath,
		Passphrase: passphrase,
		Scope:      scope,
		Logger:     slog.Default(),
	}); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Exported bundle to %s\n", *outPath)
	fmt.Fprintln(os.Stdout, "Store passphrase in password manager. Loss = unrecoverable.")
	return nil
}

func runPortabilityImport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("portability-import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	passFile := fs.String("passphrase-file", "", "")
	dryRun := fs.Bool("dry-run", false, "")
	force := fs.Bool("force", false, "")
	target := fs.String("target", "", "")
	if err := fs.Parse(normalizePortabilityArgs(args, map[string]bool{"--passphrase-file": true, "--target": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: elnath portability import <bundle.eln>")
	}
	passphrase, err := readPortabilityPassphrase(*passFile)
	if err != nil {
		return err
	}
	targetDir := *target
	if targetDir == "" {
		targetDir = portabilityRootDir()
	}
	report, err := portability.Import(ctx, portability.ImportOptions{
		BundlePath: fs.Arg(0),
		TargetDir:  targetDir,
		Passphrase: passphrase,
		DryRun:     *dryRun,
		Force:      *force,
		Logger:     slog.Default(),
	})
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Fprintf(os.Stdout, "would apply %d files\n", len(report.FilesSkipped))
	} else {
		fmt.Fprintf(os.Stdout, "applied %d files\n", len(report.FilesApplied))
	}
	for _, warning := range report.Warnings {
		fmt.Fprintln(os.Stderr, warning)
	}
	return nil
}

func runPortabilityList(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: elnath portability list")
	}
	records, err := portability.List(ctx, portabilityRootDir())
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(os.Stdout, "No portability exports found.")
		return nil
	}
	for _, record := range records {
		fmt.Fprintf(os.Stdout, "%s  %s  %d bytes\n", record.Timestamp.Format("2006-01-02 15:04:05"), record.OutPath, record.ByteSize)
	}
	return nil
}

func runPortabilityVerify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("portability-verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	passFile := fs.String("passphrase-file", "", "")
	if err := fs.Parse(normalizePortabilityArgs(args, map[string]bool{"--passphrase-file": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: elnath portability verify <bundle.eln>")
	}
	passphrase, err := readPortabilityPassphrase(*passFile)
	if err != nil {
		return err
	}
	report, err := portability.Verify(ctx, portability.VerifyOptions{
		BundlePath: fs.Arg(0),
		Passphrase: passphrase,
		Logger:     slog.Default(),
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "PASS: files=%d bytes=%d\n", report.FileCount, report.TotalBytes)
	for _, warning := range report.HostWarnings {
		fmt.Fprintln(os.Stderr, warning)
	}
	return nil
}

func parsePortabilityScope(raw string) (portability.BundleScope, error) {
	if strings.TrimSpace(raw) == "" {
		return portability.BundleScope{}, nil
	}
	var scope portability.BundleScope
	for _, part := range strings.Split(raw, ",") {
		switch strings.TrimSpace(part) {
		case "config":
			scope.Config = true
		case "db":
			scope.DB = true
		case "wiki":
			scope.Wiki = true
		case "lessons":
			scope.Lessons = true
		case "sessions":
			scope.Sessions = true
		default:
			return portability.BundleScope{}, fmt.Errorf("unknown portability scope: %s", strings.TrimSpace(part))
		}
	}
	return scope, nil
}

func portabilityRootDir() string {
	if root := extractDataDirFlag(os.Args); root != "" {
		return root
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".elnath")
}

func portabilityWikiDir(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, "config.yaml"))
	if err == nil {
		var cfg struct {
			WikiDir string `yaml:"wiki_dir"`
		}
		if yaml.Unmarshal(raw, &cfg) == nil && strings.TrimSpace(cfg.WikiDir) != "" {
			if filepath.IsAbs(cfg.WikiDir) {
				return cfg.WikiDir
			}
			return filepath.Join(root, cfg.WikiDir)
		}
	}
	return filepath.Join(root, "wiki")
}

func readPortabilityPassphrase(passFile string) ([]byte, error) {
	if passFile != "" {
		raw, err := os.ReadFile(passFile)
		if err != nil {
			return nil, fmt.Errorf("read passphrase file: %w", err)
		}
		return []byte(strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])), nil
	}
	if envValue := os.Getenv("ELNATH_PORTABILITY_PASSPHRASE"); envValue != "" {
		slog.Warn("using portability passphrase from environment")
		fmt.Fprintln(os.Stderr, "warning: using ELNATH_PORTABILITY_PASSPHRASE exposes the secret to child processes")
		return []byte(strings.TrimSpace(envValue)), nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, fmt.Errorf("passphrase required: use --passphrase-file or ELNATH_PORTABILITY_PASSPHRASE")
	}
	fmt.Fprint(os.Stderr, "Passphrase: ")
	passphrase, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("read passphrase: %w", err)
	}
	return []byte(strings.TrimSpace(string(passphrase))), nil
}

func validatePortabilityPassphrase(passphrase []byte) error {
	warning, fatal := portability.ValidatePassphrase(passphrase)
	if fatal != nil {
		return fatal
	}
	if warning == nil {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, warning.Error())
		return nil
	}
	fmt.Fprint(os.Stderr, "Continue with weak passphrase? [y/N] ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return fmt.Errorf("read weak-passphrase confirmation: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(line), "y") {
		return nil
	}
	return fmt.Errorf("weak passphrase rejected")
}

func normalizePortabilityArgs(args []string, flagsWithValue map[string]bool) []string {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if flagsWithValue[arg] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return append(flags, positionals...)
}

func stripPortabilityGlobalFlags(args []string) ([]string, error) {
	for len(args) > 0 {
		if args[0] != "--data-dir" {
			return args, nil
		}
		if len(args) < 2 {
			return nil, fmt.Errorf("usage: elnath portability --data-dir <path> <subcommand>")
		}
		args = args[2:]
	}
	return args, nil
}
