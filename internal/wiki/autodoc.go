package wiki

import (
	"context"
	"log/slog"

	"github.com/stello/elnath/internal/llm"
)

// AutoDocumenter ingests completed sessions into the wiki automatically.
type AutoDocumenter struct {
	ingester *Ingester
	logger   *slog.Logger
}

// NewAutoDocumenter creates an AutoDocumenter. provider may be nil for plain ingest without summarisation.
// logger may be nil; a no-op logger is used in that case.
func NewAutoDocumenter(store *Store, provider llm.Provider, logger *slog.Logger) *AutoDocumenter {
	if logger == nil {
		logger = slog.Default()
	}
	return &AutoDocumenter{
		ingester: NewIngester(store, provider),
		logger:   logger,
	}
}

// IngestSession ingests a completed session's messages into the wiki.
// It is safe to call with empty messages — it is a no-op in that case.
func (d *AutoDocumenter) IngestSession(ctx context.Context, sessionID string, messages []llm.Message) error {
	if len(messages) == 0 {
		return nil
	}
	return d.ingester.IngestConversation(ctx, sessionID, messages)
}
