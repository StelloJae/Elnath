package scorecard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeLessonsFile(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "lessons.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write lessons: %v", err)
	}
	return p
}

func TestComputeLessonExtractionUnknown(t *testing.T) {
	got := computeLessonExtraction(SourcesPaths{LessonsPath: "/nope"}, time.Now())
	if got.Score != ScoreUnknown {
		t.Errorf("missing: got %v", got.Score)
	}
}

func TestComputeLessonExtractionNascent(t *testing.T) {
	p := writeLessonsFile(t, []string{
		`{"id":"1","topic":"t","content":"c","tags":[]}`,
		`{"id":"2","topic":"t","content":"c","tags":[]}`,
	})
	got := computeLessonExtraction(SourcesPaths{LessonsPath: p}, time.Now())
	if got.Score != ScoreNascent {
		t.Errorf("2 lessons: got %v", got.Score)
	}
}

func TestComputeLessonExtractionDegradedAllActive(t *testing.T) {
	var lines []string
	for i := 0; i < 6; i++ {
		lines = append(lines, `{"id":"id`+twoDigits(i)+`","topic":"t","content":"c","tags":[]}`)
	}
	p := writeLessonsFile(t, lines)
	got := computeLessonExtraction(SourcesPaths{LessonsPath: p}, time.Now())
	if got.Score != ScoreDegraded {
		t.Errorf("6 lessons none superseded: got %v (%s)", got.Score, got.Reason)
	}
}

func TestComputeLessonExtractionOK(t *testing.T) {
	var lines []string
	for i := 0; i < 6; i++ {
		sup := ""
		if i >= 3 {
			sup = `,"superseded_by":"synth-x"`
		}
		lines = append(lines, `{"id":"id`+twoDigits(i)+`","topic":"t","content":"c","tags":[]`+sup+`}`)
	}
	p := writeLessonsFile(t, lines)
	got := computeLessonExtraction(SourcesPaths{LessonsPath: p}, time.Now())
	if got.Score != ScoreOK {
		t.Errorf("6 lessons 3 superseded: got %v (%s)", got.Score, got.Reason)
	}
	if got.Metrics["lessons_active"] != 3 || got.Metrics["lessons_superseded"] != 3 {
		t.Errorf("split counts wrong: %v", got.Metrics)
	}
}
