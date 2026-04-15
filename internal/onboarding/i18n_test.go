package onboarding

import "testing"

func TestT_EnglishLookup(t *testing.T) {
	got := T(En, "welcome.title")
	want := "Welcome to Elnath"
	if got != want {
		t.Errorf("T(En, welcome.title) = %q, want %q", got, want)
	}
}

func TestT_KoreanLookup(t *testing.T) {
	got := T(Ko, "welcome.title")
	want := "Elnath에 오신 것을 환영합니다"
	if got != want {
		t.Errorf("T(Ko, welcome.title) = %q, want %q", got, want)
	}
}

func TestT_FallbackToEnglish(t *testing.T) {
	got := T(Locale("fr"), "welcome.title")
	want := "Welcome to Elnath"
	if got != want {
		t.Errorf("T(fr, welcome.title) = %q, want %q (expected English fallback)", got, want)
	}
}

func TestT_MissingKeyReturnsKey(t *testing.T) {
	key := "nonexistent.key"
	got := T(En, key)
	if got != key {
		t.Errorf("T(En, %q) = %q, want key returned as-is", key, got)
	}
}

func TestTOptional_ReturnsEmptyForMissingKeys(t *testing.T) {
	if got := TOptional(En, "nonexistent.key"); got != "" {
		t.Fatalf("TOptional(En, missing) = %q, want empty string", got)
	}
	if got := TOptional(Ko, "cmd.run.help"); got != "" {
		t.Fatalf("TOptional(Ko, cmd.run.help) = %q, want empty placeholder", got)
	}
}

func TestLocales(t *testing.T) {
	locales := Locales()
	if len(locales) != 2 {
		t.Fatalf("expected 2 locales, got %d", len(locales))
	}
	if locales[0] != En || locales[1] != Ko {
		t.Errorf("expected [en, ko], got %v", locales)
	}
}
