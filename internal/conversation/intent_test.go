package conversation

import (
	"testing"
)

func TestParseIntentResponse(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantIntent  Intent
		wantErr     bool
	}{
		{
			name:       "plain JSON question",
			input:      `{"intent": "question", "confidence": 0.9}`,
			wantIntent: IntentQuestion,
		},
		{
			name:       "simple_task",
			input:      `{"intent": "simple_task", "confidence": 0.8}`,
			wantIntent: IntentSimpleTask,
		},
		{
			name:       "complex_task",
			input:      `{"intent": "complex_task", "confidence": 0.75}`,
			wantIntent: IntentComplexTask,
		},
		{
			name:       "project",
			input:      `{"intent": "project", "confidence": 0.6}`,
			wantIntent: IntentProject,
		},
		{
			name:       "research",
			input:      `{"intent": "research", "confidence": 0.95}`,
			wantIntent: IntentResearch,
		},
		{
			name:       "unclear",
			input:      `{"intent": "unclear", "confidence": 0.3}`,
			wantIntent: IntentUnclear,
		},
		{
			name:       "chat",
			input:      `{"intent": "chat", "confidence": 1.0}`,
			wantIntent: IntentChat,
		},
		{
			name:       "leading whitespace and trailing newline",
			input:      "  \n{\"intent\": \"question\", \"confidence\": 0.7}\n  ",
			wantIntent: IntentQuestion,
		},
		{
			name:       "markdown code fence json",
			input:      "```json\n{\"intent\": \"chat\", \"confidence\": 0.5}\n```",
			wantIntent: IntentChat,
		},
		{
			name:       "markdown code fence plain",
			input:      "```\n{\"intent\": \"research\", \"confidence\": 0.8}\n```",
			wantIntent: IntentResearch,
		},
		{
			name:    "invalid JSON no braces",
			input:   "intent: question",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "unknown intent category",
			input:   `{"intent": "coding", "confidence": 0.9}`,
			wantErr: true,
		},
		{
			name:    "missing intent field",
			input:   `{"confidence": 0.9}`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseIntentResponse(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got result=%+v", result)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Intent != tc.wantIntent {
				t.Errorf("Intent = %q, want %q", result.Intent, tc.wantIntent)
			}
		})
	}
}

func TestIntentConstants(t *testing.T) {
	all := []Intent{
		IntentQuestion,
		IntentSimpleTask,
		IntentComplexTask,
		IntentProject,
		IntentResearch,
		IntentWikiQuery,
		IntentUnclear,
		IntentChat,
	}

	if len(all) != 8 {
		t.Errorf("expected 8 intent constants, got %d", len(all))
	}

	seen := make(map[Intent]bool, len(all))
	for _, intent := range all {
		if intent == "" {
			t.Errorf("intent constant must not be empty string")
		}
		if seen[intent] {
			t.Errorf("duplicate intent constant: %q", intent)
		}
		seen[intent] = true
	}
}
