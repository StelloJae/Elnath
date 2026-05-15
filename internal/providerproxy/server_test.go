package providerproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeAdapter struct {
	name            string
	displayName     string
	allowed         []string
	baseURL         string
	bearer          string
	credentialErr   error
	credentialCalls atomic.Int64
}

func (a *fakeAdapter) Name() string {
	if a.name == "" {
		return "fake"
	}
	return a.name
}

func (a *fakeAdapter) DisplayName() string {
	if a.displayName == "" {
		return "Fake Provider"
	}
	return a.displayName
}

func (a *fakeAdapter) AllowedPaths() []string {
	return append([]string(nil), a.allowed...)
}

func (a *fakeAdapter) Credential(context.Context) (Credential, error) {
	a.credentialCalls.Add(1)
	if a.credentialErr != nil {
		return Credential{}, a.credentialErr
	}
	return Credential{
		Bearer:      a.bearer,
		TokenType:   "Bearer",
		BaseURL:     a.baseURL,
		ExpiresAt:   time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
		Refreshable: false,
	}, nil
}

func TestServerForwardsAllowedResponsesPath(t *testing.T) {
	var upstreamAuth string
	var upstreamBody string
	var upstreamPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuth = r.Header.Get("Authorization")
		upstreamPath = r.URL.String()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		upstreamBody = string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: one\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	adapter := &fakeAdapter{
		allowed: []string{"/responses"},
		baseURL: upstream.URL + "/v1",
		bearer:  "upstream-token",
	}
	proxy := httptest.NewServer(NewHandler(adapter, ServerOptions{}))
	defer proxy.Close()

	resp, err := http.Post(
		proxy.URL+"/v1/responses?stream=true",
		"application/json",
		strings.NewReader(`{"model":"gpt-5.5","input":"hi"}`),
	)
	if err != nil {
		t.Fatalf("post proxy: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read proxy body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type = %q, want event-stream", resp.Header.Get("Content-Type"))
	}
	if upstreamAuth != "Bearer upstream-token" {
		t.Fatalf("upstream auth = %q", upstreamAuth)
	}
	if upstreamPath != "/v1/responses?stream=true" {
		t.Fatalf("upstream path = %q", upstreamPath)
	}
	if upstreamBody != `{"model":"gpt-5.5","input":"hi"}` {
		t.Fatalf("upstream body = %q", upstreamBody)
	}
	if !strings.Contains(string(body), "data: [DONE]") {
		t.Fatalf("body = %q, want SSE payload", body)
	}
}

func TestServerRejectsDisallowedPathWithoutResolvingCredential(t *testing.T) {
	adapter := &fakeAdapter{
		allowed: []string{"/responses"},
		baseURL: "https://upstream.example/v1",
		bearer:  "upstream-token",
	}
	proxy := httptest.NewServer(NewHandler(adapter, ServerOptions{}))
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/v1/chat/completions")
	if err != nil {
		t.Fatalf("get proxy: %v", err)
	}
	defer resp.Body.Close()
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error JSON: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if payload.Error.Code != "path_not_allowed" || payload.Error.Type != "path_not_allowed" {
		t.Fatalf("error = %+v", payload.Error)
	}
	if !strings.Contains(payload.Error.Message, "/responses") {
		t.Fatalf("message = %q, want allowed path", payload.Error.Message)
	}
	if calls := adapter.credentialCalls.Load(); calls != 0 {
		t.Fatalf("credential calls = %d, want 0", calls)
	}
}

func TestServerReturnsStructuredAuthFailure(t *testing.T) {
	adapter := &fakeAdapter{
		allowed:       []string{"/responses"},
		credentialErr: errors.New("refresh session revoked"),
	}
	proxy := httptest.NewServer(NewHandler(adapter, ServerOptions{}))
	defer proxy.Close()

	resp, err := http.Post(proxy.URL+"/v1/responses", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post proxy: %v", err)
	}
	defer resp.Body.Close()
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error JSON: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if payload.Error.Code != "upstream_auth_failed" || payload.Error.Type != "upstream_auth_failed" {
		t.Fatalf("error = %+v", payload.Error)
	}
	if !strings.Contains(payload.Error.Message, "refresh session revoked") {
		t.Fatalf("message = %q", payload.Error.Message)
	}
}

func TestServerHealthReportsAdapterStatus(t *testing.T) {
	adapter := &fakeAdapter{
		name:        "openai-responses",
		displayName: "OpenAI Responses",
		allowed:     []string{"/responses", "/models"},
		baseURL:     "https://api.example/v1",
		bearer:      "secret",
	}
	proxy := httptest.NewServer(NewHandler(adapter, ServerOptions{}))
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	var payload HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode health JSON: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if payload.Status != "ok" || payload.Provider != "openai-responses" || !payload.Authenticated {
		t.Fatalf("health = %+v", payload)
	}
	if len(payload.AllowedPaths) != 2 {
		t.Fatalf("allowed paths = %#v", payload.AllowedPaths)
	}
}
