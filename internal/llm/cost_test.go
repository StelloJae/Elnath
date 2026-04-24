package llm

import (
	"strings"
	"testing"
)

func TestFormatUsageSummary(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		stats   UsageStats
		wantIn  []string // substrings that must appear
		wantNot []string // substrings that must NOT appear
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
			name:   "zero usage returns empty",
			model:  "claude-sonnet-4-6",
			stats:  UsageStats{},
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
			got := FormatUsageSummary(tt.model, tt.stats, 0, 0)

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
	got := FormatUsageSummary("claude-sonnet-4-6", stats, 0, 0)

	// Expected: $3 input + $15 output = $18.00
	if !strings.Contains(got, "$18.00") {
		t.Errorf("expected cost $18.00 for 1M in/out sonnet, got %q", got)
	}
}

// TestFormatUsageSummary_ToolCounts verifies the new tools segment is
// emitted only when toolCalls > 0, with an "(N err)" suffix when any
// errored. Phase 8.1 baseline depends on this exposure for cross-
// provider tool-use comparison (Codex + Anthropic + others all surface
// uniformly here).
func TestFormatUsageSummary_ToolCounts(t *testing.T) {
	stats := UsageStats{InputTokens: 1000, OutputTokens: 500}

	t.Run("zero tools omits segment", func(t *testing.T) {
		got := FormatUsageSummary("claude-sonnet-4-6", stats, 0, 0)
		if strings.Contains(got, "tools:") {
			t.Errorf("expected no tools segment for zero calls, got %q", got)
		}
	})

	t.Run("non-zero clean count", func(t *testing.T) {
		got := FormatUsageSummary("claude-sonnet-4-6", stats, 4, 0)
		if !strings.Contains(got, "tools: 4]") {
			t.Errorf("expected '| tools: 4]' suffix, got %q", got)
		}
		if strings.Contains(got, "err") {
			t.Errorf("clean count should not mention err, got %q", got)
		}
	})

	t.Run("with errors", func(t *testing.T) {
		got := FormatUsageSummary("claude-sonnet-4-6", stats, 7, 2)
		if !strings.Contains(got, "tools: 7 (2 err)") {
			t.Errorf("expected 'tools: 7 (2 err)' substring, got %q", got)
		}
	})
}

func TestEstimateCostGPT5Models(t *testing.T) {
	// 1M input + 1M output, no cache.
	stats := UsageStats{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	tests := []struct {
		model   string
		wantUSD float64
	}{
		{model: "gpt-5", wantUSD: 1.25 + 10.00},
		{model: "gpt-5-mini", wantUSD: 0.25 + 2.00},
		{model: "gpt-5-nano", wantUSD: 0.05 + 0.40},
		{model: "gpt-5.1", wantUSD: 1.25 + 10.00},
		{model: "gpt-5.2", wantUSD: 1.75 + 14.00},
		{model: "gpt-5.2-pro", wantUSD: 21.00 + 168.00},
		{model: "gpt-5.5", wantUSD: 5.00 + 30.00},
		{model: "gpt-5.4-mini", wantUSD: 0.75 + 4.50},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := estimateCost("", tt.model, stats)
			if got != tt.wantUSD {
				t.Errorf("estimateCost(%q, 1M/1M) = %.4f, want %.4f", tt.model, got, tt.wantUSD)
			}
		})
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
