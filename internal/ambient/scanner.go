package ambient

import (
	"log/slog"
	"strings"

	"github.com/stello/elnath/internal/wiki"
)

// Scanner finds boot tasks in the wiki store.
type Scanner struct {
	store  *wiki.Store
	logger *slog.Logger
}

// NewScanner creates a Scanner backed by the given wiki store.
func NewScanner(store *wiki.Store, logger *slog.Logger) *Scanner {
	return &Scanner{store: store, logger: logger}
}

// Scan returns all valid boot tasks found under the "boot/" prefix in the wiki.
// Pages with missing or unparseable schedules are skipped with a warning log.
func (s *Scanner) Scan() ([]BootTask, error) {
	pages, err := s.store.List()
	if err != nil {
		return nil, err
	}

	var tasks []BootTask
	for _, page := range pages {
		if !strings.HasPrefix(page.Path, "boot/") {
			continue
		}
		if page.Type != wiki.PageTypeBootTask {
			continue
		}

		scheduleRaw, _ := page.Extra["schedule"].(string)
		sched, err := ParseSchedule(scheduleRaw)
		if err != nil {
			s.logger.Warn("ambient: skipping boot task with invalid schedule",
				"path", page.Path,
				"schedule", scheduleRaw,
				"err", err,
			)
			continue
		}

		silent, _ := page.Extra["silent"].(bool)

		tasks = append(tasks, BootTask{
			Path:     page.Path,
			Title:    page.Title,
			Prompt:   page.Content,
			Schedule: sched,
			Silent:   silent,
			Tags:     page.Tags,
		})
	}

	return tasks, nil
}
