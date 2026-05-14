package tools

import (
	"fmt"
	"strings"
)

const (
	CommandIntentInspect       = "inspect"
	CommandIntentEdit          = "edit"
	CommandIntentFocusedVerify = "focused_verify"
	CommandIntentBroadVerify   = "broad_verify"
	CommandIntentDiagnostic    = "diagnostic"
	CommandIntentBackground    = "background"

	commandIntentSourceExplicit = "explicit"
	commandIntentSourceDefault  = "default"
)

func normalizeCommandIntent(raw string, fallback string) (intent string, source string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if !isValidCommandIntent(fallback) {
			return "", "", fmt.Errorf("invalid default command intent %q", fallback)
		}
		return fallback, commandIntentSourceDefault, nil
	}
	intent = strings.ToLower(raw)
	if !isValidCommandIntent(intent) {
		return "", "", fmt.Errorf("invalid command intent %q (allowed: inspect, edit, focused_verify, broad_verify, diagnostic, background)", raw)
	}
	return intent, commandIntentSourceExplicit, nil
}

func isValidCommandIntent(intent string) bool {
	switch intent {
	case CommandIntentInspect,
		CommandIntentEdit,
		CommandIntentFocusedVerify,
		CommandIntentBroadVerify,
		CommandIntentDiagnostic,
		CommandIntentBackground:
		return true
	default:
		return false
	}
}
