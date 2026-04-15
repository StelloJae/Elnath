package locale

import "testing"

func TestDetectLanguage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        string
		wantLang     string
		minConf      float64
		wantZeroConf bool
	}{
		{name: "pure korean", input: "안녕하세요 반갑습니다", wantLang: "ko", minConf: 0.8},
		{name: "mixed korean english", input: "안녕 hi", wantLang: "ko", minConf: 0.5},
		{name: "pure english", input: "hello world how are you", wantLang: "en", minConf: 0.8},
		{name: "japanese hiragana", input: "こんにちは世界", wantLang: "ja", minConf: 0.6},
		{name: "japanese katakana", input: "コンピュータ", wantLang: "ja", minConf: 0.6},
		{name: "chinese han", input: "你好世界", wantLang: "zh", minConf: 0.6},
		{name: "short english", input: "ok", wantLang: "en", wantZeroConf: true},
		{name: "short korean", input: "네", wantLang: "en", wantZeroConf: true},
		{name: "symbols only", input: "123 !@#", wantLang: "en", wantZeroConf: true},
		{name: "empty", input: "", wantLang: "en", wantZeroConf: true},
		{name: "bilingual mixed", input: "안녕 이 API 호출해줘", wantLang: "ko", minConf: 0.3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lang, conf := DetectLanguage(tc.input)
			if lang != tc.wantLang {
				t.Fatalf("DetectLanguage(%q) lang = %q, want %q", tc.input, lang, tc.wantLang)
			}
			if tc.wantZeroConf {
				if conf != 0 {
					t.Fatalf("DetectLanguage(%q) confidence = %.2f, want 0", tc.input, conf)
				}
				return
			}
			if conf < tc.minConf {
				t.Fatalf("DetectLanguage(%q) confidence = %.2f, want >= %.2f", tc.input, conf, tc.minConf)
			}
		})
	}
}
