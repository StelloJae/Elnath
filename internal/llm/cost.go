package llm

import "fmt"

// FormatUsageSummary returns a human-readable one-line summary of token usage,
// estimated cost, and (optionally) tool-call activity for the same turn.
// Returns "" if there was no usage. toolCalls/toolErrors default to 0;
// the tools segment is only emitted when toolCalls > 0 so existing zero-tool
// turns keep the legacy 3-segment format.
//
// The tool count surfaces here (not just in Debug logs) because callers and
// observability harnesses need a structured signal for "how many tools did
// this turn use" without having to parse Debug-level traces. Phase 8.1
// baseline replay relies on this for cross-provider tool-use comparison.
func FormatUsageSummary(model string, stats UsageStats, toolCalls, toolErrors int) string {
	if stats.InputTokens == 0 && stats.OutputTokens == 0 {
		return ""
	}

	cost := estimateCost("", model, stats)

	base := fmt.Sprintf("[tokens: %s in / %s out", formatNumber(stats.InputTokens), formatNumber(stats.OutputTokens))

	if stats.CacheRead > 0 || stats.CacheWrite > 0 {
		base += fmt.Sprintf(" (cache: %s read, %s write)", formatNumber(stats.CacheRead), formatNumber(stats.CacheWrite))
	}

	base += fmt.Sprintf(" | cost: $%.2f", cost)

	if toolCalls > 0 {
		if toolErrors > 0 {
			base += fmt.Sprintf(" | tools: %d (%d err)", toolCalls, toolErrors)
		} else {
			base += fmt.Sprintf(" | tools: %d", toolCalls)
		}
	}

	base += "]"
	return base
}

// formatNumber formats an integer with comma separators (e.g. 1234567 → "1,234,567").
func formatNumber(n int) string {
	if n < 0 {
		return "-" + formatNumber(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}

	var result []byte
	remainder := len(s) % 3
	if remainder > 0 {
		result = append(result, s[:remainder]...)
		if len(s) > remainder {
			result = append(result, ',')
		}
	}
	for i := remainder; i < len(s); i += 3 {
		result = append(result, s[i:i+3]...)
		if i+3 < len(s) {
			result = append(result, ',')
		}
	}
	return string(result)
}
