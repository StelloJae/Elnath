package tools

import "testing"

// TestIsPreapprovedHost verifies Claude Code preapproved.ts parity
// (/Users/stello/claude-code-src/src/tools/WebFetchTool/preapproved.ts):
// hostname-only match, path-prefix match with segment-boundary enforcement,
// and the two Elnath-specific Korean-language additions.
func TestIsPreapprovedHost(t *testing.T) {
	cases := []struct {
		name     string
		hostname string
		pathname string
		want     bool
	}{
		{"hostname-only go.dev doc path", "go.dev", "/doc/effective_go", true},
		{"hostname-only react.dev root", "react.dev", "/", true},
		{"hostname-only docs.python.org", "docs.python.org", "/3/library/", true},
		{"unknown host rejected", "evil.example.com", "/", false},

		{"path-prefix github.com/anthropics exact", "github.com", "/anthropics", true},
		{"path-prefix github.com/anthropics deeper segment", "github.com", "/anthropics/claude-code", true},
		{"path-prefix segment boundary rejects adjacent name", "github.com", "/anthropics-evil/x", false},
		{"path-prefix github.com unrelated path rejected", "github.com", "/other/repo", false},

		{"path-prefix vercel.com/docs match", "vercel.com", "/docs/functions", true},
		{"path-prefix vercel.com root rejected (no /docs)", "vercel.com", "/dashboard", false},

		{"Elnath dogfood finance.naver.com", "finance.naver.com", "/item/main.naver", true},
		{"Elnath dogfood namu.wiki", "namu.wiki", "/w/kakao", true},

		{"duplicated learn.microsoft.com still matches", "learn.microsoft.com", "/azure/", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPreapprovedHost(tc.hostname, tc.pathname); got != tc.want {
				t.Errorf("isPreapprovedHost(%q, %q) = %v, want %v", tc.hostname, tc.pathname, got, tc.want)
			}
		})
	}
}

// TestIsPreapprovedURL verifies URL parsing + delegation to
// isPreapprovedHost, and the fail-closed behaviour for malformed URLs.
func TestIsPreapprovedURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"preapproved hostname", "https://go.dev/doc/", true},
		{"preapproved path-prefix", "https://github.com/anthropics/claude-code", true},
		{"non-preapproved host", "https://evil.example.com/", false},
		{"Korean naver finance with query", "https://finance.naver.com/item/main.naver?code=005930", true},
		{"malformed URL returns false", "://bad", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPreapprovedURL(tc.url); got != tc.want {
				t.Errorf("isPreapprovedURL(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}
