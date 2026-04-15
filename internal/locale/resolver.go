package locale

import (
	"fmt"
	"strings"
)

const DefaultConfidenceThreshold = 0.6

type DetectResult struct {
	Lang       string
	Confidence float64
}

func Resolve(current DetectResult, sessionLast string, cfgLocale string) string {
	cfgLocale = NormalizeConfig(cfgLocale)

	if current.Confidence >= DefaultConfidenceThreshold && current.Lang != "" {
		return current.Lang
	}
	if sessionLast != "" {
		return sessionLast
	}
	if cfgLocale != "" {
		return cfgLocale
	}
	return "en"
}

func NormalizeConfig(cfgLocale string) string {
	locale := strings.ToLower(strings.TrimSpace(cfgLocale))
	if locale == "" || locale == "auto" {
		return ""
	}
	return locale
}

// ResponseDirective returns the system prompt suffix that instructs the LLM
// to answer in the resolved locale's native language. Empty string for "",
// "en", or any language without an English display name — callers may append
// the result unconditionally without adding their own guards.
func ResponseDirective(lang string) string {
	if lang == "" || lang == "en" {
		return ""
	}
	name := LangToEnglishName(lang)
	if name == "" {
		return ""
	}
	return fmt.Sprintf("Respond in %s.", name)
}

func LangToEnglishName(lang string) string {
	switch lang {
	case "ko":
		return "Korean"
	case "ja":
		return "Japanese"
	case "zh":
		return "Chinese"
	default:
		return ""
	}
}
