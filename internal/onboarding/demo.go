package onboarding

import (
	"context"
	"fmt"

	"github.com/stello/elnath/internal/llm"
)

// RunDemoTask submits a tiny prompt through the injected provider.
func RunDemoTask(ctx context.Context, provider llm.Provider, model string) error {
	fmt.Printf("\nDemo: asking %q - \"what is 2+2?\"\n\n", model)
	req := llm.ChatRequest{
		Model: model,
		Messages: []llm.Message{
			llm.NewUserMessage("what is 2+2?"),
		},
		MaxTokens: 64,
	}
	return provider.Stream(ctx, req, func(ev llm.StreamEvent) {
		if ev.Type == llm.EventTextDelta {
			fmt.Print(ev.Content)
		}
	})
}
