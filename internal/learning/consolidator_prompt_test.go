package learning

import (
	"strings"
	"testing"
)

func TestBuildConsolidationPrompt_ContainsAllSections(t *testing.T) {
	req := ConsolidationRequest{
		Lessons: []Lesson{
			{ID: "abc123", Topic: "file writes", Text: "tempfile+rename for atomicity", Confidence: "high"},
			{ID: "def456", Topic: "file writes", Text: "flushing before rename", Confidence: "medium"},
		},
		PriorSyntheses: []SynthesisEntry{
			{ID: "syn-001", Topic: "logging", Summary: "prefer slog over fmt"},
		},
		SessionContext: "3 sessions since last consolidation",
	}
	system, user := BuildConsolidationPrompt(req)

	if !strings.Contains(system, "STRICT JSON") {
		t.Errorf("system prompt missing STRICT JSON directive:\n%s", system)
	}
	if !strings.Contains(system, "syntheses") {
		t.Errorf("system prompt missing schema key 'syntheses':\n%s", system)
	}

	for _, header := range []string{"Phase 1", "Phase 2", "Phase 3", "Phase 4"} {
		if !strings.Contains(user, header) {
			t.Errorf("user prompt missing %q header", header)
		}
	}
	for _, id := range []string{"abc123", "def456", "syn-001"} {
		if !strings.Contains(user, id) {
			t.Errorf("user prompt missing id %q", id)
		}
	}
	if !strings.Contains(user, "3 sessions since last consolidation") {
		t.Errorf("user prompt missing session context")
	}
}

func TestBuildConsolidationPrompt_EmptyInputs(t *testing.T) {
	system, user := BuildConsolidationPrompt(ConsolidationRequest{})
	if system == "" {
		t.Fatal("system prompt empty")
	}
	if !strings.Contains(user, "(none)") {
		t.Errorf("expected (none) sentinel for empty sections, got:\n%s", user)
	}
}

func TestParseConsolidationResponse(t *testing.T) {
	validIDs := map[string]bool{"a1": true, "a2": true, "a3": true, "b1": true}
	cases := []struct {
		name    string
		content string
		want    int
		wantErr bool
	}{
		{
			name: "valid two-lesson synthesis",
			content: `{"syntheses":[
				{"synthesis_text":"prefer tempfile+rename for atomicity","topic_tags":["io"],
				 "superseded_lesson_ids":["a1","a2"],"confidence":"high"}
			]}`,
			want: 1,
		},
		{
			name:    "malformed JSON errors",
			content: `{not json`,
			wantErr: true,
		},
		{
			name: "hallucinated ID drops item",
			content: `{"syntheses":[
				{"synthesis_text":"x","topic_tags":["t"],
				 "superseded_lesson_ids":["a1","zz"],"confidence":"high"}
			]}`,
			want: 0,
		},
		{
			name: "missing confidence drops item",
			content: `{"syntheses":[
				{"synthesis_text":"x","topic_tags":["t"],
				 "superseded_lesson_ids":["a1","a2"],"confidence":""}
			]}`,
			want: 0,
		},
		{
			name: "invalid confidence drops item",
			content: `{"syntheses":[
				{"synthesis_text":"x","topic_tags":["t"],
				 "superseded_lesson_ids":["a1","a2"],"confidence":"unknown"}
			]}`,
			want: 0,
		},
		{
			name: "single superseded id drops item",
			content: `{"syntheses":[
				{"synthesis_text":"x","topic_tags":["t"],
				 "superseded_lesson_ids":["a1"],"confidence":"high"}
			]}`,
			want: 0,
		},
		{
			name: "empty topic_tags drops item",
			content: `{"syntheses":[
				{"synthesis_text":"x","topic_tags":[],
				 "superseded_lesson_ids":["a1","a2"],"confidence":"high"}
			]}`,
			want: 0,
		},
		{
			name: "empty synthesis_text drops item",
			content: `{"syntheses":[
				{"synthesis_text":"","topic_tags":["t"],
				 "superseded_lesson_ids":["a1","a2"],"confidence":"high"}
			]}`,
			want: 0,
		},
		{
			name: "mixed valid+invalid keeps only valid",
			content: `{"syntheses":[
				{"synthesis_text":"good","topic_tags":["t"],
				 "superseded_lesson_ids":["a1","a2"],"confidence":"medium"},
				{"synthesis_text":"","topic_tags":["t"],
				 "superseded_lesson_ids":["a1","a2"],"confidence":"high"}
			]}`,
			want: 1,
		},
		{
			name:    "empty array ok",
			content: `{"syntheses":[]}`,
			want:    0,
		},
		{
			name:    "missing key treated as zero",
			content: `{}`,
			want:    0,
		},
		{
			name: "confidence is case-insensitive",
			content: `{"syntheses":[
				{"synthesis_text":"x","topic_tags":["t"],
				 "superseded_lesson_ids":["a1","a2"],"confidence":"HIGH"}
			]}`,
			want: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := ParseConsolidationResponse(tc.content, validIDs)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := len(out.Syntheses); got != tc.want {
				t.Errorf("got %d syntheses, want %d (items=%+v)", got, tc.want, out.Syntheses)
			}
		})
	}
}

func TestParseConsolidationResponse_PreservesNormalizedFields(t *testing.T) {
	validIDs := map[string]bool{"a1": true, "a2": true}
	content := `{"syntheses":[
		{"synthesis_text":"  atomic file swap wins  ","topic_tags":["  io  ","  fs  "],
		 "superseded_lesson_ids":["a1","a2","a1"],"confidence":"High"}
	]}`
	out, err := ParseConsolidationResponse(content, validIDs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Syntheses) != 1 {
		t.Fatalf("expected 1 synthesis, got %d", len(out.Syntheses))
	}
	s := out.Syntheses[0]
	if s.Text != "atomic file swap wins" {
		t.Errorf("text = %q, want trimmed", s.Text)
	}
	if len(s.TopicTags) != 2 || s.TopicTags[0] != "io" || s.TopicTags[1] != "fs" {
		t.Errorf("tags = %v, want trimmed [io fs]", s.TopicTags)
	}
	if s.Confidence != "high" {
		t.Errorf("confidence = %q, want lowercased", s.Confidence)
	}
}
