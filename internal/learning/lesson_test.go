package learning

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/self"
)

func TestLessonJSONRoundtripBackwardsCompat(t *testing.T) {
	t.Parallel()

	raw := `{"id":"abc","text":"x","source":"agent","confidence":"medium","created":"2025-01-01T00:00:00Z"}`

	var lesson Lesson
	if err := json.Unmarshal([]byte(raw), &lesson); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if lesson.Rationale != "" {
		t.Fatalf("Rationale = %q, want empty", lesson.Rationale)
	}
	if len(lesson.Evidence) != 0 {
		t.Fatalf("Evidence length = %d, want 0", len(lesson.Evidence))
	}
	if lesson.PersonaParam != "" || lesson.PersonaDirection != "" || lesson.PersonaMagnitude != "" {
		t.Fatalf("persona hint fields = %#v, want zero values", lesson)
	}
}

func TestLessonJSONRoundtripAllFields(t *testing.T) {
	t.Parallel()

	want := Lesson{
		ID:         "abc12345",
		Text:       "Prefer shorter retries.",
		Topic:      "build",
		Source:     "agent:llm:single",
		Confidence: "high",
		PersonaDelta: []self.Lesson{{
			Param: "caution",
			Delta: 0.03,
		}},
		Rationale:        "The run repeated the same failing step.",
		Evidence:         []string{"bash failed three times", "same command retried"},
		PersonaParam:     "caution",
		PersonaDirection: "increase",
		PersonaMagnitude: "medium",
		Created:          time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got Lesson
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if got.ID != want.ID || got.Text != want.Text || got.Topic != want.Topic || got.Source != want.Source || got.Confidence != want.Confidence {
		t.Fatalf("roundtrip core fields = %#v, want %#v", got, want)
	}
	if got.Rationale != want.Rationale || got.PersonaParam != want.PersonaParam || got.PersonaDirection != want.PersonaDirection || got.PersonaMagnitude != want.PersonaMagnitude {
		t.Fatalf("roundtrip hint fields = %#v, want %#v", got, want)
	}
	if len(got.Evidence) != len(want.Evidence) || got.Evidence[0] != want.Evidence[0] || got.Evidence[1] != want.Evidence[1] {
		t.Fatalf("Evidence = %#v, want %#v", got.Evidence, want.Evidence)
	}
	if len(got.PersonaDelta) != 1 || got.PersonaDelta[0] != want.PersonaDelta[0] {
		t.Fatalf("PersonaDelta = %#v, want %#v", got.PersonaDelta, want.PersonaDelta)
	}
	if !got.Created.Equal(want.Created) {
		t.Fatalf("Created = %v, want %v", got.Created, want.Created)
	}
}

func TestLessonJSONOmitsZeroFields(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(Lesson{
		ID:         "abc12345",
		Text:       "x",
		Source:     "agent",
		Confidence: "medium",
		Created:    time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	encoded := string(data)
	for _, key := range []string{"rationale", "evidence", "persona_param", "persona_direction", "persona_magnitude"} {
		if strings.Contains(encoded, key) {
			t.Fatalf("encoded lesson = %s, want key %q omitted", encoded, key)
		}
	}
}
