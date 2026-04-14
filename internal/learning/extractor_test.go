package learning

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExtract(t *testing.T) {
	t.Parallel()

	longFindings := strings.Repeat("x", 300)

	tests := []struct {
		name       string
		result     ResultInfo
		wantCount  int
		assertions func(t *testing.T, lessons []Lesson)
	}{
		{
			name:      "empty result produces no lessons",
			result:    ResultInfo{},
			wantCount: 0,
		},
		{
			name: "supported high confidence round boosts persistence",
			result: ResultInfo{
				Topic: "go patterns",
				Rounds: []RoundInfo{{
					HypothesisID: "H1",
					Findings:     "prefer errors.Is over type assertions",
					Confidence:   "high",
					Supported:    true,
				}},
			},
			wantCount: 1,
			assertions: func(t *testing.T, lessons []Lesson) {
				t.Helper()
				if lessons[0].PersonaDelta[0].Param != "persistence" || lessons[0].PersonaDelta[0].Delta != 0.02 {
					t.Fatalf("PersonaDelta = %#v, want persistence +0.02", lessons[0].PersonaDelta)
				}
			},
		},
		{
			name: "majority low confidence boosts caution",
			result: ResultInfo{
				Topic:  "ml strategies",
				Rounds: []RoundInfo{{Confidence: "low"}, {Confidence: "low"}, {Confidence: "low"}},
			},
			wantCount: 1,
			assertions: func(t *testing.T, lessons []Lesson) {
				t.Helper()
				if len(lessons[0].PersonaDelta) != 2 {
					t.Fatalf("PersonaDelta len = %d, want 2", len(lessons[0].PersonaDelta))
				}
				if lessons[0].PersonaDelta[0].Param != "caution" || lessons[0].PersonaDelta[0].Delta != 0.03 {
					t.Fatalf("first delta = %#v, want caution +0.03", lessons[0].PersonaDelta[0])
				}
				if lessons[0].PersonaDelta[1].Param != "curiosity" || lessons[0].PersonaDelta[1].Delta != -0.01 {
					t.Fatalf("second delta = %#v, want curiosity -0.01", lessons[0].PersonaDelta[1])
				}
			},
		},
		{
			name: "half low confidence also boosts caution",
			result: ResultInfo{
				Topic: "go patterns",
				Rounds: []RoundInfo{
					{Findings: "finding A", Confidence: "high", Supported: true},
					{Findings: "finding B", Confidence: "low", Supported: false},
				},
			},
			wantCount: 2,
			assertions: func(t *testing.T, lessons []Lesson) {
				t.Helper()
				if lessons[1].PersonaDelta[0].Param != "caution" || lessons[1].PersonaDelta[0].Delta != 0.03 {
					t.Fatalf("PersonaDelta = %#v, want caution lesson on exact half", lessons[1].PersonaDelta)
				}
			},
		},
		{
			name: "high cost reduces verbosity",
			result: ResultInfo{
				Topic:     "budget topic",
				TotalCost: 3.5,
			},
			wantCount: 1,
			assertions: func(t *testing.T, lessons []Lesson) {
				t.Helper()
				if lessons[0].PersonaDelta[0].Param != "verbosity" || lessons[0].PersonaDelta[0].Delta != -0.02 {
					t.Fatalf("PersonaDelta = %#v, want verbosity -0.02", lessons[0].PersonaDelta)
				}
			},
		},
		{
			name: "multiple supported rounds plus cost create three lessons",
			result: ResultInfo{
				Topic:     "compound",
				TotalCost: 5.0,
				Rounds: []RoundInfo{
					{Findings: "finding A", Confidence: "high", Supported: true},
					{Findings: "finding B", Confidence: "high", Supported: true},
				},
			},
			wantCount: 3,
		},
		{
			name: "long findings are truncated",
			result: ResultInfo{
				Topic: "truncate",
				Rounds: []RoundInfo{{
					Findings:   longFindings,
					Confidence: "high",
					Supported:  true,
				}},
			},
			wantCount: 1,
			assertions: func(t *testing.T, lessons []Lesson) {
				t.Helper()
				if len(lessons[0].Text) != maxLessonTextLen {
					t.Fatalf("Text length = %d, want %d", len(lessons[0].Text), maxLessonTextLen)
				}
				if !strings.HasSuffix(lessons[0].Text, "...") {
					t.Fatalf("Text = %q, want ellipsis suffix", lessons[0].Text)
				}
			},
		},
		{
			name: "unicode findings are truncated without breaking runes",
			result: ResultInfo{
				Topic: "unicode",
				Rounds: []RoundInfo{{
					Findings:   strings.Repeat("가", 300),
					Confidence: "high",
					Supported:  true,
				}},
			},
			wantCount: 1,
			assertions: func(t *testing.T, lessons []Lesson) {
				t.Helper()
				if !utf8.ValidString(lessons[0].Text) {
					t.Fatalf("Text = %q, want valid UTF-8", lessons[0].Text)
				}
				if utf8.RuneCountInString(lessons[0].Text) != maxLessonTextLen {
					t.Fatalf("rune count = %d, want %d", utf8.RuneCountInString(lessons[0].Text), maxLessonTextLen)
				}
				if !strings.HasSuffix(lessons[0].Text, "...") {
					t.Fatalf("Text = %q, want ellipsis suffix", lessons[0].Text)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			lessons := Extract(tt.result)
			if len(lessons) != tt.wantCount {
				t.Fatalf("len(lessons) = %d, want %d", len(lessons), tt.wantCount)
			}
			for i, lesson := range lessons {
				if lesson.Created.IsZero() {
					t.Fatalf("lessons[%d].Created = zero, want timestamp", i)
				}
			}
			if tt.assertions != nil {
				tt.assertions(t, lessons)
			}
		})
	}
}
