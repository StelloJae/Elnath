package main

import (
	"strings"
	"testing"
)

func TestNoProviderConfiguredMessageMentionsResponsesProvider(t *testing.T) {
	msg := noProviderConfiguredMessage()
	for _, want := range []string{
		"ELNATH_OPENAI_RESPONSES_API_KEY",
		"openai_responses.api_key",
		"ELNATH_OPENAI_API_KEY",
		"ELNATH_ANTHROPIC_API_KEY",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q missing %q", msg, want)
		}
	}
}
