package prompt

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/stello/elnath/internal/learning"
)

// minTopicLessons is the smallest number of topic-filtered lessons we accept
// before falling back to global recent lessons. Below this, a single-lesson
// hit is too noisy to be useful for the model; we prefer broader recency.
const minTopicLessons = 3

type LessonLister interface {
	Recent(n int) ([]learning.Lesson, error)
}

type LessonFilteredLister interface {
	LessonLister
	ListFiltered(f learning.Filter) ([]learning.Lesson, error)
}

type LessonsNode struct {
	priority   int
	store      LessonLister
	maxEntries int
	maxChars   int
}

func NewLessonsNode(priority int, store LessonLister, maxEntries, maxChars int) *LessonsNode {
	if maxEntries <= 0 {
		maxEntries = 10
	}
	if maxChars <= 0 {
		maxChars = 1000
	}
	return &LessonsNode{priority: priority, store: store, maxEntries: maxEntries, maxChars: maxChars}
}

func (n *LessonsNode) Name() string { return "lessons" }

func (n *LessonsNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *LessonsNode) Render(_ context.Context, state *RenderState) (string, error) {
	if n == nil || n.store == nil {
		return "", nil
	}
	if state != nil && state.BenchmarkMode {
		return "", nil
	}

	var lessons []learning.Lesson
	var err error
	if state != nil && state.ProjectID != "" {
		if fl, ok := n.store.(LessonFilteredLister); ok {
			lessons, err = fl.ListFiltered(learning.Filter{Topic: state.ProjectID, Limit: n.maxEntries, Reverse: true})
			if err != nil || len(lessons) < minTopicLessons {
				lessons, err = n.store.Recent(n.maxEntries)
			}
		} else {
			lessons, err = n.store.Recent(n.maxEntries)
		}
	} else {
		lessons, err = n.store.Recent(n.maxEntries)
	}
	if err != nil {
		slog.Warn("lessons node: store read failed", "error", err)
		return "", nil
	}
	if len(lessons) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("Recent lessons:\n")
	used := utf8.RuneCountInString(b.String())
	for _, lesson := range lessons {
		line := fmt.Sprintf("\n- [%s] %s", lesson.Created.Format("2006-01-02"), lesson.Text)
		lineChars := utf8.RuneCountInString(line)
		if used+lineChars > n.maxChars {
			break
		}
		b.WriteString(line)
		used += lineChars
	}
	if used == utf8.RuneCountInString("Recent lessons:\n") {
		return "", nil
	}
	return b.String(), nil
}
