package providerproxy

import (
	"context"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/config"
)

func TestOpenAIResponsesAdapterFromConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.OpenAIResponses.APIKey = "sk-test"
	cfg.OpenAIResponses.BaseURL = "https://api.example/v1/"

	adapter, err := OpenAIResponsesAdapterFromConfig(cfg)
	if err != nil {
		t.Fatalf("OpenAIResponsesAdapterFromConfig: %v", err)
	}
	if adapter.Name() != "openai-responses" {
		t.Fatalf("name = %q", adapter.Name())
	}
	if !containsString(adapter.AllowedPaths(), "/responses") || !containsString(adapter.AllowedPaths(), "/models") {
		t.Fatalf("allowed paths = %#v", adapter.AllowedPaths())
	}
	cred, err := adapter.Credential(context.Background())
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if cred.Bearer != "sk-test" {
		t.Fatalf("bearer = %q", cred.Bearer)
	}
	if cred.BaseURL != "https://api.example/v1" {
		t.Fatalf("base url = %q", cred.BaseURL)
	}
	if got := adapter.Status(context.Background()); !got.Ready || !got.Authenticated {
		t.Fatalf("status = %+v", got)
	}
}

func TestOpenAIResponsesAdapterRequiresConfiguredCredential(t *testing.T) {
	adapter, err := OpenAIResponsesAdapterFromConfig(&config.Config{})
	if err != nil {
		t.Fatalf("OpenAIResponsesAdapterFromConfig: %v", err)
	}
	if got := adapter.Status(context.Background()); got.Ready || got.Authenticated {
		t.Fatalf("status = %+v, want not ready", got)
	}
	_, err = adapter.Credential(context.Background())
	if err == nil || !strings.Contains(err.Error(), "openai_responses.api_key") {
		t.Fatalf("Credential error = %v", err)
	}
}
