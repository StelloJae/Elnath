package llm

import "context"

type RefreshableProvider interface {
	Provider
	Refresh(ctx context.Context) error
}

func RefreshIfSupported(ctx context.Context, p Provider) error {
	if refreshable, ok := p.(RefreshableProvider); ok {
		return refreshable.Refresh(ctx)
	}
	return nil
}
