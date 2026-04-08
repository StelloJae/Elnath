package llm

import (
	"strings"
	"testing"
	"time"
)

func TestKeyPoolNext(t *testing.T) {
	p := NewKeyPool([]string{"key-a"})
	got, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "key-a" {
		t.Errorf("got %q, want key-a", got)
	}
}

func TestKeyPoolRoundRobin(t *testing.T) {
	p := NewKeyPool([]string{"key-1", "key-2", "key-3"})
	want := []string{"key-1", "key-2", "key-3", "key-1", "key-2"}
	for i, w := range want {
		got, err := p.Next()
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if got != w {
			t.Errorf("call %d: got %q, want %q", i, got, w)
		}
	}
}

func TestKeyPoolEmpty(t *testing.T) {
	p := NewKeyPool(nil)
	_, err := p.Next()
	if err == nil {
		t.Fatal("expected error for empty pool, got nil")
	}
	if !strings.Contains(err.Error(), "no keys configured") {
		t.Errorf("error = %q, want to contain 'no keys configured'", err.Error())
	}
}

func TestKeyPoolDuplicates(t *testing.T) {
	p := NewKeyPool([]string{"key-a", "key-a", "key-b", "key-a"})
	if p.Len() != 2 {
		t.Errorf("Len() = %d, want 2 (duplicates deduped)", p.Len())
	}
}

func TestKeyPoolEmptyStrings(t *testing.T) {
	p := NewKeyPool([]string{"", "key-a", "", "key-b"})
	if p.Len() != 2 {
		t.Errorf("Len() = %d, want 2 (empty strings dropped)", p.Len())
	}
}

func TestKeyPoolReportError429(t *testing.T) {
	p := NewKeyPool([]string{"key-a"})
	p.ReportError("key-a", 429)

	_, err := p.Next()
	if err == nil {
		t.Fatal("expected error: key should be on cooldown after 429")
	}
	if !strings.Contains(err.Error(), "cooldown") {
		t.Errorf("error = %q, want to contain 'cooldown'", err.Error())
	}

	// Verify the cooldown duration is approximately 1 hour.
	p.mu.Lock()
	remaining := time.Until(p.keys[0].CoolUntil)
	reason := p.keys[0].Reason
	p.mu.Unlock()

	if remaining < 59*time.Minute || remaining > 61*time.Minute {
		t.Errorf("429 cooldown remaining = %v, want ~1h", remaining)
	}
	if reason != "rate_limit" {
		t.Errorf("reason = %q, want rate_limit", reason)
	}
}

func TestKeyPoolReportError402(t *testing.T) {
	p := NewKeyPool([]string{"key-a"})
	p.ReportError("key-a", 402)

	_, err := p.Next()
	if err == nil {
		t.Fatal("expected error: key should be on cooldown after 402")
	}

	p.mu.Lock()
	remaining := time.Until(p.keys[0].CoolUntil)
	reason := p.keys[0].Reason
	p.mu.Unlock()

	if remaining < 23*time.Hour || remaining > 25*time.Hour {
		t.Errorf("402 cooldown remaining = %v, want ~24h", remaining)
	}
	if reason != "quota_exceeded" {
		t.Errorf("reason = %q, want quota_exceeded", reason)
	}
}

func TestKeyPoolReportError5xx(t *testing.T) {
	for _, code := range []int{500, 502, 503} {
		p := NewKeyPool([]string{"key-a"})
		p.ReportError("key-a", code)

		_, err := p.Next()
		if err == nil {
			t.Fatalf("code %d: expected error, got nil", code)
		}

		p.mu.Lock()
		remaining := time.Until(p.keys[0].CoolUntil)
		p.mu.Unlock()

		if remaining < 4*time.Minute || remaining > 6*time.Minute {
			t.Errorf("code %d: cooldown remaining = %v, want ~5m", code, remaining)
		}
	}
}

func TestKeyPoolReportErrorIgnored(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404} {
		p := NewKeyPool([]string{"key-a"})
		p.ReportError("key-a", code)

		got, err := p.Next()
		if err != nil {
			t.Fatalf("code %d: unexpected error: %v", code, err)
		}
		if got != "key-a" {
			t.Errorf("code %d: got %q, want key-a", code, got)
		}
	}
}

func TestKeyPoolAllCooledDown(t *testing.T) {
	p := NewKeyPool([]string{"key-1", "key-2"})
	p.ReportError("key-1", 429)
	p.ReportError("key-2", 429)

	_, err := p.Next()
	if err == nil {
		t.Fatal("expected error when all keys are cooled down")
	}
	if !strings.Contains(err.Error(), "cooldown") {
		t.Errorf("error = %q, want to contain 'cooldown'", err.Error())
	}
}

func TestKeyPoolActiveCount(t *testing.T) {
	p := NewKeyPool([]string{"key-1", "key-2", "key-3"})
	if p.ActiveCount() != 3 {
		t.Errorf("ActiveCount() = %d, want 3", p.ActiveCount())
	}

	p.ReportError("key-1", 429)
	if p.ActiveCount() != 2 {
		t.Errorf("after 1 cooldown: ActiveCount() = %d, want 2", p.ActiveCount())
	}

	p.ReportError("key-2", 429)
	if p.ActiveCount() != 1 {
		t.Errorf("after 2 cooldowns: ActiveCount() = %d, want 1", p.ActiveCount())
	}

	p.ReportError("key-3", 429)
	if p.ActiveCount() != 0 {
		t.Errorf("after 3 cooldowns: ActiveCount() = %d, want 0", p.ActiveCount())
	}
}

func TestKeyPoolLen(t *testing.T) {
	p := NewKeyPool([]string{"a", "b", "c"})
	if p.Len() != 3 {
		t.Errorf("Len() = %d, want 3", p.Len())
	}

	// Len counts cooled-down keys too.
	p.ReportError("a", 429)
	if p.Len() != 3 {
		t.Errorf("after cooldown: Len() = %d, want 3", p.Len())
	}
}
