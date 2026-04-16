package errorclass

import (
	"errors"
	"fmt"
	"testing"
)

func assertClassification(t *testing.T, got ClassifiedError, wantCategory Category, wantRecovery Recovery) {
	t.Helper()
	if got.Category != wantCategory {
		t.Fatalf("Category = %q, want %q", got.Category, wantCategory)
	}
	if got.Recovery != wantRecovery {
		t.Fatalf("Recovery = %#v, want %#v", got.Recovery, wantRecovery)
	}
	if got.Message == "" {
		t.Fatal("Message must not be empty")
	}
}

func TestClassify_RateLimit429(t *testing.T) {
	got := Classify(errors.New("anthropic: status 429: rate limit (429)"), Context{StatusCode: 429})
	assertClassification(t, got, RateLimit, Recovery{Retryable: true, ShouldRotateCred: true})
}

func TestClassify_RateLimitRaw429WithProviderContext(t *testing.T) {
	got := Classify(errors.New("upstream returned 429"), Context{Provider: "mock"})
	assertClassification(t, got, RateLimit, Recovery{Retryable: true, ShouldRotateCred: true})
}

func TestClassify_RateLimitOpenAI(t *testing.T) {
	got := Classify(errors.New("OPENAI: HTTP 429: TOO MANY REQUESTS"), Context{})
	assertClassification(t, got, RateLimit, Recovery{Retryable: true, ShouldRotateCred: true})
}

func TestClassify_Overloaded529(t *testing.T) {
	got := Classify(errors.New("service overloaded (529)"), Context{StatusCode: 529})
	assertClassification(t, got, Overloaded, Recovery{Retryable: true})
}

func TestClassify_ServerError500(t *testing.T) {
	got := Classify(errors.New("anthropic: status 500: internal error"), Context{StatusCode: 500})
	assertClassification(t, got, ServerError, Recovery{Retryable: true})
}

func TestClassify_ServerErrorRaw500WithProviderContext(t *testing.T) {
	got := Classify(errors.New("upstream returned 500 Internal Server Error"), Context{Provider: "mock"})
	assertClassification(t, got, ServerError, Recovery{Retryable: true})
}

func TestClassify_ServerError502(t *testing.T) {
	got := Classify(errors.New("502 bad gateway"), Context{StatusCode: 502})
	assertClassification(t, got, ServerError, Recovery{Retryable: true})
}

func TestClassify_Auth401(t *testing.T) {
	got := Classify(errors.New("Unauthorized: invalid API key"), Context{})
	assertClassification(t, got, Auth, Recovery{ShouldRotateCred: true})
}

func TestClassify_AuthPermanent403(t *testing.T) {
	got := Classify(errors.New("403 account suspended"), Context{StatusCode: 403})
	assertClassification(t, got, AuthPermanent, Recovery{ShouldFallback: true})
}

func TestClassify_Billing402(t *testing.T) {
	got := Classify(errors.New("payment required"), Context{StatusCode: 402})
	assertClassification(t, got, Billing, Recovery{ShouldFallback: true})
}

func TestClassify_Billing402Transient(t *testing.T) {
	got := Classify(errors.New("402: try again later"), Context{StatusCode: 402})
	assertClassification(t, got, RateLimit, Recovery{Retryable: true, ShouldRotateCred: true})
}

func TestClassify_ContextOverflow(t *testing.T) {
	got := Classify(errors.New("context_length_exceeded"), Context{})
	assertClassification(t, got, ContextOverflow, Recovery{ShouldCompress: true})
}

func TestClassify_ContextOverflowHeuristic(t *testing.T) {
	got := Classify(errors.New("connection reset by peer"), Context{TokensUsed: 7001, ContextLimit: 10000})
	assertClassification(t, got, ContextOverflow, Recovery{ShouldCompress: true})
}

func TestClassify_PayloadTooLarge(t *testing.T) {
	got := Classify(errors.New("request too large"), Context{})
	assertClassification(t, got, PayloadTooLarge, Recovery{ShouldCompress: true})
}

func TestClassify_ModelNotFound(t *testing.T) {
	got := Classify(errors.New("model does not exist"), Context{})
	assertClassification(t, got, ModelNotFound, Recovery{ShouldFallback: true})
}

func TestClassify_Timeout(t *testing.T) {
	got := Classify(errors.New("context deadline exceeded"), Context{})
	assertClassification(t, got, Timeout, Recovery{Retryable: true})
}

func TestClassify_FormatError(t *testing.T) {
	got := Classify(errors.New("invalid request body"), Context{})
	assertClassification(t, got, FormatError, Recovery{})
}

func TestClassify_Unknown(t *testing.T) {
	got := Classify(errors.New("something unexpected"), Context{})
	assertClassification(t, got, Unknown, Recovery{})
}

func TestClassify_ThinkingExhausted(t *testing.T) {
	got := Classify(errors.New("thinking budget exhausted"), Context{EmptyVisibleResponse: true})
	assertClassification(t, got, ThinkingExhausted, Recovery{Retryable: true})
}

func TestClassify_OllamaError(t *testing.T) {
	got := Classify(errors.New("ollama: 503"), Context{StatusCode: 503})
	assertClassification(t, got, ServerError, Recovery{Retryable: true})
}

func TestClassifiedError_Unwrap(t *testing.T) {
	root := errors.New("root cause")
	wrapped := fmt.Errorf("provider failed: %w", root)
	classified := Classify(wrapped, Context{StatusCode: 500})

	if !errors.Is(&classified, root) {
		t.Fatal("errors.Is must reach the wrapped root cause")
	}

	var got *ClassifiedError
	if !errors.As(fmt.Errorf("outer: %w", &classified), &got) {
		t.Fatal("errors.As must recover ClassifiedError")
	}
	if got.Category != ServerError {
		t.Fatalf("Category = %q, want %q", got.Category, ServerError)
	}
}
