package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/llm/promptcache"
)

// promptCacheEvent is the per-turn on-disk record shape. Written one per
// line to <data-dir>/prompt-cache/<session-id>.jsonl by the Anthropic
// provider after each call; consumed here by `elnath debug prompt-cache`.
// The on-disk producer lands in a follow-up commit (Phase 8.1.8); this
// CLI is shipped ahead so the contract is frozen before the writer.
type promptCacheEvent struct {
	Turn      int                      `json:"turn"`
	Timestamp time.Time                `json:"ts"`
	Model     string                   `json:"model"`
	Report    *promptcache.BreakReport `json:"report"`
}

// debugPromptCache implements `elnath debug prompt-cache`.
//
// Default: load <data-dir>/prompt-cache/<session-id>.jsonl, render a
// per-turn hit/miss table + attribution summary. --json prints the raw
// event list for scripting.
func debugPromptCache(args []string) error {
	var sessionID string
	jsonOut := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h", a == "--help", a == "help":
			fmt.Fprintln(os.Stdout, "Usage: elnath debug prompt-cache --session=<id> [--json]")
			return nil
		case a == "--json":
			jsonOut = true
		case strings.HasPrefix(a, "--session="):
			sessionID = strings.TrimPrefix(a, "--session=")
		case a == "--session":
			if i+1 >= len(args) {
				return errors.New("debug prompt-cache: --session requires a value")
			}
			sessionID = args[i+1]
			i++
		default:
			return fmt.Errorf("debug prompt-cache: unknown flag %q", a)
		}
	}
	if sessionID == "" {
		return errors.New("debug prompt-cache: --session=<id> is required")
	}

	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("debug prompt-cache: load config: %w", err)
	}

	path := filepath.Join(cfg.DataDir, "prompt-cache", sessionID+".jsonl")
	events, err := loadPromptCacheEvents(path)
	if err != nil {
		return err
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(events)
	}
	renderPromptCacheReport(os.Stdout, sessionID, path, events)
	return nil
}

// loadPromptCacheEvents reads the jsonl file at path. Missing file yields
// an empty slice + nil error so the CLI can explain the absence rather
// than crash. Malformed lines surface as an error so partner noise does
// not silently hide a real producer regression.
func loadPromptCacheEvents(path string) ([]promptCacheEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("debug prompt-cache: open %s: %w", path, err)
	}
	defer f.Close()

	var events []promptCacheEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var ev promptCacheEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return nil, fmt.Errorf("debug prompt-cache: %s line %d: %w", path, line, err)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("debug prompt-cache: scan %s: %w", path, err)
	}
	return events, nil
}

// renderPromptCacheReport writes a human-readable summary. Empty-event
// paths print a "no events recorded" hint mentioning the integration-
// writer follow-up so the reader understands this is not a data-loss bug.
func renderPromptCacheReport(w io.Writer, sessionID, path string, events []promptCacheEvent) {
	fmt.Fprintf(w, "Session %s prompt-cache summary\n", sessionID)
	fmt.Fprintf(w, "  Source: %s\n", path)
	if len(events) == 0 {
		fmt.Fprintf(w, "  No events recorded — provider-side writer lands in Phase 8.1.8.\n")
		return
	}
	fmt.Fprintf(w, "  Turns:  %d\n\n", len(events))
	fmt.Fprintf(w, " %-4s | %-22s | %-10s | %-8s | %-8s | %s\n",
		"Turn", "Time", "Model", "Creation", "Read", "Verdict / reasons")
	fmt.Fprintln(w, strings.Repeat("-", 88))

	hits, misses, below := 0, 0, 0
	for _, ev := range events {
		verdict := classifyVerdict(ev.Report)
		switch verdict {
		case "hit":
			hits++
		case "miss":
			misses++
		case "below":
			below++
		}
		reasons := ""
		if ev.Report != nil {
			reasons = formatReasons(ev.Report.Reasons)
		}
		fmt.Fprintf(w, " %-4d | %-22s | %-10s | %-8d | %-8d | %s %s\n",
			ev.Turn,
			ev.Timestamp.Local().Format("2006-01-02 15:04:05"),
			truncateModel(ev.Model),
			safeCreation(ev.Report),
			safeRead(ev.Report),
			verdict,
			reasons,
		)
	}
	fmt.Fprintf(w, "\n  Totals: %d hits / %d misses / %d below-threshold\n", hits, misses, below)
}

func classifyVerdict(r *promptcache.BreakReport) string {
	if r == nil {
		return "n/a"
	}
	if r.Happened {
		return "miss"
	}
	if r.BelowThreshold {
		return "below"
	}
	return "hit"
}

func safeCreation(r *promptcache.BreakReport) int {
	if r == nil {
		return 0
	}
	return r.CreationTokens
}

func safeRead(r *promptcache.BreakReport) int {
	if r == nil {
		return 0
	}
	return r.ReadTokens
}

func formatReasons(reasons []promptcache.BreakDetail) string {
	if len(reasons) == 0 {
		return ""
	}
	parts := make([]string, 0, len(reasons))
	for _, d := range reasons {
		if d.Detail == "" {
			parts = append(parts, string(d.Reason))
		} else {
			parts = append(parts, fmt.Sprintf("%s(%s)", d.Reason, d.Detail))
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func truncateModel(m string) string {
	if len(m) <= 10 {
		return m
	}
	return m[:9] + "…"
}
