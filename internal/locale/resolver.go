package locale

import "strings"

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
