package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/llm"
)

type doctorStatus string

const (
	doctorStatusPass doctorStatus = "pass"
	doctorStatusWarn doctorStatus = "warn"
	doctorStatusFail doctorStatus = "fail"
)

type doctorCheck struct {
	Name    string       `json:"name"`
	Status  doctorStatus `json:"status"`
	Summary string       `json:"summary"`
	Detail  string       `json:"detail,omitempty"`
}

type doctorReport struct {
	Status doctorStatus  `json:"status"`
	OK     bool          `json:"ok"`
	Checks []doctorCheck `json:"checks"`
}

func cmdDoctor(_ context.Context, args []string) error {
	jsonOut := false
	for _, arg := range args {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "--json":
			jsonOut = true
		case "help", "-h", "--help":
			return printDoctorUsage()
		default:
			return fmt.Errorf("doctor: unknown flag %q", arg)
		}
	}

	report := buildDoctorReport()
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("doctor: marshal JSON: %w", err)
		}
	} else {
		printDoctorReport(report)
	}
	if report.Status == doctorStatusFail {
		return fmt.Errorf("doctor: issues detected")
	}
	return nil
}

func printDoctorUsage() error {
	fmt.Fprintln(os.Stdout, "Usage: elnath doctor [--json]")
	return nil
}

func buildDoctorReport() doctorReport {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return doctorReportFromChecks([]doctorCheck{{
			Name:    "config",
			Status:  doctorStatusFail,
			Summary: "Config could not be loaded or validated.",
			Detail:  err.Error(),
		}})
	}
	applyGlobalFlagOverrides(cfg, os.Args)

	checks := []doctorCheck{
		{
			Name:    "config",
			Status:  doctorStatusPass,
			Summary: "Config loaded and validated.",
			Detail:  cfgPath,
		},
		pathDoctorCheck("data_dir", cfg.DataDir),
		pathDoctorCheck("wiki_dir", cfg.WikiDir),
		providerDoctorCheck(cfg),
		timeoutDoctorCheck(cfg),
		daemonSocketDoctorCheck(cfg.Daemon.SocketPath),
		databaseFilesDoctorCheck(cfg.DataDir),
	}
	return doctorReportFromChecks(checks)
}

func doctorReportFromChecks(checks []doctorCheck) doctorReport {
	status := doctorStatusPass
	for _, check := range checks {
		switch check.Status {
		case doctorStatusFail:
			status = doctorStatusFail
		case doctorStatusWarn:
			if status != doctorStatusFail {
				status = doctorStatusWarn
			}
		}
	}
	return doctorReport{
		Status: status,
		OK:     status != doctorStatusFail,
		Checks: checks,
	}
}

func printDoctorReport(report doctorReport) {
	fmt.Fprintf(os.Stdout, "Elnath doctor: %s\n", strings.ToUpper(string(report.Status)))
	for _, check := range report.Checks {
		fmt.Fprintf(os.Stdout, "  [%s] %s - %s\n", check.Status, check.Name, check.Summary)
		if check.Detail != "" {
			fmt.Fprintf(os.Stdout, "        %s\n", check.Detail)
		}
	}
}

func pathDoctorCheck(name string, path string) doctorCheck {
	if strings.TrimSpace(path) == "" {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusFail,
			Summary: "Path is not configured.",
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return doctorCheck{
				Name:    name,
				Status:  doctorStatusWarn,
				Summary: "Directory does not exist yet.",
				Detail:  path,
			}
		}
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusFail,
			Summary: "Directory could not be inspected.",
			Detail:  err.Error(),
		}
	}
	if !info.IsDir() {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusFail,
			Summary: "Configured path is not a directory.",
			Detail:  path,
		}
	}
	return doctorCheck{
		Name:    name,
		Status:  doctorStatusPass,
		Summary: "Directory exists.",
		Detail:  path,
	}
}

func providerDoctorCheck(cfg *config.Config) doctorCheck {
	provider, model, err := buildProvider(cfg)
	if err != nil {
		return doctorCheck{
			Name:    "provider",
			Status:  doctorStatusFail,
			Summary: "Provider is not ready for agent runs.",
			Detail:  err.Error(),
		}
	}
	caps := llm.CapabilitiesOf(provider)
	return doctorCheck{
		Name:    "provider",
		Status:  doctorStatusPass,
		Summary: fmt.Sprintf("%s model=%s", caps.Name, model),
		Detail:  fmt.Sprintf("reasoning_effort=%s timeout=%ds", caps.ReasoningEffort, caps.RequestTimeoutSeconds),
	}
}

