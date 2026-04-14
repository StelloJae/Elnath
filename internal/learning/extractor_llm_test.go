package learning

import (
	"context"
	"errors"
	"testing"

	"github.com/stello/elnath/internal/self"
)

func TestMockLLMExtractorExtractReturnsCopy(t *testing.T) {
	t.Parallel()

	extractor := &MockLLMExtractor{Lessons: []Lesson{{
		Text:         "first",
		Evidence:     []string{"one"},
		PersonaDelta: []self.Lesson{{Param: "caution", Delta: 0.01}},
	}}}
	got, err := extractor.Extract(context.Background(), ExtractRequest{})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	got[0].Text = "mutated"
	got[0].Evidence[0] = "changed"
	got[0].PersonaDelta[0].Delta = 0.5
	if extractor.Lessons[0].Text != "first" {
		t.Fatalf("mock fixture mutated to %q, want original preserved", extractor.Lessons[0].Text)
	}
	if extractor.Lessons[0].Evidence[0] != "one" {
		t.Fatalf("mock evidence mutated to %q, want original preserved", extractor.Lessons[0].Evidence[0])
	}
	if extractor.Lessons[0].PersonaDelta[0].Delta != 0.01 {
		t.Fatalf("mock persona delta mutated to %v, want original preserved", extractor.Lessons[0].PersonaDelta[0].Delta)
	}
}

func TestMockLLMExtractorExtractError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	extractor := &MockLLMExtractor{Err: wantErr}
	got, err := extractor.Extract(context.Background(), ExtractRequest{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Extract() error = %v, want %v", err, wantErr)
	}
	if got != nil {
		t.Fatalf("lessons = %#v, want nil", got)
	}
}

func TestMockLLMExtractorNilReceiver(t *testing.T) {
	t.Parallel()

	var extractor *MockLLMExtractor
	got, err := extractor.Extract(context.Background(), ExtractRequest{})
	if err != nil {
		t.Fatalf("Extract() error = %v, want nil", err)
	}
	if got != nil {
		t.Fatalf("lessons = %#v, want nil", got)
	}
}
