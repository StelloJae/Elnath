package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCommandCatalogIncludesProxy(t *testing.T) {
	catalog := commandCatalog(false)
	seen := make(map[string]commandCatalogEntry)
	for _, entry := range catalog {
		seen[entry.Name] = entry
	}
	entry, ok := seen["proxy"]
	if !ok {
		t.Fatal("command catalog missing proxy")
	}
	if entry.Category != "provider" {
		t.Fatalf("proxy category = %q", entry.Category)
	}
	if entry.ArgumentHint == "" {
		t.Fatal("proxy argument hint is empty")
	}
}

func TestCmdProxyProvidersJSON(t *testing.T) {
	cfgPath := writeProxyTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath, "proxy", "providers", "--json"})

	stdout, stderr := captureOutput(t, func() {
		if err := cmdProxy(context.Background(), []string{"providers", "--json"}); err != nil {
			t.Fatalf("cmdProxy providers: %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
	var payload []proxyProviderView
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if len(payload) != 1 {
		t.Fatalf("providers = %#v", payload)
	}
	if payload[0].Provider != "openai-responses" || !payload[0].Ready {
		t.Fatalf("provider = %+v", payload[0])
	}
	if payload[0].BaseURL == "" || payload[0].APIKeyConfigured != true {
		t.Fatalf("provider config = %+v", payload[0])
	}
}

func TestCmdProxyStatusJSONDefaultsLocalhost(t *testing.T) {
	cfgPath := writeProxyTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath, "proxy", "status", "--json"})

	stdout, stderr := captureOutput(t, func() {
		if err := cmdProxy(context.Background(), []string{"status", "--json"}); err != nil {
			t.Fatalf("cmdProxy status: %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
	var payload proxyStatusView
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if payload.ListenURL != "http://127.0.0.1:8645/v1" {
		t.Fatalf("listen URL = %q", payload.ListenURL)
	}
	if payload.State != "stopped" || payload.Provider != "openai-responses" || !payload.Ready {
		t.Fatalf("status = %+v", payload)
	}
}

func writeProxyTestConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	data := "data_dir: " + filepath.Join(dir, "data") + "\n" +
		"wiki_dir: " + filepath.Join(dir, "wiki") + "\n" +
		"provider: openai-responses\n" +
		"openai_responses:\n" +
		"  api_key: sk-test\n" +
		"  base_url: https://api.example/v1\n" +
		"  model: gpt-5.5\n" +
		"  timeout_seconds: 42\n"
	if err := os.WriteFile(cfgPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}
