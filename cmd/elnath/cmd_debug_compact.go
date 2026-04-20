package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/llm"
)

// debugCompactParams feeds runDebugCompact so tests can inject a mock provider,
// dataDir, and writer without touching config / real LLM endpoints.
type debugCompactParams struct {
	SessionID     string
	DataDir       string
	Provider      llm.Provider
	ContextWindow *conversation.ContextWindow
	Budget        int
	JSONOut       bool
	Out           io.Writer
}

// debugCompactResult is the structured payload printed as JSON (and mirrored in
// the text rendering).
type debugCompactResult struct {
	SessionID      string `json:"session_id"`
	Budget         int    `json:"budget"`
	PreCount       int    `json:"pre_count"`
	PostCount      int    `json:"post_count"`
	PreTokens      int    `json:"pre_tokens"`
	PostTokens     int    `json:"post_tokens"`
	Applied        bool   `json:"applied"`
	SummaryPreview string `json:"summary_preview"`
}

func debugCompact(ctx context.Context, args []string) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("debug compact: load config: %w", err)
	}

	params := debugCompactParams{DataDir: cfg.DataDir, Out: os.Stdout}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-h", "--help", "help":
			printDebugCompactUsage(os.Stdout)
			return nil
		case "--json":
			params.JSONOut = true
		case "--budget":
			if i+1 >= len(args) {
				return fmt.Errorf("debug compact: --budget requires a value")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("debug compact: invalid --budget value %q", args[i+1])
			}
			params.Budget = n
			i++
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("debug compact: unknown flag %q", arg)
			}
			if params.SessionID != "" {
				return fmt.Errorf("debug compact: multiple positional args (already have %q, got %q)", params.SessionID, arg)
			}
			params.SessionID = arg
		}
	}

	provider, model, err := buildProvider(cfg)
	if err != nil {
		return fmt.Errorf("debug compact: build provider: %w", err)
	}
	params.Provider = provider
	if cfg.CompressThreshold > 0 {
		params.ContextWindow = conversation.NewContextWindowWithThreshold(cfg.CompressThreshold)
	} else {
		params.ContextWindow = conversation.NewContextWindow()
	}
	if params.Budget == 0 {
		params.Budget = conversation.ResolveCompressionBudget(resolveProviderContextWindow(provider, model), cfg.MaxContextTokens)
	}

	return runDebugCompact(ctx, params)
}

func printDebugCompactUsage(w io.Writer) {
	fmt.Fprint(w, `Usage: elnath debug compact <session-id> [--budget <tokens>] [--json]

Force the context-window compaction pipeline for a single session and
report the pre/post message count and token estimate. This is a
non-destructive simulation: the session JSONL on disk is not modified
(live compaction is runtime-only, so the debug command mirrors that).

Flags:
  --budget <tokens>    Override the compaction budget (default: provider window
                       or cfg.MaxContextTokens).
  --json               Emit a structured JSON payload instead of text.

Positional:
  <session-id>         Full UUID or any unique prefix. The status-line
                       ellipsis ("...") is accepted and stripped.
`)
}

func runDebugCompact(ctx context.Context, p debugCompactParams) error {
	if p.Out == nil {
		p.Out = os.Stdout
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return fmt.Errorf("debug compact: session id required (see --help)")
	}
	if p.DataDir == "" {
		return fmt.Errorf("debug compact: data dir not configured")
	}
	if p.ContextWindow == nil {
		p.ContextWindow = conversation.NewContextWindow()
	}

	resolved, err := agent.ResolveSessionID(p.DataDir, p.SessionID)
	if err != nil {
		return fmt.Errorf("debug compact: %w", err)
	}

	sess, err := agent.LoadSession(p.DataDir, resolved)
	if err != nil {
		return fmt.Errorf("debug compact: load session: %w", err)
	}

	budget := p.Budget
	if budget <= 0 {
		budget = 100_000
	}

	preMessages := sess.Messages
	preCount := len(preMessages)
	preTokens := p.ContextWindow.EstimateTokens(preMessages)

	compressed, err := p.ContextWindow.CompressMessages(ctx, p.Provider, preMessages, budget)
	if err != nil {
		return fmt.Errorf("debug compact: compress: %w", err)
	}

	result := debugCompactResult{
		SessionID:      resolved,
		Budget:         budget,
		PreCount:       preCount,
		PostCount:      len(compressed),
		PreTokens:      preTokens,
		PostTokens:     p.ContextWindow.EstimateTokens(compressed),
		SummaryPreview: previewFirstMessage(compressed),
	}

	if p.JSONOut {
		enc := json.NewEncoder(p.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	return renderDebugCompactText(p.Out, result)
}

func renderDebugCompactText(w io.Writer, r debugCompactResult) error {
	fmt.Fprintf(w, "Compaction simulation for %s\n\n", r.SessionID)
	fmt.Fprintf(w, "  budget (tokens):  %d\n", r.Budget)
	fmt.Fprintf(w, "  pre  messages:    %d\n", r.PreCount)
	fmt.Fprintf(w, "  post messages:    %d\n", r.PostCount)
	fmt.Fprintf(w, "  pre  tokens (est):%d\n", r.PreTokens)
	fmt.Fprintf(w, "  post tokens (est):%d\n", r.PostTokens)
	if r.SummaryPreview != "" {
		preview := r.SummaryPreview
		if len(preview) > 300 {
			preview = preview[:297] + "..."
		}
		fmt.Fprintf(w, "\n  first message preview:\n    %s\n", preview)
	}
	fmt.Fprintln(w, "\n  note: session JSONL on disk was NOT modified (dry-run only)")
	return nil
}

func previewFirstMessage(msgs []llm.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	return strings.TrimSpace(msgs[0].Text())
}
