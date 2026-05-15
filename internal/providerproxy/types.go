package providerproxy

import (
	"context"
	"time"
)

const (
	DefaultHost = "127.0.0.1"
	DefaultPort = 8645
)

type Credential struct {
	Bearer      string
	TokenType   string
	BaseURL     string
	ExpiresAt   time.Time
	Refreshable bool
}

type Adapter interface {
	Name() string
	DisplayName() string
	AllowedPaths() []string
	Credential(context.Context) (Credential, error)
}

type ProviderStatus struct {
	Provider         string   `json:"provider"`
	DisplayName      string   `json:"display_name"`
	Ready            bool     `json:"ready"`
	Authenticated    bool     `json:"authenticated"`
	APIKeyConfigured bool     `json:"api_key_configured"`
	BaseURL          string   `json:"base_url,omitempty"`
	AllowedPaths     []string `json:"allowed_paths"`
	Refreshable      bool     `json:"refreshable"`
	AuthFailure      string   `json:"auth_failure,omitempty"`
}

type HealthResponse struct {
	Status        string   `json:"status"`
	Provider      string   `json:"provider"`
	DisplayName   string   `json:"display_name"`
	Authenticated bool     `json:"authenticated"`
	BaseURL       string   `json:"base_url,omitempty"`
	AllowedPaths  []string `json:"allowed_paths"`
	AuthFailure   string   `json:"auth_failure,omitempty"`
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
