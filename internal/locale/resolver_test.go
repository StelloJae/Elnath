package locale

import "testing"

func TestResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		current     DetectResult
		sessionLast string
		cfgLocale   string
		want        string
	}{
		{name: "high confidence korean", current: DetectResult{Lang: "ko", Confidence: 0.85}, want: "ko"},
		{name: "detection wins over session and config", current: DetectResult{Lang: "ko", Confidence: 0.85}, sessionLast: "en", cfgLocale: "en", want: "ko"},
		{name: "low confidence falls back to config", current: DetectResult{Lang: "en", Confidence: 0.3}, cfgLocale: "ko", want: "ko"},
		{name: "empty falls back to default english", current: DetectResult{Lang: "en", Confidence: 0.3}, want: "en"},
		{name: "below threshold without session stays english", current: DetectResult{Lang: "ko", Confidence: 0.55}, want: "en"},
		{name: "inherits session locale", current: DetectResult{Lang: "ko", Confidence: 0.55}, sessionLast: "ko", want: "ko"},
		{name: "short confirm inherits session locale", current: DetectResult{Lang: "en", Confidence: 0.0}, sessionLast: "ko", want: "ko"},
		{name: "short first turn defaults english", current: DetectResult{Lang: "en", Confidence: 0.0}, want: "en"},
		{name: "high confidence japanese wins", current: DetectResult{Lang: "ja", Confidence: 0.7}, sessionLast: "ko", cfgLocale: "ko", want: "ja"},
		{name: "auto normalizes to empty", current: DetectResult{Lang: "en", Confidence: 0.3}, cfgLocale: "auto", want: "en"},
		{name: "english session suppresses flip", current: DetectResult{Lang: "ko", Confidence: 0.35}, sessionLast: "en", cfgLocale: "ko", want: "en"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := Resolve(tc.current, tc.sessionLast, NormalizeConfig(tc.cfgLocale))
			if got != tc.want {
				t.Fatalf("Resolve(%+v, %q, %q) = %q, want %q", tc.current, tc.sessionLast, tc.cfgLocale, got, tc.want)
			}
		})
	}
}

func TestNormalizeConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{input: "", want: ""},
		{input: "auto", want: ""},
		{input: " AUTO ", want: ""},
		{input: " KO ", want: "ko"},
		{input: "ja", want: "ja"},
	}

	for _, tc := range tests {
		if got := NormalizeConfig(tc.input); got != tc.want {
			t.Fatalf("NormalizeConfig(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestResponseDirective(t *testing.T) {
	t.Parallel()

	tests := []struct {
		lang string
		want string
	}{
		{lang: "", want: ""},
		{lang: "en", want: ""},
		{lang: "ko", want: "Respond in Korean."},
		{lang: "ja", want: "Respond in Japanese."},
		{lang: "zh", want: "Respond in Chinese."},
		{lang: "fr", want: ""},
	}

	for _, tc := range tests {
		if got := ResponseDirective(tc.lang); got != tc.want {
			t.Fatalf("ResponseDirective(%q) = %q, want %q", tc.lang, got, tc.want)
		}
	}
}

func TestLangToEnglishName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		lang string
		want string
	}{
		{lang: "ko", want: "Korean"},
		{lang: "ja", want: "Japanese"},
		{lang: "zh", want: "Chinese"},
		{lang: "en", want: ""},
		{lang: "fr", want: ""},
	}

	for _, tc := range tests {
		if got := LangToEnglishName(tc.lang); got != tc.want {
			t.Fatalf("LangToEnglishName(%q) = %q, want %q", tc.lang, got, tc.want)
		}
	}
}
