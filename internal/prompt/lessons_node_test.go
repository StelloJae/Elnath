package prompt

import (
	"context"
	"errors"
	"strings"
	testing "testing"
	"time"

	"github.com/stello/elnath/internal/learning"
)

type mockLessonLister struct {
	lessons []learning.Lesson
	err     error
}

func (m *mockLessonLister) Recent(n int) ([]learning.Lesson, error) {
	if m.err != nil {
		return nil, m.err
	}
	if n > 0 && n < len(m.lessons) {
		return m.lessons[:n], nil
	}
	return m.lessons, nil
}

func TestLessonsNodeNilStore(t *testing.T) {
	t.Parallel()

	got, err := NewLessonsNode(87, nil, 10, 1000).Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestLessonsNodeEmptyStore(t *testing.T) {
	t.Parallel()

	got, err := NewLessonsNode(87, &mockLessonLister{}, 10, 1000).Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestLessonsNodeRendersRecentLessons(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	got, err := NewLessonsNode(87, &mockLessonLister{lessons: []learning.Lesson{
		{Created: base, Text: "first lesson"},
		{Created: base.Add(24 * time.Hour), Text: "second lesson"},
		{Created: base.Add(48 * time.Hour), Text: "third lesson"},
	}}, 10, 1000).Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	for _, want := range []string{
		"Recent lessons:",
		"- [2026-04-13] first lesson",
		"- [2026-04-14] second lesson",
		"- [2026-04-15] third lesson",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render = %q, want substring %q", got, want)
		}
	}
}

func TestLessonsNodeSkipsBenchmarkMode(t *testing.T) {
	t.Parallel()

	got, err := NewLessonsNode(87, &mockLessonLister{lessons: []learning.Lesson{{Text: "lesson"}}}, 10, 1000).Render(context.Background(), &RenderState{BenchmarkMode: true})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestLessonsNodeRespectsMaxChars(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	got, err := NewLessonsNode(87, &mockLessonLister{lessons: []learning.Lesson{
		{Created: base, Text: strings.Repeat("a", 40)},
		{Created: base.Add(24 * time.Hour), Text: strings.Repeat("b", 40)},
		{Created: base.Add(48 * time.Hour), Text: strings.Repeat("c", 40)},
	}}, 10, 90).Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, strings.Repeat("a", 40)) {
		t.Fatalf("Render = %q, want first lesson retained", got)
	}
	if strings.Contains(got, strings.Repeat("c", 40)) {
		t.Fatalf("Render = %q, want later lesson dropped by maxChars", got)
	}
	if got == "Recent lessons:\n" {
		t.Fatal("Render contained only header, want at least one lesson")
	}
}

func TestLessonsNodeStoreErrorIsIgnored(t *testing.T) {
	t.Parallel()

	got, err := NewLessonsNode(87, &mockLessonLister{err: errors.New("boom")}, 10, 1000).Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

type mockFilteredLessonLister struct {
	mockLessonLister
	filtered []learning.Lesson
	filterErr error
}

func (m *mockFilteredLessonLister) ListFiltered(f learning.Filter) ([]learning.Lesson, error) {
	if m.filterErr != nil {
		return nil, m.filterErr
	}
	return m.filtered, nil
}

func TestLessonsNodeWithProjectID(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	recentLessons := []learning.Lesson{
		{Created: base, Text: "recent lesson"},
	}
	filteredLessons := []learning.Lesson{
		{Created: base, Text: "project lesson one"},
		{Created: base.Add(24 * time.Hour), Text: "project lesson two"},
		{Created: base.Add(48 * time.Hour), Text: "project lesson three"},
	}
	mock := &mockFilteredLessonLister{
		mockLessonLister: mockLessonLister{lessons: recentLessons},
		filtered:         filteredLessons,
	}
	got, err := NewLessonsNode(87, mock, 10, 1000).Render(context.Background(), &RenderState{ProjectID: "elnath"})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	for _, want := range []string{"project lesson one", "project lesson two", "project lesson three"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render = %q, want substring %q", got, want)
		}
	}
	if strings.Contains(got, "recent lesson") {
		t.Fatalf("Render = %q, should not contain fallback lesson", got)
	}
}

func TestLessonsNodeProjectIDFallback(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	recentLessons := []learning.Lesson{
		{Created: base, Text: "fallback lesson one"},
		{Created: base.Add(24 * time.Hour), Text: "fallback lesson two"},
	}
	mock := &mockFilteredLessonLister{
		mockLessonLister: mockLessonLister{lessons: recentLessons},
		filtered:         []learning.Lesson{{Created: base, Text: "sparse"}},
	}
	got, err := NewLessonsNode(87, mock, 10, 1000).Render(context.Background(), &RenderState{ProjectID: "elnath"})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "fallback lesson one") {
		t.Fatalf("Render = %q, want fallback lesson", got)
	}
}

func TestLessonsNodeProjectIDNoFilterSupport(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	mock := &mockLessonLister{lessons: []learning.Lesson{
		{Created: base, Text: "only recent lesson"},
	}}
	got, err := NewLessonsNode(87, mock, 10, 1000).Render(context.Background(), &RenderState{ProjectID: "elnath"})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "only recent lesson") {
		t.Fatalf("Render = %q, want recent lesson as graceful fallback", got)
	}
}

func TestLessonsNodeDefaultsNameAndPriority(t *testing.T) {
	t.Parallel()

	node := NewLessonsNode(87, nil, 0, 0)
	if node.maxEntries != 10 {
		t.Fatalf("maxEntries = %d, want 10", node.maxEntries)
	}
	if node.maxChars != 1000 {
		t.Fatalf("maxChars = %d, want 1000", node.maxChars)
	}
	if got := node.Name(); got != "lessons" {
		t.Fatalf("Name = %q, want lessons", got)
	}
	if got := node.Priority(); got != 87 {
		t.Fatalf("Priority = %d, want 87", got)
	}
}
