package conversation

import (
	"context"
	"log/slog"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/wiki"
)

const defaultSpineIngestTimeout = 60 * time.Second

type eventIngester interface {
	IngestSession(ctx context.Context, event wiki.IngestEvent) error
}

// Spine snapshots completed sessions and forwards them to wiki ingest.
type Spine struct {
	dataDir       string
	ingester      eventIngester
	ingestTimeout time.Duration
	logger        *slog.Logger
}

// NewSpine constructs a completion sink that mirrors finished sessions into the wiki.
func NewSpine(dataDir string, ingester eventIngester, logger *slog.Logger) *Spine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Spine{
		dataDir:       dataDir,
		ingester:      ingester,
		ingestTimeout: defaultSpineIngestTimeout,
		logger:        logger,
	}
}

// WithIngestTimeout overrides the background ingest timeout.
func (s *Spine) WithIngestTimeout(d time.Duration) *Spine {
	if d <= 0 {
		d = defaultSpineIngestTimeout
	}
	s.ingestTimeout = d
	return s
}

func (s *Spine) NotifyCompletion(_ context.Context, completion daemon.TaskCompletion) error {
	if s.ingester == nil || completion.SessionID == "" {
		return nil
	}

	sess, err := agent.LoadSession(s.dataDir, completion.SessionID)
	if err != nil {
		s.logger.Warn("conversation spine: load session failed", "session_id", completion.SessionID, "error", err)
		return nil
	}

	event := wiki.IngestEvent{
		SessionID: completion.SessionID,
		Messages:  sess.SnapshotMessages(),
		Reason:    "task_completed",
		Principal: sess.Principal.SurfaceIdentity(),
		StartedAt: completion.StartedAt,
		Duration:  completion.Duration(),
	}
	go s.runIngest(event)

	return nil
}

func (s *Spine) runIngest(event wiki.IngestEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), s.ingestTimeout)
	defer cancel()
	if err := s.ingester.IngestSession(ctx, event); err != nil {
		s.logger.Warn("conversation spine: ingest failed",
			"session_id", event.SessionID,
			"reason", event.Reason,
			"error", err,
		)
	}
}

func (s *Spine) String() string {
	return "ConversationSpine"
}
