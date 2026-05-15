package providerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type ServerOptions struct {
	Client *http.Client
}

type openAIStyleError struct {
	Error openAIStyleErrorBody `json:"error"`
}

type openAIStyleErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"content-length":      {},
	"host":                {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailers":            {},
	"transfer-encoding":   {},
	"upgrade":             {},
	"authorization":       {},
}

func NewHandler(adapter Adapter, opts ServerOptions) http.Handler {
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		handleHealth(w, r, adapter)
	})
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		handleProxy(w, r, adapter, client)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeOpenAIStyleError(w, http.StatusNotFound, "path not found", "path_not_allowed")
	})
	return mux
}

func handleHealth(w http.ResponseWriter, r *http.Request, adapter Adapter) {
	if r.Method != http.MethodGet {
		writeOpenAIStyleError(w, http.StatusMethodNotAllowed, "method not allowed", "method_not_allowed")
		return
	}
	paths := sortedAllowedPaths(adapter.AllowedPaths())
	cred, err := adapter.Credential(r.Context())
	resp := HealthResponse{
		Status:        "ok",
		Provider:      adapter.Name(),
		DisplayName:   adapter.DisplayName(),
		AllowedPaths:  paths,
		Authenticated: err == nil && cred.Bearer != "",
	}
	if err == nil {
		resp.BaseURL = sanitizeBaseURLForOutput(cred.BaseURL)
	} else {
		resp.AuthFailure = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleProxy(w http.ResponseWriter, r *http.Request, adapter Adapter, client *http.Client) {
	relPath := "/" + strings.TrimPrefix(r.URL.Path, "/v1/")
	if !containsString(adapter.AllowedPaths(), relPath) {
		allowed := strings.Join(sortedAllowedPaths(adapter.AllowedPaths()), ", ")
		writeOpenAIStyleError(
			w,
			http.StatusNotFound,
			fmt.Sprintf("Path /v1%s is not forwarded by this proxy. Allowed: %s", relPath, allowed),
			"path_not_allowed",
		)
		return
	}

	cred, err := adapter.Credential(r.Context())
	if err != nil {
		writeOpenAIStyleError(w, http.StatusUnauthorized, err.Error(), "upstream_auth_failed")
		return
	}
	upstreamURL, err := buildUpstreamURL(cred.BaseURL, relPath, r.URL.RawQuery)
	if err != nil {
		writeOpenAIStyleError(w, http.StatusBadGateway, err.Error(), "upstream_url_invalid")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeOpenAIStyleError(w, http.StatusBadRequest, "request body read failed", "request_body_failed")
		return
	}
	defer r.Body.Close()

	ctx := r.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		writeOpenAIStyleError(w, http.StatusBadGateway, err.Error(), "upstream_request_invalid")
		return
	}
	copyForwardHeaders(req.Header, r.Header)
	tokenType := strings.TrimSpace(cred.TokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	req.Header.Set("Authorization", tokenType+" "+cred.Bearer)
	if len(body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		code := "upstream_unreachable"
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			code = "upstream_timeout"
			status = http.StatusGatewayTimeout
		}
		writeOpenAIStyleError(w, status, err.Error(), code)
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func buildUpstreamURL(baseURL, relPath, rawQuery string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return "", fmt.Errorf("upstream base_url is empty")
	}
	u, err := url.Parse(base + relPath)
	if err != nil {
		return "", fmt.Errorf("upstream base_url is invalid")
	}
	u.RawQuery = rawQuery
	return u.String(), nil
}

func copyForwardHeaders(dst, src http.Header) {
	for key, values := range src {
		if _, skip := hopByHopHeaders[strings.ToLower(key)]; skip {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		lower := strings.ToLower(key)
		if _, skip := hopByHopHeaders[lower]; skip {
			continue
		}
		if lower == "content-encoding" || lower == "content-length" {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func writeOpenAIStyleError(w http.ResponseWriter, status int, message, code string) {
	writeJSON(w, status, openAIStyleError{
		Error: openAIStyleErrorBody{
			Message: message,
			Type:    code,
			Code:    code,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func sortedAllowedPaths(paths []string) []string {
	out := append([]string(nil), paths...)
	sort.Strings(out)
	return out
}

func sanitizeBaseURLForOutput(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "REDACTED_INVALID_URL"
	}
	u.User = nil
	q := u.Query()
	for key, values := range q {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "key") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "auth") {
			for i := range values {
				values[i] = "REDACTED"
			}
			q[key] = values
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
