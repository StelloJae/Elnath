package secret

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/audit"
	"github.com/stello/elnath/internal/prompt"
	"github.com/stello/elnath/internal/tools"
)

type SecretScanHook struct {
	detector *Detector
	trail    *audit.Trail
}

func NewSecretScanHook(detector *Detector, trail *audit.Trail) *SecretScanHook {
	return &SecretScanHook{detector: detector, trail: trail}
}

func (h *SecretScanHook) PreToolUse(_ context.Context, _ string, _ json.RawMessage) (agent.HookResult, error) {
	return agent.HookResult{Action: agent.HookAllow}, nil
}

func (h *SecretScanHook) PostToolUse(_ context.Context, toolName string, _ json.RawMessage, result *tools.Result) error {
	if result == nil || result.Output == "" || h == nil || h.detector == nil {
		return nil
	}

	redacted, findings := h.detector.ScanAndRedact(result.Output)
	if len(findings) > 0 {
		result.Output = redacted
	}
	for _, finding := range findings {
		chars := finding.End - finding.Start
		slog.Warn("secret redacted in tool output", "tool", toolName, "rule", finding.RuleID, "chars", chars)
		if h.trail == nil {
			continue
		}
		if err := h.trail.Log(audit.Event{
			Type:     audit.EventSecretRedacted,
			ToolName: toolName,
			RuleID:   finding.RuleID,
			Detail:   fmt.Sprintf("redacted %d chars", chars),
		}); err != nil {
			slog.Warn("audit trail write failed", "tool", toolName, "rule", finding.RuleID, "error", err)
		}
	}

	cleaned, blocked := prompt.ScanContent(result.Output, "tool:"+toolName)
	if blocked {
		result.Output = cleaned
		if h.trail != nil {
			if err := h.trail.Log(audit.Event{
				Type:     audit.EventInjectionBlocked,
				ToolName: toolName,
				Detail:   "injection blocked in tool output",
			}); err != nil {
				slog.Warn("audit trail write failed", "tool", toolName, "error", err)
			}
		}
	}

	return nil
}
