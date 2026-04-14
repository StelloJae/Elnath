package prompt

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

type threatPattern struct {
	id string
	re *regexp.Regexp
}

var compiledThreatPatterns = []threatPattern{
	{id: "prompt_injection", re: regexp.MustCompile(`(?i)ignore\s+(?:\w+\s+)*(?:previous|all|above|prior)\s+(?:\w+\s+)*instructions`)},
	{id: "deception_hide", re: regexp.MustCompile(`(?i)do\s+not\s+tell\s+the\s+user`)},
	{id: "sys_prompt_override", re: regexp.MustCompile(`(?i)system\s+prompt\s+override`)},
	{id: "disregard_rules", re: regexp.MustCompile(`(?i)disregard\s+(your|all|any)\s+(instructions|rules|guidelines)`)},
	{id: "bypass_restrictions", re: regexp.MustCompile(`(?i)act\s+as\s+(if|though)\s+you\s+(have\s+no|don't\s+have)\s+(restrictions|limits|rules)`)},
	{id: "html_comment_injection", re: regexp.MustCompile(`(?i)<!--[^>]*(?:ignore|override|system|secret|hidden)[^>]*-->`)},
	{id: "hidden_div", re: regexp.MustCompile(`(?i)<\s*div\s+style\s*=\s*["'].*display\s*:\s*none`)},
	{id: "exfil_curl", re: regexp.MustCompile(`(?i)curl\s+[^\n]*\$\{?\w*(KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|API)`)},
	{id: "read_secrets", re: regexp.MustCompile(`(?i)cat\s+[^\n]*(\.env|credentials|\.netrc|\.pgpass)`)},
}

func ScanContent(content, filename string) (cleaned string, blocked bool) {
	matchedIDs := detectThreatPatterns(content)
	if len(matchedIDs) == 0 {
		return content, false
	}

	if strings.TrimSpace(filename) == "" {
		filename = "content"
	}
	slog.Warn("threat_scan: blocked content", "filename", filename, "patterns", matchedIDs)
	return fmt.Sprintf("[BLOCKED: %s contained potential prompt injection (%s). Content not loaded.]", filename, strings.Join(matchedIDs, ", ")), true
}

func detectThreatPatterns(content string) []string {
	matchedIDs := make([]string, 0, len(compiledThreatPatterns)+1)
	if hasInvisibleUnicode(content) {
		matchedIDs = append(matchedIDs, "invisible_unicode")
	}
	for _, pattern := range compiledThreatPatterns {
		if pattern.re.MatchString(content) {
			matchedIDs = append(matchedIDs, pattern.id)
		}
	}
	return matchedIDs
}

func hasInvisibleUnicode(content string) bool {
	for _, r := range content {
		switch r {
		case '\u200b', '\u200c', '\u200d', '\u2060', '\ufeff':
			return true
		}
		if r >= '\u202a' && r <= '\u202e' {
			return true
		}
	}
	return false
}
