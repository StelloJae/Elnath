package llm

import "fmt"

// FormatUsageSummary returns a human-readable one-line summary of token usage
// and estimated cost. Returns "" if there was no usage.
func FormatUsageSummary(model string, stats UsageStats) string {
	if stats.InputTokens == 0 && stats.OutputTokens == 0 {
		return ""
	}

	cost := estimateCost("", model, stats)

	base := fmt.Sprintf("[tokens: %s in / %s out", formatNumber(stats.InputTokens), formatNumber(stats.OutputTokens))

	if stats.CacheRead > 0 || stats.CacheWrite > 0 {
		base += fmt.Sprintf(" (cache: %s read, %s write)", formatNumber(stats.CacheRead), formatNumber(stats.CacheWrite))
	}

	base += fmt.Sprintf(" | cost: $%.2f]", cost)
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
