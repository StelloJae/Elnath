package ambient

import (
	"fmt"
	"strings"
	"time"
)

// BootTask represents a scheduled task defined in the wiki under boot/.
type BootTask struct {
	Path     string
	Title    string
	Prompt   string
	Schedule Schedule
	Silent   bool
	Tags     []string
}

// ScheduleType classifies how a BootTask is triggered.
type ScheduleType int

const (
	ScheduleStartup  ScheduleType = iota // run once on daemon start
	ScheduleInterval                     // run every Interval
	ScheduleDaily                        // run once per day at DailyAt
)

// Schedule defines when a BootTask should run.
type Schedule struct {
	Type     ScheduleType
	Interval time.Duration
	DailyAt  TimeOfDay
}

// TimeOfDay is an HH:MM clock time used for daily schedules.
type TimeOfDay struct {
	Hour   int
	Minute int
}

// ParseSchedule parses a schedule string from a boot-task wiki page.
//
// Accepted forms:
//
//	"startup"          → ScheduleStartup
//	"every <dur>"      → ScheduleInterval (e.g. "every 30m", "every 6h")
//	"daily HH:MM"      → ScheduleDaily
func ParseSchedule(raw string) (Schedule, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Schedule{}, fmt.Errorf("ambient: schedule string is empty")
	}

	if raw == "startup" {
		return Schedule{Type: ScheduleStartup}, nil
	}

	if strings.HasPrefix(raw, "every ") {
		durStr := strings.TrimPrefix(raw, "every ")
		d, err := time.ParseDuration(durStr)
		if err != nil {
			return Schedule{}, fmt.Errorf("ambient: invalid interval %q: %w", durStr, err)
		}
		return Schedule{Type: ScheduleInterval, Interval: d}, nil
	}

	if strings.HasPrefix(raw, "daily ") {
		timeStr := strings.TrimPrefix(raw, "daily ")
		var h, m int
		if _, err := fmt.Sscanf(timeStr, "%d:%d", &h, &m); err != nil {
			return Schedule{}, fmt.Errorf("ambient: invalid daily time %q: expected HH:MM", timeStr)
		}
		if h < 0 || h > 23 || m < 0 || m > 59 {
			return Schedule{}, fmt.Errorf("ambient: invalid daily time %q: hour or minute out of range", timeStr)
		}
		return Schedule{Type: ScheduleDaily, DailyAt: TimeOfDay{Hour: h, Minute: m}}, nil
	}

	return Schedule{}, fmt.Errorf("ambient: unknown schedule format %q", raw)
}

// NextDailyRun returns the duration until the next occurrence of tod after now.
// If tod has not yet passed today, the next run is today; otherwise it is tomorrow.
// Uses AddDate(0,0,1) for DST-safe next-day calculation.
func NextDailyRun(now time.Time, tod TimeOfDay) time.Duration {
	target := time.Date(now.Year(), now.Month(), now.Day(), tod.Hour, tod.Minute, 0, 0, now.Location())
	if !now.Before(target) {
		// Target has passed (or is exactly now); advance to the same time tomorrow.
		target = target.AddDate(0, 0, 1)
	}
	return target.Sub(now)
}
