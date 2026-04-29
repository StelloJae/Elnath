package agentictools

import (
	"context"

	basetools "github.com/stello/elnath/internal/tools"
)

type Context = basetools.AgenticContext

func WithContext(ctx context.Context, c Context) context.Context {
	return basetools.WithAgenticContext(ctx, basetools.AgenticContext(c))
}

func ContextFrom(ctx context.Context) (Context, bool) {
	c, ok := basetools.AgenticContextFrom(ctx)
	return Context(c), ok
}
