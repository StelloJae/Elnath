package main

import (
	"testing"

	"github.com/stello/elnath/internal/config"
)

func TestResolveFallbackModel(t *testing.T) {
	t.Run("uses config field when set", func(t *testing.T) {
		cfg := &config.Config{FallbackModel: "gpt-custom"}
		if got := resolveFallbackModel(cfg); got != "gpt-custom" {
			t.Fatalf("resolveFallbackModel = %q, want %q", got, "gpt-custom")
		}
	})

	t.Run("defaults to centralized constant when cfg field empty", func(t *testing.T) {
		cfg := &config.Config{FallbackModel: ""}
		if got := resolveFallbackModel(cfg); got != "gpt-5.5" {
			t.Fatalf("resolveFallbackModel = %q, want %q", got, "gpt-5.5")
		}
	})

	t.Run("nil cfg falls back to centralized constant", func(t *testing.T) {
		if got := resolveFallbackModel(nil); got != "gpt-5.5" {
			t.Fatalf("resolveFallbackModel(nil) = %q, want %q", got, "gpt-5.5")
		}
	})
}
