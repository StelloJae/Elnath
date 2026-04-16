package ambient

import (
	"testing"
	"time"
)

func TestParseSchedule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    Schedule
		wantErr bool
	}{
		{
			name:  "startup",
			input: "startup",
			want:  Schedule{Type: ScheduleStartup},
		},
		{
			name:  "every 30m",
			input: "every 30m",
			want:  Schedule{Type: ScheduleInterval, Interval: 30 * time.Minute},
		},
		{
			name:  "every 6h",
			input: "every 6h",
			want:  Schedule{Type: ScheduleInterval, Interval: 6 * time.Hour},
		},
		{
			name:  "daily 09:00",
			input: "daily 09:00",
			want:  Schedule{Type: ScheduleDaily, DailyAt: TimeOfDay{Hour: 9, Minute: 0}},
		},
		{
			name:  "daily 22:30",
			input: "daily 22:30",
			want:  Schedule{Type: ScheduleDaily, DailyAt: TimeOfDay{Hour: 22, Minute: 30}},
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
		{
			name:    "unknown format",
			input:   "weekly monday",
			wantErr: true,
		},
		{
			name:    "bad duration",
			input:   "every notaduration",
			wantErr: true,
		},
		{
			name:    "bad time",
			input:   "daily 25:99",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseSchedule(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ParseSchedule(%q) expected error, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSchedule(%q) unexpected error: %v", tc.input, err)
			}
			if got.Type != tc.want.Type {
				t.Errorf("Type = %v, want %v", got.Type, tc.want.Type)
			}
			if got.Interval != tc.want.Interval {
				t.Errorf("Interval = %v, want %v", got.Interval, tc.want.Interval)
			}
			if got.DailyAt != tc.want.DailyAt {
				t.Errorf("DailyAt = %v, want %v", got.DailyAt, tc.want.DailyAt)
			}
		})
	}
}

func TestNextDailyRun(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	// Base: 2024-01-15 10:00:00 UTC
	base := time.Date(2024, 1, 15, 10, 0, 0, 0, loc)

	tests := []struct {
		name string
		now  time.Time
		tod  TimeOfDay
		// wantMin / wantMax define acceptable range for the returned duration.
		wantMin time.Duration
		wantMax time.Duration
	}{
		{
			name:    "before target same day",
			now:     base, // 10:00
			tod:     TimeOfDay{Hour: 14, Minute: 30},
			wantMin: 4*time.Hour + 29*time.Minute + 59*time.Second,
			wantMax: 4*time.Hour + 30*time.Minute + 1*time.Second,
		},
		{
			name:    "after target rolls to next day",
			now:     base, // 10:00
			tod:     TimeOfDay{Hour: 8, Minute: 0},
			wantMin: 21*time.Hour + 59*time.Minute + 59*time.Second,
			wantMax: 22*time.Hour + 1*time.Second,
		},
		{
			name:    "exactly at target rolls to next day",
			now:     time.Date(2024, 1, 15, 9, 0, 0, 0, loc),
			tod:     TimeOfDay{Hour: 9, Minute: 0},
			wantMin: 23*time.Hour + 59*time.Minute + 59*time.Second,
			wantMax: 24*time.Hour + 1*time.Second,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NextDailyRun(tc.now, tc.tod)
			if got < tc.wantMin || got > tc.wantMax {
				t.Errorf("NextDailyRun = %v, want in [%v, %v]", got, tc.wantMin, tc.wantMax)
			}
		})
	}
}
