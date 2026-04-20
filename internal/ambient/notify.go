package ambient

import "strings"

// FormatNotification renders an ambient notification into a consistent,
// persona-aware markdown block so scheduled or auto-triggered tasks speak
// with Elnath's voice rather than a raw daemon log. The locale argument is
// lightly normalised internally; today only a "ko" prefix selects Korean
// chrome, with every other value falling through to English.
//
// Empty title is rendered as no title block (skip the bold line) rather than
// an empty pair of asterisks. Empty body is likewise skipped. The signature
// line is always present so downstream surfaces can identify the sender.
func FormatNotification(title, body, persona, locale string) string {
	normLocale := strings.ToLower(strings.TrimSpace(locale))
	trimmedTitle := strings.TrimSpace(title)
	trimmedBody := strings.TrimSpace(body)
	trimmedPersona := strings.TrimSpace(persona)

	var sb strings.Builder
	if trimmedTitle != "" {
		sb.WriteString("**")
		sb.WriteString(trimmedTitle)
		sb.WriteString("**")
		sb.WriteString("\n\n")
	}
	if trimmedBody != "" {
		sb.WriteString(trimmedBody)
		sb.WriteString("\n\n")
	}

	sb.WriteString(ambientSignature(normLocale, trimmedPersona))
	return sb.String()
}

func ambientSignature(normLocale, persona string) string {
	base := "— Elnath ambient"
	if strings.HasPrefix(normLocale, "ko") {
		base = "— 엘나트 ambient"
	}
	if persona == "" {
		return base
	}
	return base + " · " + persona
}
