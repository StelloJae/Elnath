package learning

import (
	"strings"
	"testing"
	"time"
)

func stubAgentToolStats(pairs ...any) []AgentToolStat {
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

func assertLessonDelta(t *testing.T, got Lesson, wantParams ...any) {
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

func TestExtractAgent(t *testing.T) {
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
					ToolStats: stubAgentToolStats("bash", 5, 3),
				},
				wantCount: 1,
				assertions: func(t *testing.T, lessons []Lesson) {
					t.Helper()
					if !strings.Contains(lessons[0].Text, "bash") {
						t.Fatalf("Text = %q, want bash mention", lessons[0].Text)
					}
					assertLessonDelta(t, lessons[0], "caution", 0.02)
				},
			},
			{
				name: "tool failure threshold not met",
				info: AgentResultInfo{
					Topic:     "repo cleanup",
					ToolStats: stubAgentToolStats("bash", 5, 2),
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
					assertLessonDelta(t, lessons[0], "caution", 0.03, "verbosity", -0.01)
				},
			},
			{
				name: "efficient completion with tool usage boosts persistence",
				info: AgentResultInfo{
					Topic:         "repo cleanup",
					FinishReason:  "stop",
					Iterations:    3,
					MaxIterations: 50,
					ToolStats:     stubAgentToolStats("bash", 1, 0),
				},
				wantCount: 1,
				assertions: func(t *testing.T, lessons []Lesson) {
					t.Helper()
					if !strings.Contains(lessons[0].Text, "Efficient completion") {
						t.Fatalf("Text = %q, want efficient completion hint", lessons[0].Text)
					}
					assertLessonDelta(t, lessons[0], "persistence", 0.01)
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
					ToolStats:     stubAgentToolStats("bash", 1, 0),
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
					assertLessonDelta(t, lessons[0], "verbosity", -0.02)
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
					ToolStats:     stubAgentToolStats("bash", 7, 5, "file", 6, 4),
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

func TestExtractAgent_RalphRetry(t *testing.T) {
	tests := []struct {
		name       string
		retryCount int
		workflow   string
		wantCount  int
		assertions func(t *testing.T, lesson Lesson)
	}{
		{
			name:       "no retry",
			retryCount: 0,
			workflow:   "ralph",
			wantCount:  0,
		},
		{
			name:       "below threshold",
			retryCount: 2,
			workflow:   "ralph",
			wantCount:  0,
		},
		{
			name:       "at threshold",
			retryCount: 3,
			workflow:   "ralph",
			wantCount:  1,
			assertions: func(t *testing.T, lesson Lesson) {
				t.Helper()
				if !strings.Contains(lesson.Text, "retried 3 times") {
					t.Fatalf("Text = %q, want retry count", lesson.Text)
				}
				if lesson.Source != "agent:ralph" {
					t.Fatalf("Source = %q, want agent:ralph", lesson.Source)
				}
				assertLessonDelta(t, lesson, "caution", 0.02)
			},
		},
		{
			name:       "high retry",
			retryCount: 5,
			workflow:   "ralph",
			wantCount:  1,
			assertions: func(t *testing.T, lesson Lesson) {
				t.Helper()
				if !strings.Contains(lesson.Text, "retried 5 times") {
					t.Fatalf("Text = %q, want retry count", lesson.Text)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			lessons := ExtractAgent(AgentResultInfo{
				Topic:      "repo cleanup",
				RetryCount: tt.retryCount,
				Workflow:   tt.workflow,
			})
			if len(lessons) != tt.wantCount {
				t.Fatalf("len(lessons) = %d, want %d", len(lessons), tt.wantCount)
			}
			if tt.assertions != nil {
				tt.assertions(t, lessons[0])
			}
		})
	}
}

func TestExtractAgent_SourceSuffix(t *testing.T) {
	workflows := []string{"", "single", "team", "ralph", "autopilot"}
	rules := []struct {
		name string
		info func(string) AgentResultInfo
	}{
		{
			name: "tool failure",
			info: func(workflow string) AgentResultInfo {
				return AgentResultInfo{
					Topic:     "repo cleanup",
					Workflow:  workflow,
					ToolStats: stubAgentToolStats("bash", 5, 3),
				}
			},
		},
		{
			name: "budget exceeded",
			info: func(workflow string) AgentResultInfo {
				return AgentResultInfo{
					Topic:         "repo cleanup",
					Workflow:      workflow,
					FinishReason:  "budget_exceeded",
					Iterations:    50,
					MaxIterations: 50,
				}
			},
		},
		{
			name: "efficient completion",
			info: func(workflow string) AgentResultInfo {
				return AgentResultInfo{
					Topic:         "repo cleanup",
					Workflow:      workflow,
					FinishReason:  "stop",
					Iterations:    3,
					MaxIterations: 50,
					ToolStats:     stubAgentToolStats("bash", 1, 0),
				}
			},
		},
		{
			name: "verbose output",
			info: func(workflow string) AgentResultInfo {
				return AgentResultInfo{
					Topic:        "repo cleanup",
					Workflow:     workflow,
					OutputTokens: 60000,
				}
			},
		},
		{
			name: "retry instability",
			info: func(workflow string) AgentResultInfo {
				return AgentResultInfo{
					Topic:      "repo cleanup",
					Workflow:   workflow,
					RetryCount: 3,
				}
			},
		},
	}

	for _, workflow := range workflows {
		workflow := workflow
		for _, rule := range rules {
			rule := rule
			name := workflow
			wantSource := "agent"
			if workflow == "" {
				name = "legacy"
			} else {
				wantSource = "agent:" + workflow
			}

			t.Run(rule.name+"/"+name, func(t *testing.T) {
				lessons := ExtractAgent(rule.info(workflow))
				if len(lessons) != 1 {
					t.Fatalf("len(lessons) = %d, want 1", len(lessons))
				}
				if lessons[0].Source != wantSource {
					t.Fatalf("Source = %q, want %q", lessons[0].Source, wantSource)
				}
			})
		}
	}
}

func TestMergeAgentToolStats(t *testing.T) {
	tests := []struct {
		name  string
		input [][]AgentToolStat
		want  []AgentToolStat
	}{
		{
			name:  "empty input",
			input: nil,
			want:  []AgentToolStat{},
		},
		{
			name: "single slice drops zero calls and sorts",
			input: [][]AgentToolStat{{
				{Name: "zsh", Calls: 2, Errors: 1, TotalTime: 3 * time.Second},
				{Name: "apply_patch", Calls: 0, Errors: 7, TotalTime: time.Second},
				{Name: "bash", Calls: 1, Errors: 0, TotalTime: 2 * time.Second},
			}},
			want: []AgentToolStat{
				{Name: "bash", Calls: 1, Errors: 0, TotalTime: 2 * time.Second},
				{Name: "zsh", Calls: 2, Errors: 1, TotalTime: 3 * time.Second},
			},
		},
		{
			name: "overlapping names sum calls errors and time",
			input: [][]AgentToolStat{
				{{Name: "bash", Calls: 1, Errors: 1, TotalTime: time.Second}},
				{{Name: "bash", Calls: 3, Errors: 2, TotalTime: 4 * time.Second}},
			},
			want: []AgentToolStat{{Name: "bash", Calls: 4, Errors: 3, TotalTime: 5 * time.Second}},
		},
		{
			name: "zero call entries are excluded after merge",
			input: [][]AgentToolStat{
				{{Name: "bash", Calls: 0, Errors: 2, TotalTime: time.Second}},
				{{Name: "bash", Calls: 0, Errors: 1, TotalTime: 2 * time.Second}},
			},
			want: []AgentToolStat{},
		},
		{
			name: "distinct names are preserved across slices",
			input: [][]AgentToolStat{
				{{Name: "bash", Calls: 1, Errors: 0, TotalTime: time.Second}},
				{{Name: "read", Calls: 2, Errors: 1, TotalTime: 2 * time.Second}},
			},
			want: []AgentToolStat{
				{Name: "bash", Calls: 1, Errors: 0, TotalTime: time.Second},
				{Name: "read", Calls: 2, Errors: 1, TotalTime: 2 * time.Second},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := MergeAgentToolStats(tt.input...)
			if len(got) != len(tt.want) {
				t.Fatalf("len(got) = %d, want %d", len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("got[%d] = %#v, want %#v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
