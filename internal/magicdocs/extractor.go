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
)

type Extractor struct {
	provider llm.Provider
	model    string
	writer   *WikiWriter
	logger   *slog.Logger
}

func NewExtractor(provider llm.Provider, model string, writer *WikiWriter, logger *slog.Logger) *Extractor {
	return &Extractor{
		provider: provider,
		model:    model,
		writer:   writer,
		logger:   logger,
	}
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

	prompt := buildPrompt(req, filtered, x.model)
	resp, err := x.provider.Chat(callCtx, prompt)
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
