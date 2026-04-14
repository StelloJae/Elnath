package learning

import (
	"strings"
	"testing"
)

func TestExtractAgent(t *testing.T) {
	stubStats := func(pairs ...any) []AgentToolStat {
		out := []AgentToolStat{}
		for i := 0; i+2 < len(pairs); i += 3 {
			out = append(out, AgentToolStat{
				Name:   pairs[i].(string),
				Calls:  pairs[i+1].(int),
				Errors: pairs[i+2].(int),
			})
		}
		return out
	}

	assertDelta := func(t *testing.T, got Lesson, wantParams ...any) {
		t.Helper()
		if len(got.PersonaDelta) != len(wantParams)/2 {
			t.Fatalf("len(PersonaDelta) = %d, want %d", len(got.PersonaDelta), len(wantParams)/2)
		}
		for i := 0; i < len(wantParams); i += 2 {
			delta := got.PersonaDelta[i/2]
			if delta.Param != wantParams[i].(string) || delta.Delta != wantParams[i+1].(float64) {
				t.Fatalf("PersonaDelta[%d] = %#v, want (%q, %v)", i/2, delta, wantParams[i].(string), wantParams[i+1].(float64))
			}
		}
	}

	t.Run("rules", func(t *testing.T) {
		tests := []struct {
			name       string
			info       AgentResultInfo
			wantCount  int
			assertions func(t *testing.T, lessons []Lesson)
		}{
			{
				name:      "empty info produces no lessons",
				info:      AgentResultInfo{},
				wantCount: 0,
			},
			{
				name: "tool failure loop creates caution lesson",
				info: AgentResultInfo{
					Topic:     "repo cleanup",
					ToolStats: stubStats("bash", 5, 3),
				},
				wantCount: 1,
				assertions: func(t *testing.T, lessons []Lesson) {
					t.Helper()
					if !strings.Contains(lessons[0].Text, "bash") {
						t.Fatalf("Text = %q, want bash mention", lessons[0].Text)
					}
					assertDelta(t, lessons[0], "caution", 0.02)
				},
			},
			{
				name: "tool failure threshold not met",
				info: AgentResultInfo{
					Topic:     "repo cleanup",
					ToolStats: stubStats("bash", 5, 2),
				},
				wantCount: 0,
			},
			{
				name: "budget exceeded creates stalled lesson",
				info: AgentResultInfo{
					Topic:         "repo cleanup",
					FinishReason:  "budget_exceeded",
					Iterations:    50,
					MaxIterations: 50,
				},
				wantCount: 1,
				assertions: func(t *testing.T, lessons []Lesson) {
					t.Helper()
					if !strings.Contains(lessons[0].Text, "50/50") {
						t.Fatalf("Text = %q, want iteration ratio", lessons[0].Text)
					}
					assertDelta(t, lessons[0], "caution", 0.03, "verbosity", -0.01)
				},
			},
			{
				name: "efficient completion with tool usage boosts persistence",
				info: AgentResultInfo{
					Topic:         "repo cleanup",
					FinishReason:  "stop",
					Iterations:    3,
					MaxIterations: 50,
					ToolStats:     stubStats("bash", 1, 0),
				},
				wantCount: 1,
				assertions: func(t *testing.T, lessons []Lesson) {
					t.Helper()
					if !strings.Contains(lessons[0].Text, "Efficient completion") {
						t.Fatalf("Text = %q, want efficient completion hint", lessons[0].Text)
					}
					assertDelta(t, lessons[0], "persistence", 0.01)
				},
			},
			{
				name: "efficient completion ignores trivial success without tool calls",
				info: AgentResultInfo{
					Topic:         "repo cleanup",
					FinishReason:  "stop",
					Iterations:    3,
					MaxIterations: 50,
				},
				wantCount: 0,
			},
			{
				name: "inefficient completion does not create persistence lesson",
				info: AgentResultInfo{
					Topic:         "repo cleanup",
					FinishReason:  "stop",
					Iterations:    40,
					MaxIterations: 50,
					ToolStats:     stubStats("bash", 1, 0),
				},
				wantCount: 0,
			},
			{
				name: "verbose output reduces verbosity",
				info: AgentResultInfo{
					Topic:        "repo cleanup",
					OutputTokens: 60000,
				},
				wantCount: 1,
				assertions: func(t *testing.T, lessons []Lesson) {
					t.Helper()
					if !strings.Contains(lessons[0].Text, "60000") {
						t.Fatalf("Text = %q, want token count", lessons[0].Text)
					}
					assertDelta(t, lessons[0], "verbosity", -0.02)
				},
			},
			{
				name: "composite case emits four lessons",
				info: AgentResultInfo{
					Topic:         "repo cleanup",
					FinishReason:  "budget_exceeded",
					Iterations:    50,
					MaxIterations: 50,
					OutputTokens:  70000,
					ToolStats:     stubStats("bash", 7, 5, "file", 6, 4),
				},
				wantCount: 4,
				assertions: func(t *testing.T, lessons []Lesson) {
					t.Helper()
					joined := lessons[0].Text + "\n" + lessons[1].Text + "\n" + lessons[2].Text + "\n" + lessons[3].Text
					for _, want := range []string{"bash", "file", "50/50", "70000"} {
						if !strings.Contains(joined, want) {
							t.Fatalf("joined lesson text missing %q: %q", want, joined)
						}
					}
				},
			},
			{
				name: "blank topic falls back to agent-task",
				info: AgentResultInfo{
					FinishReason:  "budget_exceeded",
					Iterations:    4,
					MaxIterations: 4,
				},
				wantCount: 1,
				assertions: func(t *testing.T, lessons []Lesson) {
					t.Helper()
					if !strings.Contains(lessons[0].Text, "agent-task") {
						t.Fatalf("Text = %q, want fallback topic", lessons[0].Text)
					}
				},
			},
			{
				name: "long topic is truncated through shared helper",
				info: AgentResultInfo{
					Topic:        strings.Repeat("topic-", 40),
					OutputTokens: 60000,
				},
				wantCount: 1,
				assertions: func(t *testing.T, lessons []Lesson) {
					t.Helper()
					if len(lessons[0].Text) != maxLessonTextLen {
						t.Fatalf("len(Text) = %d, want %d", len(lessons[0].Text), maxLessonTextLen)
					}
					if !strings.HasSuffix(lessons[0].Text, "...") {
						t.Fatalf("Text = %q, want ellipsis suffix", lessons[0].Text)
					}
				},
			},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				lessons := ExtractAgent(tt.info)
				if len(lessons) != tt.wantCount {
					t.Fatalf("len(lessons) = %d, want %d", len(lessons), tt.wantCount)
				}
				for i, lesson := range lessons {
					if lesson.Created.IsZero() {
						t.Fatalf("lessons[%d].Created = zero, want timestamp", i)
					}
					if lesson.Source != "agent" {
						t.Fatalf("lessons[%d].Source = %q, want agent", i, lesson.Source)
					}
				}
				if tt.assertions != nil {
					tt.assertions(t, lessons)
				}
			})
		}
	})
}
