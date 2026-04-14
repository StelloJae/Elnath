package llm

import (
	"strings"
	"testing"
)

func TestFormatUsageSummary(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		stats    UsageStats
		wantIn   []string // substrings that must appear
		wantNot  []string // substrings that must NOT appear
	}{
		{
			name:  "basic sonnet usage",
			model: "claude-sonnet-4-6",
			stats: UsageStats{InputTokens: 1000, OutputTokens: 500},
			wantIn: []string{
				"1,000 in",
				"500 out",
				"$",
			},
		},
		{
			name:  "zero usage returns empty",
			model: "claude-sonnet-4-6",
			stats: UsageStats{},
			wantIn: []string{""},
		},
		{
			name:  "with cache tokens",
			model: "claude-sonnet-4-6",
			stats: UsageStats{
				InputTokens:  2000,
				OutputTokens: 1000,
				CacheRead:    500,
				CacheWrite:   100,
			},
			wantIn: []string{
				"2,000 in",
				"1,000 out",
				"cache",
				"$",
			},
		},
		{
			name:  "unknown model uses fallback pricing",
			model: "unknown-model-xyz",
			stats: UsageStats{InputTokens: 1000, OutputTokens: 500},
			wantIn: []string{
				"1,000 in",
				"500 out",
				"$",
			},
		},
		{
			name:  "large token counts formatted with commas",
			model: "claude-opus-4-6",
			stats: UsageStats{InputTokens: 1234567, OutputTokens: 89012},
			wantIn: []string{
				"1,234,567 in",
				"89,012 out",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatUsageSummary(tt.model, tt.stats)

			if tt.name == "zero usage returns empty" {
				if got != "" {
					t.Errorf("expected empty string for zero usage, got %q", got)
				}
				return
			}

			for _, s := range tt.wantIn {
				if !strings.Contains(got, s) {
					t.Errorf("FormatUsageSummary() = %q, want substring %q", got, s)
				}
			}
			for _, s := range tt.wantNot {
				if strings.Contains(got, s) {
					t.Errorf("FormatUsageSummary() = %q, should NOT contain %q", got, s)
				}
			}
		})
	}
}

func TestFormatUsageSummary_CostAccuracy(t *testing.T) {
	// Sonnet: $3/M in, $15/M out
	stats := UsageStats{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	got := FormatUsageSummary("claude-sonnet-4-6", stats)

	// Expected: $3 input + $15 output = $18.00
	if !strings.Contains(got, "$18.00") {
		t.Errorf("expected cost $18.00 for 1M in/out sonnet, got %q", got)
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{1234567, "1,234,567"},
		{100, "100"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatNumber(tt.n); got != tt.want {
				t.Errorf("formatNumber(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}
