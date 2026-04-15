package prompt

import (
	"context"
	"testing"
)

func TestLocaleInstructionNodeRender(t *testing.T) {
	t.Parallel()

	node := &LocaleInstructionNode{}
	tests := []struct {
		name  string
		state *RenderState
		want  string
	}{
		{name: "nil state", state: nil, want: ""},
		{name: "empty locale", state: &RenderState{}, want: ""},
		{name: "english locale", state: &RenderState{Locale: "en"}, want: ""},
		{name: "korean locale", state: &RenderState{Locale: "ko"}, want: "Respond in Korean."},
		{name: "japanese locale", state: &RenderState{Locale: "ja"}, want: "Respond in Japanese."},
		{name: "chinese locale", state: &RenderState{Locale: "zh"}, want: "Respond in Chinese."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := node.Render(context.Background(), tc.state)
			if err != nil {
				t.Fatalf("Render error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Render = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLocaleInstructionNodePriority(t *testing.T) {
	t.Parallel()

	if got := (&LocaleInstructionNode{}).Priority(); got != 999 {
		t.Fatalf("Priority() = %d, want 999", got)
	}
}

func TestLocaleInstructionNodeDeterministic(t *testing.T) {
	t.Parallel()

	b := NewBuilder()
	b.Register(&LocaleInstructionNode{})
	state := &RenderState{Locale: "ko"}

	first, err := b.Build(context.Background(), state)
	if err != nil {
		t.Fatalf("first Build error: %v", err)
	}
	second, err := b.Build(context.Background(), state)
	if err != nil {
		t.Fatalf("second Build error: %v", err)
	}
	if first != second {
		t.Fatalf("Build outputs differ: %q != %q", first, second)
	}
}
