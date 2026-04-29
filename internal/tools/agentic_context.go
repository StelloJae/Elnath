package tools

import "context"

type AgenticContext struct {
	TaskID         int64
	ActorID        int64
	ToolCallID     string
	ActionKind     string
	FinalizeResult bool
}

type agenticContextKey struct{}

func WithAgenticContext(ctx context.Context, c AgenticContext) context.Context {
	if existing, ok := AgenticContextFrom(ctx); ok {
		if c.TaskID == 0 {
			c.TaskID = existing.TaskID
		}
		if c.ActorID == 0 {
			c.ActorID = existing.ActorID
		}
		if c.ToolCallID == "" {
			c.ToolCallID = existing.ToolCallID
		}
		if c.ActionKind == "" {
			c.ActionKind = existing.ActionKind
		}
	}
	return context.WithValue(ctx, agenticContextKey{}, c)
}

func WithAgenticToolCallID(ctx context.Context, id string) context.Context {
	c, _ := AgenticContextFrom(ctx)
	c.ToolCallID = id
	c.FinalizeResult = true
	return WithAgenticContext(ctx, c)
}

func AgenticContextFrom(ctx context.Context) (AgenticContext, bool) {
	c, ok := ctx.Value(agenticContextKey{}).(AgenticContext)
	return c, ok
}
