package learning

import "context"

type LLMExtractor interface {
	Extract(ctx context.Context, req ExtractRequest) ([]Lesson, error)
}

type ExtractRequest struct {
	SessionID       string
	Topic           string
	Workflow        string
	CompactSummary  string
	ToolStats       []AgentToolStat
	FinishReason    string
	Iterations      int
	MaxIterations   int
	RetryCount      int
	ExistingLessons []LessonManifestEntry
	SinceLine       int
}

type LessonManifestEntry struct {
	ID    string
	Topic string
	Text  string
}

type MockLLMExtractor struct {
	Lessons []Lesson
	Err     error
}

func (m *MockLLMExtractor) Extract(_ context.Context, _ ExtractRequest) ([]Lesson, error) {
	if m == nil {
		return nil, nil
	}
	if m.Err != nil {
		return nil, m.Err
	}
	out := make([]Lesson, len(m.Lessons))
	copy(out, m.Lessons)
	for i := range out {
		if out[i].Evidence != nil {
			out[i].Evidence = append([]string(nil), out[i].Evidence...)
		}
		if out[i].PersonaDelta != nil {
			out[i].PersonaDelta = append(out[i].PersonaDelta[:0:0], out[i].PersonaDelta...)
		}
	}
	return out, nil
}