func timeoutDoctorCheck(cfg *config.Config) doctorCheck {
	var failures []string
	if cfg.Anthropic.Timeout <= 0 {
		failures = append(failures, "anthropic.timeout_seconds")
	}
	if cfg.OpenAI.Timeout <= 0 {
		failures = append(failures, "openai.timeout_seconds")
	}
	if cfg.OpenAIResponses.Timeout <= 0 {
		failures = append(failures, "openai_responses.timeout_seconds")
	}
	if cfg.Daemon.InactivityTimeout <= 0 {
		failures = append(failures, "daemon.inactivity_timeout_seconds")
	}
	if cfg.Daemon.WallClockTimeout <= 0 {
		failures = append(failures, "daemon.wall_clock_timeout_seconds")
	}
	if cfg.SelfHealing.TimeoutSeconds <= 0 {
		failures = append(failures, "self_healing.timeout_seconds")
	}
	if len(failures) > 0 {
		return doctorCheck{
			Name:    "timeouts",
			Status:  doctorStatusFail,
			Summary: "One or more timeout values are non-positive.",
			Detail:  strings.Join(failures, ", "),
		}
	}
	return doctorCheck{
		Name:    "timeouts",
		Status:  doctorStatusPass,
		Summary: "Configured timeouts are positive.",
		Detail: fmt.Sprintf("provider=%ds/%ds/%ds daemon=%ds/%ds self_heal=%ds",
			cfg.Anthropic.Timeout,
			cfg.OpenAI.Timeout,
			cfg.OpenAIResponses.Timeout,
			cfg.Daemon.InactivityTimeout,
			cfg.Daemon.WallClockTimeout,
			cfg.SelfHealing.TimeoutSeconds,
		),
	}
}

func daemonSocketDoctorCheck(path string) doctorCheck {
	if strings.TrimSpace(path) == "" {
		return doctorCheck{
			Name:    "daemon_socket",
			Status:  doctorStatusWarn,
			Summary: "Daemon socket path is not configured.",
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return doctorCheck{
				Name:    "daemon_socket",
				Status:  doctorStatusWarn,
				Summary: "Daemon socket is not present; daemon may not be running.",
				Detail:  path,
			}
		}
		return doctorCheck{
			Name:    "daemon_socket",
			Status:  doctorStatusFail,
			Summary: "Daemon socket could not be inspected.",
			Detail:  err.Error(),
		}
	}
	if info.Mode()&os.ModeSocket == 0 {
		return doctorCheck{
			Name:    "daemon_socket",
			Status:  doctorStatusWarn,
			Summary: "Daemon socket path exists but is not a socket.",
			Detail:  path,
		}
	}
	return doctorCheck{
		Name:    "daemon_socket",
		Status:  doctorStatusPass,
		Summary: "Daemon socket exists.",
		Detail:  path,
	}
}

func databaseFilesDoctorCheck(dataDir string) doctorCheck {
	if strings.TrimSpace(dataDir) == "" {
		return doctorCheck{
			Name:    "database_files",
			Status:  doctorStatusFail,
			Summary: "Data directory is not configured.",
		}
	}
	var missing []string
	for _, name := range []string{"elnath.db", "wiki.db"} {
		path := filepath.Join(dataDir, name)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, name)
				continue
			}
			return doctorCheck{
				Name:    "database_files",
				Status:  doctorStatusFail,
				Summary: "Database files could not be inspected.",
				Detail:  err.Error(),
			}
		}
	}
	if len(missing) > 0 {
		return doctorCheck{
			Name:    "database_files",
			Status:  doctorStatusWarn,
			Summary: "Database files are not initialized yet.",
			Detail:  strings.Join(missing, ", "),
		}
	}
	return doctorCheck{
		Name:    "database_files",
		Status:  doctorStatusPass,
		Summary: "Database files exist.",
		Detail:  filepath.Join(dataDir, "elnath.db") + ", " + filepath.Join(dataDir, "wiki.db"),
	}
}
