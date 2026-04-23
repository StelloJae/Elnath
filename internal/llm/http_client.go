package llm

import (
	"net/http"
	"time"
)

const (
	defaultHTTPMaxConnsPerHost = 8
	defaultHTTPTimeoutSeconds  = 120
)

// defaultTimeout converts a caller-supplied seconds value into a
// time.Duration, falling back to defaultHTTPTimeoutSeconds when the
// caller passes a non-positive number. Centralized so HTTP clients
// across providers (Anthropic, OpenAI, Codex) share the same fallback.
func defaultTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = defaultHTTPTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

func newHTTPClientWithPerHostCap(timeoutSeconds int) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if transport.MaxConnsPerHost == 0 {
		transport.MaxConnsPerHost = defaultHTTPMaxConnsPerHost
	}
	if transport.MaxIdleConnsPerHost == 0 || transport.MaxIdleConnsPerHost < transport.MaxConnsPerHost {
		transport.MaxIdleConnsPerHost = transport.MaxConnsPerHost
	}
	return &http.Client{
		Timeout:   defaultTimeout(timeoutSeconds),
		Transport: transport,
	}
}
