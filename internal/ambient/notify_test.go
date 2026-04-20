package ambient

import (
	"strings"
	"testing"
)

func TestFormatNotification(t *testing.T) {
	cases := []struct {
		name       string
		title      string
		body       string
		persona    string
		locale     string
		wantTitle  string
		wantBody   string
		wantFooter string
	}{
		{
			name:       "en no persona",
			title:      "Morning digest",
			body:       "3 new items in the inbox.",
			locale:     "en",
			wantTitle:  "**Morning digest**",
			wantBody:   "3 new items in the inbox.",
			wantFooter: "— Elnath ambient",
		},
		{
			name:       "ko no persona",
			title:      "아침 브리핑",
			body:       "받은 편지함에 새 항목 3건.",
			locale:     "ko",
			wantTitle:  "**아침 브리핑**",
			wantBody:   "받은 편지함에 새 항목 3건.",
			wantFooter: "— 엘나트 ambient",
		},
		{
			name:       "en with persona",
			title:      "Hourly check",
			body:       "No failures.",
			persona:    "cozy assistant",
			locale:     "en",
			wantTitle:  "**Hourly check**",
			wantBody:   "No failures.",
			wantFooter: "— Elnath ambient · cozy assistant",
		},
		{
			name:       "ko with persona",
			title:      "정기 점검",
			body:       "실패 없음.",
			persona:    "아늑한 동료",
			locale:     "ko",
			wantTitle:  "**정기 점검**",
			wantBody:   "실패 없음.",
			wantFooter: "— 엘나트 ambient · 아늑한 동료",
		},
		{
			name:       "auto locale falls back to english",
			title:      "Fallback",
			body:       "Body.",
			locale:     "auto",
			wantTitle:  "**Fallback**",
			wantBody:   "Body.",
			wantFooter: "— Elnath ambient",
		},
		{
			name:       "whitespace locale normalises",
			title:      "Pad",
			body:       "b",
			locale:     "  KO  ",
			wantTitle:  "**Pad**",
			wantBody:   "b",
			wantFooter: "— 엘나트 ambient",
		},
		{
			name:       "failure body prefix preserved verbatim",
			title:      "Research probe",
			body:       "Task failed: provider timeout",
			locale:     "en",
			wantTitle:  "**Research probe**",
			wantBody:   "Task failed: provider timeout",
			wantFooter: "— Elnath ambient",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatNotification(tc.title, tc.body, tc.persona, tc.locale)
			if !strings.Contains(got, tc.wantTitle) {
				t.Errorf("missing title %q in:\n%s", tc.wantTitle, got)
			}
			if !strings.Contains(got, tc.wantBody) {
				t.Errorf("missing body %q in:\n%s", tc.wantBody, got)
			}
			if !strings.Contains(got, tc.wantFooter) {
				t.Errorf("missing footer %q in:\n%s", tc.wantFooter, got)
			}
			if idx := strings.Index(got, tc.wantFooter); idx <= strings.Index(got, tc.wantBody) {
				t.Errorf("footer must come after body; got:\n%s", got)
			}
		})
	}
}

func TestFormatNotification_EmptyTitleAndBody(t *testing.T) {
	got := FormatNotification("", "", "", "en")
	if got == "" {
		t.Fatal("expected non-empty output even with empty title/body")
	}
	if !strings.Contains(got, "— Elnath ambient") {
		t.Errorf("expected signature in output, got:\n%s", got)
	}
	if strings.Contains(got, "****") {
		t.Errorf("expected no empty bold block, got:\n%s", got)
	}
}

func TestFormatNotification_PersonaTrimmed(t *testing.T) {
	got := FormatNotification("t", "b", "   spaced  ", "en")
	if !strings.Contains(got, "— Elnath ambient · spaced") {
		t.Errorf("persona should be trimmed; got:\n%s", got)
	}
}
