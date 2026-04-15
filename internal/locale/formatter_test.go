package locale

import (
	"testing"
	"time"
)

func TestFormatTimeAndDate(t *testing.T) {
	loc, err := time.LoadLocation("Local")
	if err != nil {
		loc = time.UTC
	}

	base := time.Date(2024, time.March, 15, 14, 30, 0, 0, loc)
	defaultTime := base.Format("2006-01-02 15:04 MST")

	tests := []struct {
		locale   string
		wantTime string
		wantDate string
	}{
		{locale: "ko", wantTime: "2024년 3월 15일 14:30", wantDate: "2024년 3월 15일"},
		{locale: "ja", wantTime: "2024年3月15日 14:30", wantDate: "2024年3月15日"},
		{locale: "zh", wantTime: "2024年3月15日 14:30", wantDate: "2024年3月15日"},
		{locale: "en", wantTime: defaultTime, wantDate: "2024-03-15"},
		{locale: "fr", wantTime: defaultTime, wantDate: "2024-03-15"},
	}

	for _, tc := range tests {
		t.Run(tc.locale, func(t *testing.T) {
			if got := FormatTime(base, tc.locale); got != tc.wantTime {
				t.Fatalf("FormatTime(%q) = %q, want %q", tc.locale, got, tc.wantTime)
			}
			if got := FormatDate(base, tc.locale); got != tc.wantDate {
				t.Fatalf("FormatDate(%q) = %q, want %q", tc.locale, got, tc.wantDate)
			}
		})
	}
}
