package magicdocs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/prompt"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/wiki"
)

// ExtractorPromptBuilder is the minimum surface the magic-docs extractor
// needs to render a base prompt prefix. prompt.Builder satisfies this via
// its Build method. A local interface keeps magicdocs decoupled from the
// full prompt package construction details.
type ExtractorPromptBuilder interface {
	Build(ctx context.Context, state *prompt.RenderState) (string, error)
}

// ExtractorPromptDeps bundles the optional prompt-pipeline dependencies
// for wiki-extraction. When non-nil, the extractor prefixes the hardcoded
// extraction rules with a Builder-rendered system prompt carrying identity,
// persona, and wiki-RAG context. A nil deps (or a Build error at call
// time) falls back to the legacy hardcoded-only system prompt so the
// extractor always produces output.
type ExtractorPromptDeps struct {
	Builder      ExtractorPromptBuilder
	Self         *self.SelfState
	WikiIdx      *wiki.Index
	PersonaExtra string
	ProviderName string
	WorkDir      string
}

// ExtractorOption configures optional Extractor dependencies.
type ExtractorOption func(*Extractor)

// WithPromptPipeline wires the prompt-pipeline so wiki extraction benefits
// from base identity + persona + RAG context in addition to the hardcoded
// extraction rules (GAP-MAGICDOCS-01 fix).
func WithPromptPipeline(deps ExtractorPromptDeps) ExtractorOption {
	return func(e *Extractor) {
		d := deps
		e.pipeline = &d
	}
}

type Extractor struct {
	provider llm.Provider
	model    string
	writer   *WikiWriter
	logger   *slog.Logger
	pipeline *ExtractorPromptDeps
}

func NewExtractor(provider llm.Provider, model string, writer *WikiWriter, logger *slog.Logger, opts ...ExtractorOption) *Extractor {
	e := &Extractor{
		provider: provider,
		model:    model,
		writer:   writer,
		logger:   logger,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// assemblePromptPrefix renders the pipeline prefix that carries identity
// and RAG context for extraction. Returns empty string (legacy fallback)
// when no pipeline is wired or when the builder errors.
func (x *Extractor) assemblePromptPrefix(ctx context.Context) string {
	if x.pipeline == nil || x.pipeline.Builder == nil {
		return ""
	}
	state := &prompt.RenderState{
		UserInput:    "wiki knowledge extraction from agent activity events",
		Self:         x.pipeline.Self,
		WikiIdx:      x.pipeline.WikiIdx,
		PersonaExtra: x.pipeline.PersonaExtra,
		Model:        x.model,
		Provider:     x.pipeline.ProviderName,
		WorkDir:      x.pipeline.WorkDir,
		DaemonMode:   true,
	}
	built, err := x.pipeline.Builder.Build(ctx, state)
	if err != nil {
		x.logger.Warn("magic-docs: prompt pipeline build failed, using legacy fallback", "error", err)
		return ""
	}
	return built
}

func (x *Extractor) Run(ctx context.Context, ch <-chan ExtractionRequest) {
	for req := range ch {
		select {
		case <-ctx.Done():
			return
		default:
		}
		x.processRequest(ctx, req)
	}
}

func (x *Extractor) processRequest(ctx context.Context, req ExtractionRequest) {
	defer func() {
		if r := recover(); r != nil {
			x.logger.Error("magic-docs extractor panic", "recover", r, "trigger", req.Trigger)
		}
	}()

	filtered := Filter(req.Events)
	if len(filtered.Signal) == 0 {
		x.logger.Debug("magic-docs skip: no signal events",
			"trigger", req.Trigger,
			"total_events", len(req.Events),
		)
		return
	}

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	systemPrefix := x.assemblePromptPrefix(callCtx)
	chatReq := buildPrompt(req, filtered, x.model, systemPrefix)
	resp, err := x.provider.Chat(callCtx, chatReq)
	if err != nil {
		x.logger.Error("magic-docs LLM call failed",
			"trigger", req.Trigger,
			"error", err,
		)
		return
	}

	result, err := parseExtractionResult(resp.Content)
	if err != nil {
		x.logger.Error("magic-docs parse failed",
			"trigger", req.Trigger,
			"error", err,
		)
		return
	}

	if len(result.Pages) == 0 {
		x.logger.Debug("magic-docs: nothing worth keeping",
			"trigger", req.Trigger,
		)
		return
	}

	var valid []PageAction
	for _, a := range result.Pages {
		if err := validatePageAction(a); err != nil {
			x.logger.Warn("magic-docs invalid page action",
				"path", a.Path,
				"error", err,
			)
			continue
		}
		valid = append(valid, a)
	}

	if len(valid) == 0 {
		return
	}

	created, updated := x.writer.Apply(valid, req.SessionID, req.Trigger)
	x.logger.Info("magic-docs extraction complete",
		"trigger", req.Trigger,
		"signal_events", len(filtered.Signal),
		"pages_created", created,
		"pages_updated", updated,
	)
}

func parseExtractionResult(raw string) (*ExtractionResult, error) {
	cleaned := strings.TrimSpace(raw)
	if strings.HasPrefix(cleaned, "```") {
		lines := strings.Split(cleaned, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
		}
		if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			lines = lines[:len(lines)-1]
		}
		cleaned = strings.Join(lines, "\n")
	}

	cleaned = extractFirstJSONObject(cleaned)

	var result ExtractionResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("parse extraction json: %w", err)
	}
	return &result, nil
}

func extractFirstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start == -1 {
		return s
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		c := s[i]
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}

var validActions = map[string]bool{"create": true, "update": true}
var validTypes = map[string]bool{
	"entity": true, "concept": true, "source": true,
	"analysis": true, "map": true,
}
var validConfidence = map[string]bool{"high": true, "medium": true, "low": true}

func validatePageAction(a PageAction) error {
	if !validActions[a.Action] {
		return fmt.Errorf("invalid action %q", a.Action)
	}
	if a.Path == "" {
		return fmt.Errorf("empty path")
	}
	if strings.Contains(a.Path, "..") {
		return fmt.Errorf("path traversal detected: %q", a.Path)
	}
	if filepath.IsAbs(a.Path) {
		return fmt.Errorf("absolute path not allowed: %q", a.Path)
	}
	if strings.ContainsAny(a.Path, "\x00") {
		return fmt.Errorf("path contains null byte: %q", a.Path)
	}
	if !validTypes[a.Type] {
		return fmt.Errorf("invalid type %q", a.Type)
	}
	if !validConfidence[a.Confidence] {
		return fmt.Errorf("invalid confidence %q", a.Confidence)
	}
	return nil
}
